package supervisor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/platform/redact"
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

// projectEnv is what the sessions, the gates and the setup of a node get
// besides the fixed part: the cache variables, with the build caches under
// builds, and COMPOSE_PROJECT_NAME (ADR 0025).
func (s *Supervisor) projectEnv(n store.NodeRow, builds string) (map[string]string, error) {
	caches, err := sessionenv.Caches(s.cfg.Project.Config.Caches, s.cfg.Downloads, builds)
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
	return base, s.setup(ctx, n, wt, sessionsPlace)
}

// place is where a checkout of the node runs: its HOME and build caches.
type place struct{ home, builds string }

// The sessions and the gates of a node use apart HOMEs and build caches: a
// session cannot leave in them what a gate will run (ADR 0020).
var (
	sessionsPlace = place{home: "home", builds: "cache"}
	gatesPlace    = place{home: "gates-home", builds: "gates-cache"}
)

// env is the environment of a checkout of the node at p: env_allow from
// Kluzo, the caches and COMPOSE_PROJECT_NAME.
func (s *Supervisor) env(n store.NodeRow, p place, allow []string) ([]string, error) {
	extra, err := s.projectEnv(n, filepath.Join(s.cfg.Dir, p.builds))
	if err != nil {
		return nil, err
	}
	return sessionenv.Build(sessionenv.Spec{Parent: s.cfg.ParentEnv, Home: filepath.Join(s.cfg.Dir, p.home), Allow: allow, Extra: extra}), nil
}

// setup runs worktree.include and worktree.setup in a checkout of the node.
func (s *Supervisor) setup(ctx context.Context, n store.NodeRow, dir string, p place) error {
	env, err := s.env(n, p, s.cfg.Project.Config.Worktree.EnvAllow)
	if err != nil {
		return err
	}
	return s.d.Git.Prepare(ctx, gitops.PrepareSpec{Repo: s.cfg.Repo, Worktree: dir, Base: s.cfg.Branch, Env: env, DefaultTimeout: 15 * time.Minute})
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
	found, err := s.hostCredentials(ctx)
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return s.decideSession(ctx, f, n, done, ports.Result{State: ports.StateFailed, Cause: ports.CauseHostCredentials,
			Detail: "credentials of the host are visible to the sessions (" + strings.Join(found, "; ") + "); remove them, or set security.allow_host_credentials with an isolation of your own"})
	}
	left := s.budget(n) - n.CostUSDMicros
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
		if err := s.afterSession(ctx, n, st, cmp.Or(done.Base, r.Base), round, &done); err != nil {
			res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "after the session: " + err.Error(), CostUSD: res.CostUSD, Cumulative: res.Cumulative}
		}
	}
	if res.State == ports.StateFailed && res.Cause.Retryable() && !done.Tampered && runNo < sessionRuns {
		return s.decide(ctx, n, core.SessionRetry{Step: r.Step}, retried(f, runNo, string(res.Cause)+": "+res.Detail))
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
	done.State, done.Cause = res.State, res.Cause
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
	return config.USDMicros(s.cfg.Project.Config.Budgets.Node.USD)
}

// afterSession records what an accepted session produced: the implementer's
// work as a commit, checked against the guarantees of ADR 0020 from the base
// of the attempt, and for a review from round 2 the lines the round changed.
func (s *Supervisor) afterSession(ctx context.Context, n store.NodeRow, st core.FlowStep, base string, round []gitops.DiffFile, done *core.SessionDone) error {
	if st.Role == core.RoleImplementer {
		head, err := s.d.Git.Commit(ctx, s.cfg.Repo, s.worktree(n), fmt.Sprintf("kluzo: #%s attempt %d round %d", n.IssueRef, n.Attempt, n.Round))
		if err != nil {
			return err
		}
		done.Head = head
		done.Proposal, done.Auto, err = s.checkWork(ctx, n.ID, base, head)
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
// when no harness session ran, or a *stopped when the workspace left running
// while it ran and the session did not tamper with the repository.
func (s *Supervisor) play(ctx context.Context, f store.Effect, n store.NodeRow, r core.Run, st core.FlowStep, runNo int, left int64, round []gitops.DiffFile, done *core.SessionDone) (ports.Result, *core.HarnessSession, error) {
	role := s.cfg.Project.Roles[st.Role]
	if role == nil {
		return ports.Result{}, nil, fmt.Errorf("role %s is not in the project: %w", st.Role, ports.ErrPermanent)
	}
	schema, ok := contracts.OutputSchema(role.Output)
	if !ok {
		return ports.Result{}, nil, fmt.Errorf("output schema %s of role %s: %w", role.Output, role.Name, ports.ErrPermanent)
	}
	in, err := s.promptInput(ctx, n, r, cmp.Or(done.Base, r.Base), st, role, schema, round)
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
	prev, reduced := r.Implementer, prompt.Prompt{}
	resume := st.Role == core.RoleImplementer && prev != nil && prev.Model == model && prev.Harness == s.d.Runtime.Name() && prev.Segments == segments
	if resume {
		in.Task.Reduced = true
		reduced, err = prompt.Build(in, func() (string, error) { return prev.Nonce, nil })
		switch {
		case errors.Is(err, prompt.ErrNonceCollision):
			resume = false
		case err != nil:
			return ports.Result{}, nil, err
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
	env, err := s.projectEnv(n, filepath.Join(s.cfg.Dir, sessionsPlace.builds))
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
		System: pr.System(), Segments: segments, Workdir: wt, Verify: verify(in.Task.Verify),
		MCPEndpoint: ms.URL(), MCPToken: ms.Token(), MCPTools: []string{mcp.ToolResult}, Accepted: ms.Accepted,
		HookEndpoint: ms.HookURL(), Hooked: ms.Hooked, Env: env, MaxBudgetUSDMicros: left,
		Timeout: time.Duration(role.Budget.Minutes) * time.Minute,
	}
	// sent is the prompt the harness gets: the reduced task, under the nonce
	// of the conversation, when it resumes.
	var sess ports.Session
	sent := pr
	if resume {
		spec.Prompt, spec.PriorUsage, sent = reduced.Task, prev.Cumulative, reduced
		sess, err = s.d.Runtime.Resume(ctx, prev.Ref, spec)
		if errors.Is(err, ports.ErrResumeMismatch) {
			resume = false
		}
	}
	if !resume {
		spec.Prompt, spec.PriorUsage, sent = pr.Task, ports.Usage{}, pr
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
	if err := s.writePrompt(sessionID, sent); err != nil {
		s.d.Log.Warn("prompt log failed", "session", sessionID, "err", err)
	}
	stop := s.watchStop(ctx, sess)
	if err := s.journal(ctx, n, sessionID, started{ref: sess.HarnessRef(), prompt: sent}, sess.Events()); err != nil {
		s.d.Log.Warn("journal of session events failed", "session", sessionID, "err", err)
	}
	res, err := sess.Wait()
	by := stop()
	if err != nil {
		res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "wait: " + err.Error()}
	}
	res = s.checked(ctx, snap, branch, res, done)
	if by != "" && !done.Tampered {
		return ports.Result{}, nil, &stopped{state: by} // tampering still fails the node
	}
	return res, &core.HarnessSession{Ref: sess.HarnessRef(), Harness: s.d.Runtime.Name(), Model: model, Segments: segments, Nonce: sent.Nonce, Cumulative: res.Cumulative}, nil
}

// stopped means the workspace left running while the session ran (a pause
// or a cancellation, maybe from another process): the session was cancelled
// and no decision is taken on it.
type stopped struct{ state core.WorkspaceState }

func (e *stopped) Error() string {
	return "the workspace is " + string(e.state) + ": the session was cancelled"
}

// watchStop cancels the session when the workspace leaves running: paused or
// cancelled, by this process or another one (kluzo pause, the web). While the
// loop waits for the session, the state is read again every Idle
// (state-machine.md). The returned function ends the watch and returns the
// state that stopped the session ("" when none did).
func (s *Supervisor) watchStop(ctx context.Context, sess ports.Session) func() core.WorkspaceState {
	stop, over := make(chan struct{}), make(chan struct{})
	var by core.WorkspaceState
	go func() {
		defer close(over)
		tick := time.NewTicker(s.cfg.Idle)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			snap, err := s.d.DB.Snapshot(ctx)
			if err != nil || snap.Workspace == nil || snap.Workspace.State == core.WorkspaceRunning {
				continue
			}
			by = snap.Workspace.State
			s.d.Log.Info("the workspace left running: the session is cancelled", "workspace", s.cfg.Workspace, "state", by, "ref", sess.HarnessRef())
			if err := sess.Cancel(); err != nil {
				s.d.Log.Warn("cancel the session", "ref", sess.HarnessRef(), "err", err)
			}
			return
		}
	}()
	return func() core.WorkspaceState {
		close(stop)
		<-over // by is written before over closes
		return by
	}
}

// stoppedSession settles a session effect whose session the workspace
// stopped, in one transaction: done, and for a pause a new session effect of
// the same step, which runs when the workspace is resumed. The node keeps its
// state and its run, the last stable ones, and the new effect starts its own
// count of runs: a pause does not use up the retries of the step.
func (s *Supervisor) stoppedSession(ctx context.Context, f store.Effect, by core.WorkspaceState) error {
	now := s.d.Clock.Now()
	var again []store.EffectSpec
	if by == core.WorkspacePaused {
		id, err := s.d.IDs.New(ids.Effect, now)
		if err != nil {
			return err
		}
		again = append(again, store.EffectSpec{ID: id, Kind: f.Kind, Key: "again", Payload: f.Payload})
	}
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		if err := t.FollowEffect(f.ID, now, again...); err != nil {
			return err
		}
		return completed(f)(t, now)
	})
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

// writePrompt keeps the text of the prompt a session got, redacted, in
// logs/sessions/<id>.prompt.md (prompt.md#manifesto-da-sessão); a session that
// runs again overwrites it with its own.
func (s *Supervisor) writePrompt(session string, p prompt.Prompt) error {
	dir := filepath.Join(s.cfg.Dir, "logs", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	text := redact.New().String(p.System() + "\n\n" + p.Task)
	return os.WriteFile(filepath.Join(dir, session+".prompt.md"), []byte(text), 0o600)
}

// started is what the supervisor adds to the session.started of the adapter:
// the harness conversation and the prompt it sent, for the manifest.
type started struct {
	ref    string
	prompt prompt.Prompt
}

// journal writes the normalized events of a session as they arrive, with the
// manifest in session.started; session.finished carries the cost of the
// session to the budgets before the decision on it. Each event is journaled
// on its own: a failure loses that entry only, never the session.finished
// that follows. It reads to the end: the session must not block on its
// channel.
func (s *Supervisor) journal(ctx context.Context, n store.NodeRow, session string, st started, events <-chan ports.Event) error {
	var errs []error
	for ev := range events {
		e := store.Event{Kind: ev.Kind, NodeID: n.ID, SessionID: session, Payload: ev.Payload}
		var err error
		switch ev.Kind {
		case contracts.EventSessionStarted:
			if e.Payload, err = s.manifest(ev.Payload, st); err != nil {
				e.Payload = ev.Payload // the session row still needs it
			}
			err = errors.Join(err, s.append(ctx, e))
		case contracts.EventSessionFinished:
			err = s.finished(ctx, n, e)
		default:
			err = s.append(ctx, e)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// manifest adds the harness conversation and the session manifest
// (prompt.md#manifesto-da-sessão) to the payload of session.started.
func (s *Supervisor) manifest(payload json.RawMessage, st started) (json.RawMessage, error) {
	var p contracts.SessionStarted
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("session.started: %w", err)
	}
	m := st.prompt.Manifest(prompt.Runtime{KluzoVersion: s.cfg.Version, Harness: p.Harness, HarnessVersion: p.HarnessVersion, Model: p.Model})
	p.HarnessSessionRef = st.ref
	p.Manifest, _ = json.Marshal(m) // plain data
	out, _ := json.Marshal(p)       // plain data
	return out, nil
}

// finished journals session.finished, which adds the cost of the session to
// the node and the milestone, and the budget.alert of the milestone when the
// cost crosses its alert threshold (budgets.milestone.alert_at), with a
// notification.
func (s *Supervisor) finished(ctx context.Context, n store.NodeRow, e store.Event) error {
	h, err := s.header()
	if err != nil {
		return err
	}
	e.Header = h
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		before, own, err := t.WorkspaceCost()
		if err != nil {
			return err
		}
		if _, err := t.Append(e); err != nil {
			return err
		}
		after, _, err := t.WorkspaceCost()
		if err != nil {
			return err
		}
		limit, threshold := s.milestoneBudget(own)
		if threshold <= 0 || before >= threshold || after < threshold {
			return nil
		}
		ah, err := s.header()
		if err != nil {
			return err
		}
		id, err := s.d.IDs.New(ids.Effect, ah.TS)
		if err != nil {
			return err
		}
		alert, _ := json.Marshal(contracts.BudgetAlert{Scope: contracts.BudgetScopeMilestone, SpentUSDMicros: after, LimitUSDMicros: limit}) // plain data
		notify, _ := json.Marshal(effectPayload{Node: n.ID, Issue: n.IssueRef, Do: core.Effect{                                              // plain data
			Kind: core.EffectNotify, Key: "notify", Title: "Milestone budget alert", Priority: ports.PriorityHigh,
			Body: fmt.Sprintf("the milestone spent US$ %.2f of US$ %.2f", config.USD(after), config.USD(limit)),
		}})
		_, err = t.Append(store.Event{Header: ah, Kind: contracts.EventBudgetAlert, Payload: alert},
			store.EffectSpec{ID: id, Kind: string(core.EffectNotify), Key: "notify", Payload: notify})
		return err
	})
}

// milestoneBudget is the budget of the milestone (the workspace's own, or
// budgets.milestone.usd) and the cost that raises the alert, in
// micro-dollars; zero when the milestone has no budget.
func (s *Supervisor) milestoneBudget(own int64) (limit, threshold int64) {
	b := s.cfg.Project.Config.Budgets.Milestone
	limit = own
	if limit == 0 {
		limit = config.USDMicros(b.USD)
	}
	at := b.AlertAt
	if at == 0 {
		at = 1
	}
	return limit, int64(float64(limit) * at)
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
// project rules read from the milestone tip, the gate commands frozen at the
// base of the attempt, the criteria, and the cause of the round.
func (s *Supervisor) promptInput(ctx context.Context, n store.NodeRow, r core.Run, base string, st core.FlowStep, role *config.Role, schema []byte, round []gitops.DiffFile) (prompt.Input, error) {
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
	frozen, err := s.frozen(ctx, base)
	if err != nil {
		return prompt.Input{}, err
	}
	var cmds []prompt.Command
	for _, g := range frozen.Gates {
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
	// Back for review findings, red gates, a failed integration: the findings stay in the
	// task until the reviewer judges them, because the coverage asks for an
	// answer to each.
	if st.Role == core.RoleImplementer && (r.Rejected || len(r.GateFailures) > 0 || r.Integration != nil) {
		t.Why = &prompt.Why{}
		if r.Rejected {
			t.Why.Review = &prompt.ReviewCause{Findings: r.Findings.Open()}
		}
		if len(r.GateFailures) > 0 {
			t.Why.Gate = gateFailures(r.GateFailures, cmds)
		}
		if c := r.Integration; c != nil {
			t.Why.Integration = &prompt.IntegrationCause{ConflictFiles: c.ConflictFiles, Markers: c.Markers, Freshness: c.Freshness}
		}
	}
	if st.Role == core.RoleImplementer && r.Fresh && n.Attempt > 1 {
		evs, err := s.d.DB.NodeEvents(ctx, n.ID)
		if err != nil {
			return prompt.Input{}, err
		}
		facts, err := previousFacts(evs, n.Attempt-1, s.worktree(n))
		if err != nil {
			return prompt.Input{}, err
		}
		t.Previous = &prompt.PreviousAttempt{Attempt: n.Attempt - 1, Facts: facts}
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
		out[i] = prompt.GateFailure{Name: g.Gate, ExitCode: g.ExitCode, Failures: g.Failures, Log: g.Summary, LogPath: g.LogRef, Report: g.Report}
		for _, c := range cmds {
			if c.Name == g.Gate {
				out[i].Command = c.Command
			}
		}
	}
	return out
}

// Limits of the facts of a previous attempt (prompt.md#limites).
const (
	factFiles    = 50
	factFindings = 20
)

// previousFacts are the facts of an attempt a new one gets, from the journal
// of the node (prompt.md, Previous attempt): the files its sessions changed,
// the findings still open and the gates whose last run failed. An event
// belongs to the attempt of the last node.transition before it. Edited paths
// are made relative to the worktree wt; edits outside it are left out.
func previousFacts(evs []store.Event, attempt int, wt string) ([]string, error) {
	cur := 0
	files := map[string]bool{}
	gates := map[string]string{} // gate → state of its last run
	var gateOrder []string
	var last *contracts.NodeFlow
	for _, e := range evs {
		var err error
		switch e.Kind {
		case contracts.EventNodeTransition:
			var p contracts.NodeTransition
			err = json.Unmarshal(e.Payload, &p)
			cur = p.Attempt
		case contracts.EventFileEdited:
			var p contracts.FileEdited
			if err = json.Unmarshal(e.Payload, &p); err == nil && cur == attempt {
				if rel, ok := inside(wt, p.Path); ok {
					files[rel] = true
				}
			}
		case contracts.EventGateFinished:
			var p contracts.GateRun
			if err = json.Unmarshal(e.Payload, &p); err == nil && cur == attempt {
				if _, ok := gates[p.Gate]; !ok {
					gateOrder = append(gateOrder, p.Gate)
				}
				gates[p.Gate] = p.State
			}
		case contracts.EventNodeFlow:
			if cur == attempt {
				last = &contracts.NodeFlow{}
				err = json.Unmarshal(e.Payload, last)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("journal entry %d (%s): %w", e.Seq, e.Kind, err)
		}
	}
	var facts []string
	changed := slices.Sorted(maps.Keys(files))
	if len(changed) > 0 {
		fact := "changed files: " + strings.Join(changed[:min(len(changed), factFiles)], ", ")
		if extra := len(changed) - factFiles; extra > 0 {
			fact += fmt.Sprintf(" (+%d files)", extra)
		}
		facts = append(facts, fact)
	}
	for _, g := range gateOrder {
		if st := gates[g]; st != ports.GatePassed {
			facts = append(facts, fmt.Sprintf("gate %s %s", g, st))
		}
	}
	if last != nil {
		var r core.Run
		if err := json.Unmarshal(last.Run, &r); err != nil {
			return nil, fmt.Errorf("node.flow of attempt %d: %w", attempt, err)
		}
		open := r.Findings.Open()
		for _, f := range open[:min(len(open), factFindings)] {
			facts = append(facts, fmt.Sprintf("finding %s still open (%s, %s:%d): %s", f.ID, f.Severity, f.File, f.Line, f.Message))
		}
		if extra := len(open) - factFindings; extra > 0 {
			facts = append(facts, fmt.Sprintf("+%d open findings", extra))
		}
	}
	return facts, nil
}

// inside returns name relative to the worktree wt, slash-separated, and
// whether it is inside it; a relative name is already relative to wt.
func inside(wt, name string) (string, bool) {
	if !filepath.IsAbs(name) {
		name = filepath.Join(wt, name)
	}
	rel, err := filepath.Rel(wt, filepath.Clean(name))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
