package supervisor_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

func TestApprovedInTheFirstRound(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	n, r := e.nodeRow()
	if n.State != core.NodeIntegrated || !r.Ready || r.Head == "" || r.Base == "" || r.Head == r.Base {
		t.Fatalf("node %s, run %+v", n.State, r)
	}
	if want := []string{"ready", "running", "in_review", "gating", "queued", "integrating", "integrated"}; !slices.Equal(e.moves(), want) {
		t.Errorf("moves = %v, want %v", e.moves(), want)
	}
	comments := e.comments()
	if len(comments) != 7 || !strings.Contains(comments[2], "`running` → `in_review` · implementer delivered (attempt 1, round 0)") {
		t.Errorf("comments = %q", comments)
	}
	issue, _ := e.tracker.Issue(t.Context(), e.issue)
	if !slices.Contains(issue.Labels, "orch/integrated") {
		t.Errorf("labels = %v", issue.Labels)
	}
	if runs := e.gates.Runs(); len(runs) != 2 || runs[0].Gate != "build" || !runs[0].Fast || runs[1].Gate != "test" || runs[1].SHA != r.Head {
		t.Errorf("gate runs = %+v", runs)
	}
	calls := e.runtime.Calls()
	if len(calls) != 2 || calls[0].resume || calls[1].role != core.RoleReviewer || calls[0].budget != 6_000_000 || calls[1].budget != 5_990_000 {
		t.Errorf("calls = %+v", calls)
	}
	if !strings.Contains(calls[0].prompt, "Agenda API") || !strings.Contains(calls[1].prompt, "Implementer report") {
		t.Errorf("prompts = %+v", calls)
	}
	if len(e.events(contracts.EventMCPCall)) != 2 || len(e.events(contracts.EventSessionFinished)) != 2 || len(e.events(contracts.EventGateFinished)) != 2 {
		t.Errorf("journal: %d mcp calls, %d sessions, %d gates", len(e.events(contracts.EventMCPCall)), len(e.events(contracts.EventSessionFinished)), len(e.events(contracts.EventGateFinished)))
	}
}

func TestReturnedWithFindings(t *testing.T) {
	e := newEnv(t,
		impl(implemented()), rev(reviewed(true, nil, bug)),
		impl(implemented(response("F1", "fixed"))), rev(reviewed(true, []map[string]any{previous("F1", "resolved")})))
	e.drain()
	n, r := e.nodeRow()
	if n.State != core.NodeIntegrated || n.Round != 1 || r.Findings.List[0].Status != core.FindingResolved {
		t.Fatalf("node %s round %d, findings %+v", n.State, n.Round, r.Findings)
	}
	calls := e.runtime.Calls()
	if len(calls) != 4 || !calls[2].resume || calls[2].counter != 0 {
		t.Fatalf("calls = %+v", calls)
	}
	// The resumed implementer gets the reduced task: why it is back, and Deliver.
	if p := calls[2].prompt; !strings.Contains(p, "Why you are here again") || !strings.Contains(p, "F1") || strings.Contains(p, "Create the agenda endpoints") {
		t.Errorf("resumed prompt = %q", p)
	}
	// The second review gets the open findings and the diff of the round.
	if p := calls[3].prompt; !strings.Contains(p, "F1") || !strings.Contains(p, "Round diff") {
		t.Errorf("round 2 review prompt = %q", p)
	}
}

func TestContestedFinding(t *testing.T) {
	for _, tt := range []struct {
		status string
		want   core.NodeState
	}{{"resolved", core.NodeIntegrated}, {"open", core.NodeBlocked}} {
		t.Run(tt.status, func(t *testing.T) {
			e := newEnv(t,
				impl(implemented()), rev(reviewed(true, nil, bug)),
				impl(implemented(response("F1", "contested"))), rev(reviewed(true, []map[string]any{previous("F1", tt.status)})))
			e.drain()
			n, r := e.nodeRow()
			f := r.Findings.List[0]
			if n.State != tt.want || !f.Contested || f.ContestAccepted != (tt.status == "resolved") {
				t.Errorf("node %s, finding %+v", n.State, f)
			}
			if tt.want == core.NodeBlocked && (n.BlockedReason != core.BlockedTriage || len(e.notifier.Sent()) != 1) {
				t.Errorf("refused contest at the last round: %s, notifications %v", n.BlockedReason, e.notifier.Sent())
			}
		})
	}
}

func TestRoundsOver(t *testing.T) {
	e := newEnv(t,
		impl(implemented()), rev(reviewed(true, nil, bug)),
		impl(implemented(response("F1", "fixed"))), rev(reviewed(false, []map[string]any{previous("F1", "open")})))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeBlocked || n.BlockedReason != core.BlockedTriage {
		t.Fatalf("node = %+v", n)
	}
	if c := e.comments(); !strings.Contains(c[len(c)-1], "blocked: triage") {
		t.Errorf("last comment = %q", c[len(c)-1])
	}
}

func TestFastGateRed(t *testing.T) {
	e := newEnv(t, impl(implemented()), impl(implemented()), rev(reviewed(true, nil)))
	e.gates.Queue("build", ports.GateResult{State: ports.GateFailed, Summary: "undefined: Agenda", LogRef: "logs/gates/1.log"})
	e.drain()
	n, r := e.nodeRow()
	if n.State != core.NodeIntegrated || n.Attempt != 1 || n.Round != 0 || r.FastGateFailures != 1 {
		t.Fatalf("node %+v, run %+v", n, r)
	}
	calls := e.runtime.Calls()
	if len(calls) != 3 || !calls[1].resume || !strings.Contains(calls[1].prompt, "undefined: Agenda") {
		t.Errorf("calls = %+v", calls)
	}
	if want := []string{"ready", "running", "in_review", "gating", "queued", "integrating", "integrated"}; !slices.Equal(e.moves(), want) {
		t.Errorf("a red fast gate is not a transition: moves = %v", e.moves())
	}
}

func TestFullGateRedStartsANewAttempt(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)), impl(implemented()), rev(reviewed(true, nil)))
	e.gates.Queue("test", ports.GateResult{State: ports.GateFailed, Summary: "FAIL TestAgenda"})
	e.drain()
	n, _ := e.nodeRow()
	calls := e.runtime.Calls()
	if n.State != core.NodeIntegrated || n.Attempt != 2 || len(calls) != 4 || calls[2].resume || !strings.Contains(calls[2].prompt, "Previous attempt") {
		t.Fatalf("node %+v, calls %+v", n, calls)
	}
	// The facts come from the journal of attempt 1.
	for _, fact := range []string{"changed files: agenda.go", "gate test failed"} {
		if !strings.Contains(calls[2].prompt, fact) {
			t.Errorf("the prompt of attempt 2 misses %q:\n%s", fact, calls[2].prompt)
		}
	}
}

func TestSessionCauses(t *testing.T) {
	for _, tt := range []struct {
		cause     ports.Cause
		runs      int // sessions started
		reason    core.BlockedReason
		workspace core.WorkspaceState
	}{
		{ports.CauseAuth, 1, core.BlockedPaused, core.WorkspacePaused},
		{ports.CauseRateLimit, 3, core.BlockedTriage, core.WorkspaceRunning},
		{ports.CauseRuntime, 3, core.BlockedTriage, core.WorkspaceRunning},
		{ports.CauseBudget, 1, core.BlockedBudget, core.WorkspaceRunning},
		{ports.CauseGuard, 1, core.BlockedTriage, core.WorkspaceRunning},
		{ports.CauseMaxTurns, 1, core.BlockedTriage, core.WorkspaceRunning},
		{ports.CauseRefusal, 1, core.BlockedTriage, core.WorkspaceRunning},
		{ports.CauseNoResult, 1, core.BlockedTriage, core.WorkspaceRunning},
	} {
		t.Run(string(tt.cause), func(t *testing.T) {
			steps := make([]step, tt.runs)
			for i := range steps {
				steps[i] = step{role: core.RoleImplementer, cause: tt.cause}
			}
			e := newEnv(t, steps...)
			e.drain()
			n, _ := e.nodeRow()
			snap, _ := e.db.Snapshot(t.Context())
			if n.State != core.NodeBlocked || n.BlockedReason != tt.reason || snap.Workspace.State != tt.workspace {
				t.Errorf("node %s/%s, workspace %s", n.State, n.BlockedReason, snap.Workspace.State)
			}
			calls := e.runtime.Calls()
			if len(calls) != tt.runs || calls[len(calls)-1].counter != tt.runs-1 || n.CostUSDMicros != int64(tt.runs)*10_000 {
				t.Errorf("calls %+v, cost %d", calls, n.CostUSDMicros)
			}
			if len(e.notifier.Sent()) != 1 {
				t.Errorf("notifications = %+v", e.notifier.Sent())
			}
		})
	}
}

func TestSingleCorrection(t *testing.T) {
	invalid := map[string]any{"v": 1, "status": "done", "summary": "s", "files_changed": []string{}, "tests": map[string]any{"added": 0, "command": "x"},
		"criteria": []map[string]any{{"id": "AC1", "evidence": []string{"missing.go:3"}}}}
	broken := map[string]any{"v": 1, "status": "done"}

	e := newEnv(t, impl(broken, implemented()), rev(reviewed(true, nil)))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
		t.Errorf("a schema error corrected once: %s", n.State)
	}
	// A schema error and then a coverage error use the one correction: the
	// session ends without a result.
	e = newEnv(t, impl(broken, invalid))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeBlocked || n.BlockedReason != core.BlockedTriage {
		t.Errorf("two invalid results: %+v", n)
	}
}

func TestTamperingFailsTheNode(t *testing.T) {
	st := impl(implemented())
	st.tamper = true
	e := newEnv(t, st)
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeFailed {
		t.Fatalf("node = %+v", n)
	}
	if out, err := gitRun(e.repo, "branch", "--list", "stolen"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("the branch the session created is still there: %q %v", out, err)
	}
	// A failed node keeps its worktree for diagnosis (ADR 0025).
	if _, err := os.Stat(filepath.Join(e.dir, "ws", "worktrees", e.issue)); err != nil || len(e.docker.Calls()) != 0 {
		t.Errorf("worktree of the failed node: %v; docker calls %v", err, e.docker.Calls())
	}
}

// A node that spent its budget is blocked before the next session starts.
func TestBudgetSpent(t *testing.T) {
	e := newEnvBudget(t, 10_000, impl(implemented()))
	e.drain()
	n, _ := e.nodeRow()
	if n.State != core.NodeBlocked || n.BlockedReason != core.BlockedBudget || n.CostUSDMicros != 10_000 || len(e.runtime.Calls()) != 1 {
		t.Errorf("node %s/%s, cost %d, calls %d", n.State, n.BlockedReason, n.CostUSDMicros, len(e.runtime.Calls()))
	}
}

// failingTracker refuses state labels with err.
type failingTracker struct {
	*fakes.Tracker
	err   error
	calls int
}

func (f *failingTracker) SetStateLabel(context.Context, string, string) error {
	f.calls++
	return f.err
}

func TestOutboxPolicy(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		runs int
	}{
		{"permanent error fails at once", fmt.Errorf("label: %w", ports.ErrPermanent), 1},
		{"transient error is retried 8 times", errors.New("tracker down"), 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
			ft := &failingTracker{Tracker: e.tracker, err: tt.err}
			e.sup = rebuild(t, e, ft)
			e.drain()
			failed := e.events(contracts.EventEffectFailed)
			// One label per transition fails; the node still moves on: its
			// state does not depend on the label (state-machine.md).
			if n, _ := e.nodeRow(); n.State != core.NodeIntegrated || len(failed) != 7 || ft.calls != 7*tt.runs {
				t.Fatalf("node %s, effect.failed %d, label calls %d", n.State, len(failed), ft.calls)
			}
			var p contracts.EffectFailed
			_ = json.Unmarshal(failed[0].Payload, &p)
			if p.Kind != "tracker.set_label" || p.Attempts != tt.runs || !strings.Contains(p.LastError, tt.err.Error()) {
				t.Errorf("effect.failed = %+v", p)
			}
			if n := len(e.notifier.Sent()); n != 8 {
				t.Errorf("notifications = %d, want one per failed effect and the PR", n)
			}
		})
	}
}

// rebuild swaps the tracker of an env's supervisor.
func rebuild(t *testing.T, e *env, tracker ports.Tracker) *supervisor.Supervisor {
	t.Helper()
	return build(t, filepath.Dir(e.repo), deps{db: e.db, clock: e.clock, ids: ids.NewGenerator(rand.Reader), tracker: tracker, runtime: e.runtime, gates: e.gates, notifier: e.notifier}, e.issue)
}

// Run steps until the context ends, idling when there is nothing to do.
func TestRun(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.sup.Run(ctx) }()
	deadline := time.After(30 * time.Second)
	for {
		if n, _ := e.nodeRow(); n.State == core.NodeIntegrated {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the node was not integrated")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
}

// A failure after the session ran (here the commit of its work) is a runtime
// failure of the session: its cost counts and the session policy applies.
func TestFailureAfterTheSession(t *testing.T) {
	locked := impl(implemented())
	locked.lock = true
	e := newEnv(t, locked, locked, locked)
	e.drain()
	n, _ := e.nodeRow()
	if n.State != core.NodeBlocked || n.BlockedReason != core.BlockedTriage || n.CostUSDMicros != 30_000 || len(e.runtime.Calls()) != 3 {
		t.Errorf("node %s/%s, cost %d, calls %d", n.State, n.BlockedReason, n.CostUSDMicros, len(e.runtime.Calls()))
	}
	if c := e.comments(); !strings.Contains(c[len(c)-1], "after the session") {
		t.Errorf("last comment = %q", c[len(c)-1])
	}
}

// flakyTracker fails the first state label once; the others go through.
type flakyTracker struct {
	*fakes.Tracker
	failed bool
}

func (f *flakyTracker) SetStateLabel(ctx context.Context, issue, label string) error {
	if !f.failed {
		f.failed = true
		return errors.New("tracker down")
	}
	return f.Tracker.SetStateLabel(ctx, issue, label)
}

// A label waiting for its backoff holds the newer labels of the issue, so the
// stale one never lands last.
func TestLabelsGoOutInOrder(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	e.sup = rebuild(t, e, &flakyTracker{Tracker: e.tracker})
	e.drain()
	issue, _ := e.tracker.Issue(t.Context(), e.issue)
	if !slices.Contains(issue.Labels, "orch/integrated") || slices.Contains(issue.Labels, "orch/ready") {
		t.Errorf("labels = %v, want orch/integrated", issue.Labels)
	}
	if c := e.comments(); len(c) != 7 || !strings.Contains(c[0], "dependencies integrated") || !strings.Contains(c[4], "gates passed") || !strings.Contains(c[6], "merged") {
		t.Errorf("comments out of order: %q", c)
	}
}

// The exit of a blocked node by a person, from the CLI (v0.1 item 10b): each
// decision is an input of the core, journaled with its comment on the issue.
func TestPersonDecidesOnABlockedNode(t *testing.T) {
	// The implementer session breaks a guard rule: the node is blocked for triage.
	blocked := []step{{role: core.RoleImplementer, cause: ports.CauseGuard}}
	done := []step{impl(implemented()), rev(reviewed(true, nil))}
	decide := func(t *testing.T, e *env, in core.Input) error {
		t.Helper()
		project, err := config.Load(filepath.Join(e.repo, ".kluzo"))
		if err != nil {
			t.Fatal(err)
		}
		return supervisor.DecideNode(context.Background(), e.db, ids.NewGenerator(rand.Reader), e.clock, project, e.issue, in)
	}
	t.Run("retry", func(t *testing.T) {
		e := newEnv(t, append(blocked, done...)...)
		e.drain()
		if err := decide(t, e, core.Retry{By: "kenny"}); err != nil {
			t.Fatal(err)
		}
		e.drain()
		if n, _ := e.nodeRow(); n.State != core.NodeIntegrated || n.Attempt != 2 {
			t.Errorf("node %s attempt %d; moves %v", n.State, n.Attempt, e.moves())
		}
		if !slices.ContainsFunc(e.comments(), func(c string) bool {
			return strings.Contains(c, "`blocked` → `running` · new attempt (attempt 2, round 0)")
		}) {
			t.Errorf("comments %q", e.comments())
		}
	})
	t.Run("unblock", func(t *testing.T) {
		e := newEnv(t, append(blocked, done...)...)
		e.drain()
		if err := decide(t, e, core.Unblock{By: "kenny"}); err != nil {
			t.Fatal(err)
		}
		if err := decide(t, e, core.Unblock{By: "kenny"}); !errors.Is(err, core.ErrInvalidTransition) {
			t.Errorf("unblock of a node not blocked: %v", err)
		}
		e.drain()
		if n, _ := e.nodeRow(); n.State != core.NodeIntegrated || n.Attempt != 1 {
			t.Errorf("node %s attempt %d; moves %v", n.State, n.Attempt, e.moves())
		}
	})
	t.Run("cancel", func(t *testing.T) {
		e := newEnv(t, blocked...)
		e.drain()
		// A planned node that depends on this one.
		gen := ids.NewGenerator(rand.Reader)
		other, _ := gen.New(ids.Node, epoch)
		if err := e.db.Tx(context.Background(), func(x *store.Tx) error {
			for _, ev := range []store.Event{
				{Kind: contracts.EventNodeAdded, NodeID: other, Payload: mustJSON(contracts.NodeAdded{IssueRef: "99", Title: "Agenda UI", Areas: []string{"ui"}})},
				{Kind: contracts.EventEdgeAdded, Payload: mustJSON(contracts.EdgeAdded{From: e.node, To: other, Kind: "blocks", Source: "plan"})},
			} {
				id, _ := gen.New(ids.Event, epoch)
				ev.Header = store.Header{ID: id, TS: epoch}
				if _, err := x.Append(ev); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := decide(t, e, core.Cancel{By: "kenny"}); !errors.Is(err, core.ErrInvalidTransition) || !strings.Contains(err.Error(), "#99") {
			t.Fatalf("cancel with a dependent: %v", err)
		}
		project, _ := config.Load(filepath.Join(e.repo, ".kluzo"))
		if err := supervisor.DecideNode(context.Background(), e.db, gen, e.clock, project, "99", core.Cancel{By: "kenny"}); err != nil {
			t.Fatal(err)
		}
		if err := decide(t, e, core.Cancel{By: "kenny"}); err != nil {
			t.Fatal(err)
		}
		e.drain()
		if n, _ := e.nodeRow(); n.State != core.NodeCancelled {
			t.Errorf("node %s; moves %v", n.State, e.moves())
		}
		if c := e.comments(); !strings.Contains(c[len(c)-1], "cancelled") {
			t.Errorf("last comment %q", c[len(c)-1])
		}
		if err := decide(t, e, core.Retry{}); err == nil {
			t.Error("retry of a cancelled node")
		}
	})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
