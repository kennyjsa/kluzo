// Package supervisor is the imperative shell of a workspace: it turns what
// happens (a ready node, a session that ended, gates that ran) into inputs of
// core.Apply, stores each decision in one transaction with its effects, and
// runs the effects of the outbox through the ports (ADRs 0012 and 0013).
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/docker"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/store"
)

// Deps are the ports and services the supervisor drives.
type Deps struct {
	DB       *store.DB
	Clock    clock.Clock
	IDs      *ids.Generator
	Tracker  ports.Tracker
	Runtime  ports.Runtime
	MCP      *mcp.Server
	Git      *gitops.Git
	Gates    ports.Gates
	Notifier ports.Notifier
	// SCM creates the milestone branch on the remote, opens the PR
	// milestone → main and observes its merge.
	SCM ports.SCM
	// GitToken is the token of the bot for git fetch and push; nil when the
	// remote needs none (a local path).
	GitToken func(context.Context) (string, error)
	// Docker removes the Docker resources of nodes and workspaces (ADR 0025).
	Docker *docker.Docker
	Nonce  func() (string, error) // the envelope nonce of each prompt
	// HostCredentials finds the credentials of the host visible to the
	// sessions (hostcred.Check with gitops.HostCredentialHelpers).
	HostCredentials func(context.Context) ([]string, error)
	Log             *slog.Logger
}

// missing names the required dependencies left out: all of them but
// GitToken (a remote that needs none) and Log (discarded when nil).
func (d Deps) missing() []string {
	var out []string
	for _, dep := range []struct {
		name string
		set  bool
	}{
		{"DB", d.DB != nil}, {"Clock", d.Clock != nil}, {"IDs", d.IDs != nil}, {"Tracker", d.Tracker != nil},
		{"Runtime", d.Runtime != nil}, {"MCP", d.MCP != nil}, {"Git", d.Git != nil}, {"Gates", d.Gates != nil},
		{"Notifier", d.Notifier != nil}, {"SCM", d.SCM != nil}, {"Docker", d.Docker != nil}, {"Nonce", d.Nonce != nil},
		{"HostCredentials", d.HostCredentials != nil},
	} {
		if !dep.set {
			out = append(out, dep.name)
		}
	}
	return out
}

// Config is the workspace the supervisor runs.
type Config struct {
	Project   *config.Project
	Workspace string // ws_<ulid>
	Milestone string // title of the milestone
	Repo      string // the main checkout
	Remote    string // the URL of the repository the bot pushes to
	Branch    string // the milestone branch, milestone/<name>
	Dir       string // the workspace directory: worktrees/, home/, cache/, logs/
	Downloads string // the shared download cache of the project (ADR 0025)
	// ParentEnv is the environment of Kluzo, read only for the env_allow of
	// worktree.setup.
	ParentEnv []string
	// Criteria are the acceptance criteria of each node, by issue reference.
	Criteria map[string][]core.Criterion
	// Idle is how long Run waits when there is nothing to do (default 5 s).
	Idle time.Duration
	// Version is the Kluzo version, for the session manifest.
	Version string
}

// Supervisor runs one workspace. Its methods are called from one goroutine.
type Supervisor struct {
	d    Deps
	cfg  Config
	flow core.Flow
	// polled is when the merge on main was last observed.
	polled time.Time
}

// New checks the node flow and returns the supervisor.
func New(d Deps, cfg Config) (*Supervisor, error) {
	flow, err := nodeFlow(cfg.Project)
	if err != nil {
		return nil, err
	}
	if missing := d.missing(); len(missing) > 0 || cfg.Remote == "" {
		if cfg.Remote == "" {
			missing = append(missing, "the remote")
		}
		return nil, fmt.Errorf("supervisor: missing %s", strings.Join(missing, ", "))
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Idle == 0 {
		cfg.Idle = 5 * time.Second
	}
	return &Supervisor{d: d, cfg: cfg, flow: flow}, nil
}

// nodeFlow converts flows/node.yaml to the flow of the core.
func nodeFlow(p *config.Project) (core.Flow, error) {
	f := p.Flows["node"]
	if f == nil {
		return core.Flow{}, fmt.Errorf("%w: flows/node.yaml is missing", core.ErrFlowShape)
	}
	fast := map[string]bool{}
	for _, g := range p.Config.Gates {
		fast[g.Name] = g.Fast
	}
	index := func(id string, def int) int {
		if i := slices.IndexFunc(f.Steps, func(s config.Step) bool { return s.ID == id }); i >= 0 {
			return i
		}
		return def
	}
	var out core.Flow
	for _, s := range f.Steps {
		st := core.FlowStep{ID: s.ID}
		switch {
		case s.Role != "":
			st.Kind, st.Role, st.Phase = core.StepRole, s.Role, core.NodeState(s.Phase)
			st.OnReject, st.MaxRounds = index(s.OnReject, -1), s.MaxRounds
		case len(s.Gate) > 0:
			st.Kind, st.Gates, st.OnFail, st.MaxRetries = core.StepGate, s.Gate, index(s.OnFail, 0), s.MaxRetries
			st.Fast = !slices.ContainsFunc(s.Gate, func(g string) bool { return !fast[g] })
			if st.Fast && st.MaxRetries == 0 {
				st.MaxRetries = core.DefaultFastRetries
			}
		case s.MergeQueue != "":
			st.Kind = core.StepMergeQueue
		default:
			return core.Flow{}, fmt.Errorf("%w: step %q is not a role, gate or merge_queue step", core.ErrFlowShape, s.ID)
		}
		out.Steps = append(out.Steps, st)
	}
	return out, out.Validate()
}

// Run repeats Step until ctx ends, waiting Idle when there is nothing to do.
func (s *Supervisor) Run(ctx context.Context) error {
	for {
		busy, err := s.Step(ctx)
		if ctx.Err() != nil {
			return nil // stopping: what was in flight stays pending for the restart
		}
		if err != nil {
			return err
		}
		if busy {
			continue
		}
		if done, err := s.over(ctx); err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.cfg.Idle):
		}
	}
}

// over reports whether the workspace is over (merged, cancelled, failed
// or archived) with nothing left in the outbox: Run then returns.
func (s *Supervisor) over(ctx context.Context) (bool, error) {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil || snap.Workspace == nil || snap.Workspace.State.Active() {
		return false, err
	}
	pending, err := s.d.DB.PendingEffects(ctx, farFuture, 1)
	return len(pending) == 0, err
}

// Step does one round of work: it starts an approved workspace once its
// milestone branch is ready (or pauses it when the branch failed for good),
// marks ready the planned nodes whose dependencies are integrated, gives the
// merge queue to the next queued node, closes the workspace when every node
// is integrated, dispatches a ready node when no node is active
// (max_parallel is 1 in v0.1), observes the merge on main of a workspace
// ready to merge, and runs the next pending effect. A workspace that is not
// running, or whose milestone branch is not ready, starts no session, no gate
// and no integration, but its labels, comments, notifications and cleanups
// still go out. It reports whether it did anything.
func (s *Supervisor) Step(ctx context.Context) (bool, error) {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil || snap.Workspace == nil {
		return false, err
	}
	w := snap.Workspace
	if trigger, ok := core.MilestoneTrigger(w.State, w.Milestone); ok {
		return true, s.moveWorkspace(ctx, trigger)
	}
	running := core.Dispatching(w.State, w.Milestone)
	var busy bool
	switch {
	case running:
		busy, err = s.schedule(ctx, snap)
	case w.State == core.WorkspaceReadyToMerge:
		busy, err = s.watchMerge(ctx, w)
	}
	if busy || err != nil {
		return busy, err
	}
	f, ok, err := s.next(ctx, running, w.Milestone == core.MilestoneDone)
	if err != nil || !ok {
		return false, err
	}
	return true, s.execute(ctx, f)
}

// next picks the effect to run: the oldest that is due, except that the
// labels and comments of an issue go out in order (an older one waiting for
// its backoff holds the newer ones, so a stale label never wins), a
// workspace that is not running starts no session and no gate, and the plan
// is committed only once the milestone branch is ready.
func (s *Supervisor) next(ctx context.Context, running, milestone bool) (store.Effect, bool, error) {
	now := s.d.Clock.Now()
	pending, err := s.d.DB.PendingEffects(ctx, farFuture, pendingBatch)
	if err != nil {
		return store.Effect{}, false, err
	}
	held := map[string]bool{} // issues with an earlier tracker effect pending
	for _, f := range pending {
		k := core.EffectKind(f.Kind)
		tracker := k == core.EffectLabel || k == core.EffectComment
		var p effectPayload
		_ = json.Unmarshal(f.Payload, &p) // a bad payload fails in execute
		switch {
		case tracker && held[p.Issue]:
			continue
		case f.NextAt.After(now), !running && workEffect(k), !milestone && k == core.EffectCommitPlan:
			held[p.Issue] = held[p.Issue] || tracker
			continue
		}
		return f, true, nil
	}
	return store.Effect{}, false, nil
}

// workEffect reports whether an effect is work on a node that only a running
// workspace does: sessions, gates and the integration.
func workEffect(k core.EffectKind) bool {
	switch k {
	case core.EffectSession, core.EffectGates, core.EffectRebase, core.EffectMerge:
		return true
	}
	return false
}

// farFuture lists every pending effect, due or not.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// pendingBatch is how many pending effects Step looks at to find one it may run.
const pendingBatch = 200

// schedule applies Ready to the planned nodes the graph says are ready,
// QueueTurn to the queued node whose turn it is, closes the workspace when
// every node is integrated, and applies Dispatch to the next ready node when
// no node is active.
func (s *Supervisor) schedule(ctx context.Context, snap store.Snapshot) (bool, error) {
	g := core.Graph{Nodes: map[string]core.GraphNode{}}
	byRef := map[string]store.NodeRow{}
	byID := map[string]string{}
	for _, n := range snap.Nodes {
		g.Nodes[n.IssueRef] = core.GraphNode{ID: n.IssueRef, Title: n.Title, State: n.State, Areas: n.Areas}
		byRef[n.IssueRef], byID[n.ID] = n, n.IssueRef
	}
	for _, e := range snap.Edges {
		g.Edges = append(g.Edges, core.Edge{From: byID[e.From], To: byID[e.To], Kind: contracts.EdgeKind(e.Kind)})
	}
	for _, ref := range g.Ready() {
		if n := byRef[ref]; n.State == core.NodePlanned {
			return true, s.decide(ctx, n, core.Ready{}, nil)
		}
	}
	if ref, ok := g.NextToIntegrate(); ok {
		return true, s.decide(ctx, byRef[ref], core.QueueTurn{}, nil)
	}
	states := make([]core.NodeState, len(snap.Nodes))
	for i, n := range snap.Nodes {
		states[i] = n.State
	}
	if core.AllIntegrated(states) {
		return true, s.moveWorkspace(ctx, core.TriggerAllIntegrated)
	}
	for _, n := range snap.Nodes {
		if n.State == core.NodeRunning || n.State == core.NodeInReview || n.State == core.NodeGating {
			return false, nil
		}
	}
	if next := g.NextToDispatch(1); len(next) > 0 {
		return true, s.decide(ctx, byRef[next[0]], core.Dispatch{}, nil)
	}
	return false, nil
}

// effectPayload is what an effect of the node flow carries in the outbox.
type effectPayload struct {
	Node  string      `json:"node"`
	Issue string      `json:"issue"`
	Do    core.Effect `json:"do"`
}

// header is a new journal entry header.
func (s *Supervisor) header() (store.Header, error) {
	now := s.d.Clock.Now()
	id, err := s.d.IDs.New(ids.Event, now)
	return store.Header{ID: id, TS: now}, err
}

// run decodes the run of a node.
func run(n store.NodeRow) (core.Run, error) {
	var r core.Run
	if len(n.Flow) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(n.Flow, &r); err != nil {
		return r, fmt.Errorf("node %s: bad flow: %w", n.ID, err)
	}
	return r, nil
}

// limits are the node limits for the core.
func (s *Supervisor) limits(n store.NodeRow) core.Limits {
	attempts := n.Budget.Attempts
	if attempts == 0 {
		attempts = s.cfg.Project.Config.Budgets.Node.Attempts
	}
	return core.Limits{MaxAttempts: attempts}
}

// settleFn records, in the transaction of a decision, what happens to the
// effect whose run produced the input: done, or retried later.
type settleFn func(t *store.Tx, at time.Time) error

// completed marks the effect done.
func completed(f store.Effect) settleFn {
	return func(t *store.Tx, at time.Time) error { return t.CompleteEffect(f.ID, at) }
}

// retried schedules the next run of the effect after the backoff of run n.
func retried(f store.Effect, n int, why string) settleFn {
	return func(t *store.Tx, at time.Time) error { return t.RetryEffect(f.ID, why, at.Add(backoff(n))) }
}

// decide applies an input to a node and stores the decision in one
// transaction: the journal entries of what happened (extra), the moves, the
// workspace trigger, the new run with its effects, and what happens to the
// effect whose run produced the input, if any.
func (s *Supervisor) decide(ctx context.Context, n store.NodeRow, in core.Input, settle settleFn, extra ...store.Event) error {
	r, err := run(n)
	if err != nil {
		return err
	}
	node := core.Node{State: n.State, PrevState: n.PrevState, BlockedReason: n.BlockedReason, Attempt: n.Attempt, Round: n.Round}
	d, err := core.Apply(core.NodeFlow{Node: node, Run: r}, in, s.flow, s.limits(n))
	if errors.Is(err, core.ErrStaleInput) && settle != nil {
		s.d.Log.Warn("stale effect result dropped", "node", n.ID, "err", err)
		return s.d.DB.Tx(ctx, func(t *store.Tx) error { return settle(t, s.d.Clock.Now()) })
	}
	if err != nil {
		return fmt.Errorf("node %s: %w", n.ID, err)
	}
	if p, ok, err := s.proposal(d); err != nil || ok {
		if err != nil {
			return err
		}
		extra = append(extra, p)
	}
	if f := d.Freshness; f != nil {
		body, _ := json.Marshal(contracts.MergeFreshness{ReviewedSHA: f.Reviewed, TipSHA: f.Tip, Rerun: f.Rerun}) // plain data
		extra = append(extra, store.Event{Kind: contracts.EventMergeFreshness, Payload: body})
	}
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		for _, e := range extra {
			h, err := s.header()
			if err != nil {
				return err
			}
			e.Header, e.NodeID = h, n.ID
			if _, err := t.Append(e); err != nil {
				return err
			}
		}
		for _, m := range d.Moves {
			h, err := s.header()
			if err != nil {
				return err
			}
			got, err := t.TransitionNode(n.ID, m.Event, m.Limits, h)
			if err != nil {
				return err
			}
			if got.State != m.To {
				return fmt.Errorf("node %s: the store reached %s, the core decided %s", n.ID, got.State, m.To)
			}
		}
		if d.Workspace != "" {
			h, err := s.header()
			if err != nil {
				return err
			}
			if err := s.transitionWorkspace(t, d.Workspace, h); err != nil {
				return err
			}
		}
		h, err := s.header()
		if err != nil {
			return err
		}
		specs, err := s.effectSpecs(h, n.ID, n.IssueRef, d.Effects)
		if err != nil {
			return err
		}
		runJSON, _ := json.Marshal(d.Next.Run) // plain data, cannot fail
		flow, _ := json.Marshal(contracts.NodeFlow{Step: d.Next.Run.Step, Run: runJSON})
		if _, err := t.Append(store.Event{Header: h, Kind: contracts.EventNodeFlow, NodeID: n.ID, Payload: flow}, specs...); err != nil {
			return err
		}
		if settle != nil {
			return settle(t, h.TS)
		}
		return nil
	})
}

// effectSpecs are the outbox rows of effects of a node ("" for the workspace).
func (s *Supervisor) effectSpecs(h store.Header, node, issue string, effects []core.Effect) ([]store.EffectSpec, error) {
	specs := make([]store.EffectSpec, len(effects))
	for i, e := range effects {
		id, err := s.d.IDs.New(ids.Effect, h.TS)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(effectPayload{Node: node, Issue: issue, Do: e}) // plain data, cannot fail
		specs[i] = store.EffectSpec{ID: id, Kind: string(e.Kind), Key: e.Key, Payload: body}
	}
	return specs, nil
}

// proposal is the proposal.created of a decision that blocks the node for a
// proposal: the change waits for a human (ADR 0020).
func (s *Supervisor) proposal(d core.Decision) (store.Event, bool, error) {
	if !slices.ContainsFunc(d.Moves, func(m core.Move) bool { return m.Event.Reason == core.BlockedProposal }) {
		return store.Event{}, false, nil
	}
	id, err := s.d.IDs.New(ids.Proposal, s.d.Clock.Now())
	if err != nil {
		return store.Event{}, false, err
	}
	body, _ := json.Marshal(contracts.ProposalCreated{ProposalID: id, Kind: contracts.ProposalChange, State: contracts.ProposalPending}) // plain data
	return store.Event{Kind: contracts.EventProposalCreated, Payload: body}, true, nil
}

// node reads the current row of a node.
func (s *Supervisor) node(ctx context.Context, id string) (store.NodeRow, error) {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil {
		return store.NodeRow{}, err
	}
	for _, n := range snap.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return store.NodeRow{}, fmt.Errorf("%w: node %s", store.ErrNotFound, id)
}

// effectRuns is how many runs an effect gets (docs/contracts/data-model.md).
const effectRuns = 8

// sessionRuns is how many runs a session effect gets for a retryable cause:
// the first and at most 2 new sessions in the same attempt (ADR 0024, item 7).
const sessionRuns = 3

// backoff is the wait after the n-th failed run: 1 s doubling, at most 5 min,
// without jitter.
func backoff(n int) time.Duration {
	d := time.Second << min(n-1, 9)
	return min(d, 5*time.Minute)
}

// execute runs one effect of the outbox.
func (s *Supervisor) execute(ctx context.Context, f store.Effect) error {
	n, err := s.d.DB.BeginEffect(ctx, f.ID)
	if err != nil {
		return err
	}
	var p effectPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		return s.settle(ctx, f, n, fmt.Errorf("bad payload: %w: %w", err, ports.ErrPermanent))
	}
	log := s.d.Log.With("workspace", s.cfg.Workspace, "node", p.Node, "effect", f.ID, "kind", f.Kind, "run", n)
	switch core.EffectKind(f.Kind) {
	case core.EffectLabel:
		err = s.d.Tracker.SetStateLabel(ctx, p.Issue, p.Do.Label)
	case core.EffectComment:
		_, err = s.d.Tracker.Comment(ctx, p.Issue, p.Do.Body, f.IdemKey)
	case core.EffectNotify:
		title := p.Do.Title
		if p.Issue != "" {
			title += " · #" + p.Issue
		}
		err = s.d.Notifier.Notify(ctx, ports.Notification{Title: title, Body: p.Do.Body, Priority: p.Do.Priority, Group: s.cfg.Workspace})
	case core.EffectSession:
		err = s.session(ctx, f, n, p)
		if err == nil {
			return nil // the session stored its own outcome
		}
		if st := new(stopped); errors.As(err, &st) {
			log.Info("session cancelled", "workspace_state", st.state)
			return s.stoppedSession(ctx, f, st.state)
		}
	case core.EffectGates:
		err = s.gates(ctx, f, p)
		if err == nil {
			return nil
		}
	case core.EffectRebase:
		err = s.rebase(ctx, f, p)
		if err == nil {
			return nil
		}
	case core.EffectMerge:
		err = s.merge(ctx, f, p)
		if err == nil {
			return nil
		}
	case core.EffectOpenPR:
		err = s.openPR(ctx, f)
		if err == nil {
			return nil
		}
	case core.EffectCleanupNode:
		err = s.cleanupNode(ctx, p)
	case core.EffectEnsureMilestone:
		err = s.ensureMilestone(ctx)
	case core.EffectCleanupWorkspace:
		err = s.cleanupWorkspace(ctx, f)
		if err == nil {
			return nil // completed with the notification it queued
		}
	case core.EffectCommitPlan:
		err = s.commitPlan(ctx, p)
	default:
		err = fmt.Errorf("unknown effect kind %q: %w", f.Kind, ports.ErrPermanent)
	}
	if err != nil {
		log.Warn("effect failed", "err", err)
	}
	return s.settle(ctx, f, n, err)
}

// settle records how the n-th run of an effect ended: done, retried after the
// backoff, or failed (a permanent error, or the last run) with effect.failed
// in the journal and a notification.
func (s *Supervisor) settle(ctx context.Context, f store.Effect, n int, err error) error {
	now := s.d.Clock.Now()
	switch {
	case err == nil:
		return s.d.DB.Tx(ctx, func(t *store.Tx) error { return completed(f)(t, now) })
	case ctx.Err() != nil:
		return ctx.Err() // stopping: the effect stays pending
	case !ports.IsPermanent(err) && n < effectRuns:
		return s.d.DB.Tx(ctx, func(t *store.Tx) error { return retried(f, n, err.Error())(t, now) })
	}
	var p effectPayload
	_ = json.Unmarshal(f.Payload, &p) // best effort: the node of the event
	h, herr := s.header()
	if herr != nil {
		return herr
	}
	body, _ := json.Marshal(contracts.EffectFailed{EffectID: f.ID, Kind: f.Kind, Attempts: n, LastError: err.Error()})
	if err := s.d.DB.Tx(ctx, func(t *store.Tx) error {
		if _, err := t.Append(store.Event{Header: h, Kind: contracts.EventEffectFailed, NodeID: p.Node, Payload: body}); err != nil {
			return err
		}
		return t.FailEffect(f.ID, err.Error())
	}); err != nil {
		return err
	}
	if nerr := s.d.Notifier.Notify(ctx, ports.Notification{
		Title: "Kluzo effect failed: " + f.Kind, Body: err.Error() + " (after " + strconv.Itoa(n) + " runs)",
		Priority: ports.PriorityHigh, Group: s.cfg.Workspace,
	}); nerr != nil {
		s.d.Log.Warn("notify failed", "err", nerr)
	}
	return nil
}
