package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
	"github.com/kennyjsa/kluzo/internal/platform/text"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/prompt"
	"github.com/kennyjsa/kluzo/internal/store"
)

// rulesFiles are the project rules read from the milestone tip (ADR 0024).
var rulesFiles = []string{"AGENTS.md", "CLAUDE.md"}

// slugChars are the runs of characters a branch slug keeps.
var slugChars = regexp.MustCompile(`[a-z0-9]+`)

// branch is the node branch: branches.node_pattern with {number} and {slug}.
func (s *Supervisor) branch(n store.NodeRow) string {
	pattern := s.cfg.Project.Config.Branches.NodePattern
	if pattern == "" {
		pattern = "issue/{number}-{slug}"
	}
	slug := strings.Join(slugChars.FindAllString(strings.ToLower(n.Title), -1), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	return strings.NewReplacer("{number}", n.IssueRef, "{slug}", slug).Replace(pattern)
}

func (s *Supervisor) worktree(n store.NodeRow) string {
	return filepath.Join(s.cfg.Dir, "worktrees", n.IssueRef)
}

// projectEnv is what the sessions and the setup of a node get besides the
// fixed part: the cache variables and COMPOSE_PROJECT_NAME (ADR 0025).
func (s *Supervisor) projectEnv(n store.NodeRow) (map[string]string, error) {
	caches, err := sessionenv.Caches(s.cfg.Project.Config.Caches, s.cfg.Downloads, filepath.Join(s.cfg.Dir, "cache"))
	if err != nil {
		return nil, err
	}
	return sessionenv.Merge(caches, sessionenv.Compose(s.cfg.Workspace, n.IssueRef)), nil
}

// prepare gives an attempt a new worktree from the milestone tip: the branch
// of the previous attempt is deleted first (AddWorktree would reuse it), then
// the worktree is created and prepared. It returns the commit it started from.
func (s *Supervisor) prepare(ctx context.Context, n store.NodeRow) (string, error) {
	repo, wt, branch := s.cfg.Repo, s.worktree(n), s.branch(n)
	if err := s.d.Git.RemoveWorktree(ctx, repo, wt, branch); err != nil {
		return "", err
	}
	base, err := s.d.Git.AddWorktree(ctx, gitops.WorktreeSpec{Repo: repo, Path: wt, Branch: branch, Base: s.cfg.Branch})
	if err != nil {
		return "", err
	}
	extra, err := s.projectEnv(n)
	if err != nil {
		return "", err
	}
	env := sessionenv.Build(sessionenv.Spec{Parent: s.cfg.ParentEnv, Home: filepath.Join(s.cfg.Dir, "home"), Allow: s.cfg.Project.Config.Worktree.EnvAllow, Extra: extra})
	return base, s.d.Git.Prepare(ctx, gitops.PrepareSpec{Repo: repo, Worktree: wt, Base: s.cfg.Branch, Env: env, DefaultTimeout: 15 * time.Minute})
}

// session runs the n-th run of a session effect: one role session of the
// node flow, from the worktree to the post-session check, and stores the
// outcome as an input of the core. A retryable failure is retried by the
// outbox, at most sessionRuns runs (ADR 0024, item 7).
func (s *Supervisor) session(ctx context.Context, f store.Effect, runNo int, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	r, err := run(n)
	if err != nil {
		return err
	}
	if r.Step != p.Do.Step {
		return s.decide(ctx, n, core.SessionDone{Step: p.Do.Step}, completed(f)) // stale: dropped by the core
	}
	st := s.flow.Steps[r.Step]
	done := core.SessionDone{Step: r.Step}
	left := s.budget(n) - r.CostUSD
	if left <= 0 {
		return s.decideSession(ctx, f, n, done, ports.Result{State: ports.StateFailed, Cause: ports.CauseBudget, Detail: "the node has no budget left"})
	}
	if st.Role == core.RoleImplementer && r.Fresh {
		base, err := s.prepare(ctx, n)
		var setup *gitops.SetupError
		switch {
		case errors.As(err, &setup):
			// The output of the setup stays out of the comment: it may hold secrets.
			return s.failedRun(ctx, f, runNo, n, done, ports.CauseRuntime, setupSummary(setup))
		case err != nil:
			return err
		}
		done.Base = base
	}
	var round []gitops.DiffFile
	if st.Role == core.RoleReviewer && n.Round > 0 {
		if round, err = s.d.Git.Diff(ctx, s.cfg.Repo, r.RoundBase, r.Head); err != nil {
			return err
		}
	}
	// An error before the harness session starts is retried by the outbox.
	// Once it started, every failure is a result of the session, so its cost
	// is stored and the session policy applies.
	res, hs, err := s.play(ctx, f, n, r, st, runNo, left, round, &done)
	if err != nil {
		return err
	}
	done.Session = hs
	if res.State == ports.StateFinished && len(res.Raw) > 0 && !done.Tampered {
		if err := s.afterSession(ctx, n, st, round, &done); err != nil {
			res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "after the session: " + err.Error(), CostUSD: res.CostUSD, Cumulative: res.Cumulative}
		}
	}
	if res.State == ports.StateFailed && res.Cause.Retryable() && !done.Tampered && runNo < sessionRuns {
		return s.decide(ctx, n, core.SessionRetry{Step: r.Step, CostUSD: res.CostUSD}, retried(f, runNo, string(res.Cause)+": "+res.Detail))
	}
	return s.decideSession(ctx, f, n, done, res)
}

// setupSummary describes a failed worktree.setup without its output.
func setupSummary(e *gitops.SetupError) string {
	switch {
	case e.TimedOut:
		return "worktree.setup timed out"
	case e.Err != nil:
		return "worktree.setup could not start"
	default:
		return fmt.Sprintf("worktree.setup exited %d", e.Code)
	}
}

// failedRun ends a run that could not start a session.
func (s *Supervisor) failedRun(ctx context.Context, f store.Effect, runNo int, n store.NodeRow, done core.SessionDone, cause ports.Cause, detail string) error {
	if cause.Retryable() && runNo < sessionRuns {
		return s.decide(ctx, n, core.SessionRetry{Step: done.Step}, retried(f, runNo, detail))
	}
	return s.decideSession(ctx, f, n, done, ports.Result{State: ports.StateFailed, Cause: cause, Detail: detail})
}

// decideSession stores the outcome of a session as SessionDone.
func (s *Supervisor) decideSession(ctx context.Context, f store.Effect, n store.NodeRow, done core.SessionDone, res ports.Result) error {
	done.State, done.Cause, done.CostUSD = res.State, res.Cause, res.CostUSD
	done.Detail = text.Clip(res.Detail, contracts.SummaryMax, text.Ellipsis)
	if res.State == ports.StateFinished {
		done.Raw = res.Raw
	}
	return s.decide(ctx, n, done, completed(f))
}

// budget is what the node may spend, in micro-dollars.
func (s *Supervisor) budget(n store.NodeRow) int64 {
	if n.Budget.USDMicros > 0 {
		return n.Budget.USDMicros
	}
	return int64(s.cfg.Project.Config.Budgets.Node.USD * 1e6)
}

// afterSession records what an accepted session produced: the implementer's
// work as a commit, and for a review from round 2 the lines the round changed.
func (s *Supervisor) afterSession(ctx context.Context, n store.NodeRow, st core.FlowStep, round []gitops.DiffFile, done *core.SessionDone) error {
	if st.Role == core.RoleImplementer {
		head, err := s.d.Git.Commit(ctx, s.cfg.Repo, s.worktree(n), fmt.Sprintf("kluzo: #%s attempt %d round %d", n.IssueRef, n.Attempt, n.Round))
		done.Head = head
		return err
	}
	if round == nil {
		return nil
	}
	done.Changed = map[string][]core.LineRange{}
	for _, f := range round {
		for _, l := range f.Lines {
			done.Changed[f.Path] = append(done.Changed[f.Path], core.LineRange{From: l[0], To: l[1]})
		}
	}
	return nil
}

// play assembles the prompt, opens the MCP session and runs the harness
// session, resuming the implementer's conversation when its segments did not
// change (prompt.md#sessões, ADR 0024). Then it checks the repository: a
// change outside the node branch sets done.Tampered. It returns an error only
// when no harness session ran.
func (s *Supervisor) play(ctx context.Context, f store.Effect, n store.NodeRow, r core.Run, st core.FlowStep, runNo int, left int64, round []gitops.DiffFile, done *core.SessionDone) (ports.Result, *core.HarnessSession, error) {
	role := s.cfg.Project.Roles[st.Role]
	if role == nil {
		return ports.Result{}, nil, fmt.Errorf("role %s is not in the project: %w", st.Role, ports.ErrPermanent)
	}
	schema, ok := contracts.OutputSchema(role.Output)
	if !ok {
		return ports.Result{}, nil, fmt.Errorf("output schema %s of role %s: %w", role.Output, role.Name, ports.ErrPermanent)
	}
	in, err := s.promptInput(ctx, n, r, st, role, schema, round)
	if err != nil {
		return ports.Result{}, nil, err
	}
	model := role.Model
	if model == "" {
		model = s.cfg.Project.Config.Defaults.Model
	}
	pr, err := prompt.Build(in, s.d.Nonce)
	if err != nil {
		return ports.Result{}, nil, err
	}
	seg := pr.Manifest(prompt.Runtime{}).Segments
	segments := ports.SegmentHashes{Protocol: seg.Protocol, Role: seg.Role, Rules: seg.Rules}
	// The implementer resumes its conversation within an attempt when the
	// model, the harness and the stable segments are the same; it then gets
	// only the reduced task, under the nonce of the conversation.
	prev, task, nonce := r.Implementer, pr.Task, pr.Nonce
	resume := st.Role == core.RoleImplementer && prev != nil && prev.Model == model && prev.Harness == s.d.Runtime.Name() && prev.Segments == segments
	if resume {
		in.Task.Reduced = true
		reduced, err := prompt.Build(in, func() (string, error) { return prev.Nonce, nil })
		switch {
		case errors.Is(err, prompt.ErrNonceCollision):
			resume = false
		case err != nil:
			return ports.Result{}, nil, err
		default:
			task, nonce = reduced.Task, prev.Nonce
		}
	}

	wt := s.worktree(n)
	// One Kluzo session per effect; its runs are the harness sessions, numbered
	// by Counter.
	sessionID := "ss_" + strings.TrimPrefix(f.ID, "ef_")
	check := s.check(st, n, r, wt)
	cfg := s.cfg.Project.Config
	ms, err := s.d.MCP.Open(mcp.SessionSpec{
		ID: sessionID, Workspace: s.cfg.Workspace, Node: n.ID, Role: role.Name, Tools: role.MCP, Schema: schema, Check: check,
		Guard: guard.Policy{Workdir: wt, ProtectedPaths: cfg.ProtectedPaths, SecretPaths: cfg.SecretPaths},
	})
	if err != nil {
		return ports.Result{}, nil, err
	}
	defer ms.Close()
	env, err := s.projectEnv(n)
	if err != nil {
		return ports.Result{}, nil, err
	}
	snap, err := s.d.Git.Snapshot(ctx, s.cfg.Repo)
	if err != nil {
		return ports.Result{}, nil, err
	}
	spec := ports.SessionSpec{
		SessionID: sessionID, Counter: runNo - 1, Attempt: n.Attempt, Round: n.Round,
		Role:   ports.RoleConfig{Name: role.Name, Model: model, Effort: role.Effort, Tools: role.Tools, MaxTurns: role.MaxTurns},
		System: pr.System(), Prompt: task, Segments: segments, Workdir: wt, Verify: verify(in.Task.Verify),
		MCPEndpoint: ms.URL(), MCPToken: ms.Token(), MCPTools: []string{mcp.ToolResult}, Accepted: ms.Accepted,
		HookEndpoint: ms.HookURL(), Hooked: ms.Hooked, Env: env, MaxBudgetUSDMicros: left,
		Timeout: time.Duration(role.Budget.Minutes) * time.Minute,
	}
	var sess ports.Session
	if resume {
		spec.PriorUsage = prev.Cumulative
		sess, err = s.d.Runtime.Resume(ctx, prev.Ref, spec)
		if errors.Is(err, ports.ErrResumeMismatch) {
			resume = false
		}
	}
	if !resume {
		spec.Prompt, spec.PriorUsage, nonce = pr.Task, ports.Usage{}, pr.Nonce
		sess, err = s.d.Runtime.Start(ctx, spec)
	}
	branch := "refs/heads/" + s.branch(n)
	if err != nil {
		if ports.IsPermanent(err) {
			return ports.Result{}, nil, err
		}
		res := ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: err.Error()}
		return s.checked(ctx, snap, branch, res, done), nil, nil
	}
	if err := s.journal(ctx, n.ID, sessionID, sess.Events()); err != nil {
		s.d.Log.Warn("journal of session events failed", "session", sessionID, "err", err)
	}
	res, err := sess.Wait()
	if err != nil {
		res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "wait: " + err.Error()}
	}
	res = s.checked(ctx, snap, branch, res, done)
	return res, &core.HarnessSession{Ref: sess.HarnessRef(), Harness: s.d.Runtime.Name(), Model: model, Segments: segments, Nonce: nonce, Cumulative: res.Cumulative}, nil
}

// checked runs the post-session check on the result of a session: a change
// outside the node branch sets done.Tampered (the node fails), and a check
// that could not run makes the session a runtime failure, which is retried.
func (s *Supervisor) checked(ctx context.Context, snap gitops.Snapshot, branch string, res ports.Result, done *core.SessionDone) ports.Result {
	err := s.d.Git.Check(ctx, snap, branch)
	switch {
	case errors.Is(err, gitops.ErrTampered):
		done.Tampered, res.Detail = true, err.Error()
	case err != nil:
		res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "post-session check: " + err.Error(), CostUSD: res.CostUSD, Cumulative: res.Cumulative}
	}
	return res
}

// check is the validation the core adds to the schema of the role's output
// (ADR 0023): coverage and evidence. Its problems share the single
// correction with the schema's (Q31).
func (s *Supervisor) check(st core.FlowStep, n store.NodeRow, r core.Run, wt string) func(json.RawMessage) []string {
	files := gitops.NewFiles(wt)
	criteria := s.cfg.Criteria[n.IssueRef]
	open := r.Findings.Open()
	if st.Role == core.RoleReviewer && n.Round == 0 {
		open = nil // previous is only answered from round 2
	}
	return func(raw json.RawMessage) []string {
		var problems []string
		var err error
		if st.Role == core.RoleImplementer {
			problems, err = core.CheckNodeResult(raw, core.NodeCheck{Criteria: criteria, Open: open, Files: files})
		} else {
			problems, err = core.CheckReviewResult(raw, core.ReviewCheck{Criteria: criteria, Open: open, Files: files})
		}
		if err != nil {
			return []string{"$: the worktree could not be read to check the evidence; try again"}
		}
		return problems
	}
}

// journal writes the normalized events of a session as they arrive. It keeps
// reading after a failure: the session must not block on its channel.
func (s *Supervisor) journal(ctx context.Context, node, session string, events <-chan ports.Event) error {
	var failed error
	for ev := range events {
		if failed == nil {
			failed = s.append(ctx, store.Event{Kind: ev.Kind, NodeID: node, SessionID: session, Payload: ev.Payload})
		}
	}
	return failed
}

// append journals one event that leads to no decision.
func (s *Supervisor) append(ctx context.Context, e store.Event) error {
	h, err := s.header()
	if err != nil {
		return err
	}
	e.Header = h
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		_, err := t.Append(e)
		return err
	})
}

func verify(cmds []prompt.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Command
	}
	return out
}

// promptInput gathers what the prompt of a role session needs: the issue, the
// project rules and the gate commands read from the milestone tip, the
// criteria, and the cause of the round.
func (s *Supervisor) promptInput(ctx context.Context, n store.NodeRow, r core.Run, st core.FlowStep, role *config.Role, schema []byte, round []gitops.DiffFile) (prompt.Input, error) {
	issue, err := s.d.Tracker.Issue(ctx, n.IssueRef)
	if err != nil {
		return prompt.Input{}, err
	}
	var rules []prompt.ProjectFile
	for _, name := range rulesFiles {
		b, err := s.d.Git.ReadAt(ctx, s.cfg.Repo, "refs/heads/"+s.cfg.Branch, name)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return prompt.Input{}, err
		}
		rules = append(rules, prompt.ProjectFile{Name: name, Content: b})
	}
	tip, err := s.d.Git.ReadAt(ctx, s.cfg.Repo, "refs/heads/"+s.cfg.Branch, ".kluzo/kluzo.yaml")
	if err != nil {
		return prompt.Input{}, err
	}
	tipCfg, err := config.ParseConfig("kluzo.yaml at the milestone tip", tip)
	if err != nil {
		return prompt.Input{}, fmt.Errorf("%w: %w", err, ports.ErrPermanent)
	}
	var cmds []prompt.Command
	for _, g := range tipCfg.Gates {
		if g.Run != "" {
			cmds = append(cmds, prompt.Command{Name: g.Name, Command: g.Run})
		}
	}
	review := s.flow.Steps[reviewStep(s.flow)]
	t := prompt.Task{
		Step: st.ID, Attempt: n.Attempt, MaxAttempts: s.limits(n).MaxAttempts, Round: n.Round, MaxRounds: review.MaxRounds,
		Issue: n.IssueRef, Branch: s.branch(n), IssueTitle: issue.Title, IssueBody: issue.Body,
		Criteria: s.cfg.Criteria[n.IssueRef], Verify: cmds,
	}
	// Back for review findings, red gates or both: the findings stay in the
	// task until the reviewer judges them, because the coverage asks for an
	// answer to each.
	if st.Role == core.RoleImplementer && (r.Rejected || len(r.GateFailures) > 0) {
		t.Why = &prompt.Why{}
		if r.Rejected {
			t.Why.Review = &prompt.ReviewCause{Findings: r.Findings.Open()}
		}
		if len(r.GateFailures) > 0 {
			t.Why.Gate = gateFailures(r.GateFailures, cmds)
		}
	}
	if st.Role == core.RoleImplementer && r.Fresh && n.Attempt > 1 {
		t.Previous = &prompt.PreviousAttempt{Attempt: n.Attempt - 1, Facts: previousFacts(r)}
	}
	if st.Role == core.RoleReviewer {
		t.ImplementerReport = string(r.Report)
		if n.Round > 0 {
			t.PreviousFindings = r.Findings.Open()
			t.Diff = &prompt.Diff{}
			for _, f := range round {
				t.Diff.Files = append(t.Diff.Files, prompt.DiffFile{Path: f.Path, Added: f.Added, Removed: f.Removed})
			}
		}
	}
	return prompt.Input{Role: role, Language: s.cfg.Project.Config.Project.Language, Milestone: s.cfg.Milestone, Task: t, Rules: rules, Schema: schema}, nil
}

func reviewStep(f core.Flow) int {
	for i, st := range f.Steps {
		if st.Kind == core.StepRole && st.Role == core.RoleReviewer {
			return i
		}
	}
	return 0 // Validate guarantees a reviewer
}

func gateFailures(results []ports.GateResult, cmds []prompt.Command) []prompt.GateFailure {
	out := make([]prompt.GateFailure, len(results))
	for i, g := range results {
		out[i] = prompt.GateFailure{Name: g.Gate, Log: g.Summary, LogPath: g.LogRef}
		for _, c := range cmds {
			if c.Name == g.Gate {
				out[i].Command = c.Command
			}
		}
	}
	return out
}

// previousFacts are the facts of the previous attempt a new one gets.
func previousFacts(r core.Run) []string {
	var facts []string
	for _, g := range r.GateFailures {
		facts = append(facts, fmt.Sprintf("gate %s %s", g.Gate, g.State))
	}
	if open := r.Findings.Open(); len(open) > 0 {
		facts = append(facts, fmt.Sprintf("%d review findings were still open", len(open)))
	}
	return facts
}
