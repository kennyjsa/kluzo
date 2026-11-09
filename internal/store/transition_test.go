package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

// nodeEvents is every node trigger, each with the reason it needs.
// sameKind reports whether err carries the same domain error as want.
func sameKind(err, want error) bool {
	for _, sentinel := range []error{core.ErrInvalidTransition, core.ErrLimitReached} {
		if errors.Is(want, sentinel) {
			return errors.Is(err, sentinel)
		}
	}
	return false
}

func nodeEvents() []core.NodeEvent {
	var out []core.NodeEvent
	for _, tr := range []core.NodeTrigger{
		core.TriggerDepsSatisfied, core.TriggerDispatched, core.TriggerImplementerDone, core.TriggerReviewRejected,
		core.TriggerReviewApproved, core.TriggerGateFailed, core.TriggerGatesPassed, core.TriggerQueueTurn,
		core.TriggerMerged, core.TriggerIntegrationFailed, core.TriggerBlock, core.TriggerUnblock, core.TriggerFail, core.TriggerCancel,
	} {
		ev := core.NodeEvent{Trigger: tr}
		if tr == core.TriggerBlock {
			ev.Reason = core.BlockedQuestion
		}
		out = append(out, ev)
	}
	return out
}

// pathTo finds, by breadth-first search over core's machine, the triggers that
// lead a new node to the target state.
func pathTo(t *testing.T, target core.NodeState) []core.NodeEvent {
	t.Helper()
	type step struct {
		node core.Node
		path []core.NodeEvent
	}
	queue := []step{{node: core.Node{State: core.NodePlanned}}}
	seen := map[core.NodeState]bool{core.NodePlanned: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.node.State == target {
			return cur.path
		}
		for _, ev := range nodeEvents() {
			next, err := core.NextNode(cur.node, ev, limits)
			if err != nil || seen[next.State] {
				continue
			}
			seen[next.State] = true
			queue = append(queue, step{next, append(append([]core.NodeEvent{}, cur.path...), ev)})
		}
	}
	t.Fatalf("no path to node state %s", target)
	return nil
}

// TestNodeTransitions records every (state, trigger) pair of the node machine.
// Valid ones must produce one journal entry and the matching projection;
// invalid ones must write nothing (docs/contracts/state-machine.md).
func TestNodeTransitions(t *testing.T) {
	for _, from := range core.NodeStates {
		path := pathTo(t, from)
		for _, ev := range nodeEvents() {
			t.Run(fmt.Sprintf("%s/%s", from, ev.Trigger), func(t *testing.T) {
				t.Parallel()
				d := openTemp(t)
				createWorkspace(t, d, 1)
				addNode(t, d, 3, "nd_1", "1")
				n := 4
				var cur core.Node
				cur.State = core.NodePlanned
				for _, step := range path {
					cur = transitionNode(t, d, n, "nd_1", step)
					n++
				}
				before, beforeEvents := snapshot(t, d), events(t, d)

				want, wantErr := core.NextNode(cur, ev, limits)
				var got core.Node
				err := d.Tx(context.Background(), func(x *Tx) (err error) {
					got, err = x.TransitionNode("nd_1", ev, limits, hdr(n))
					return err
				})

				if wantErr != nil {
					if !sameKind(err, wantErr) {
						t.Fatalf("error = %v, want %v", err, wantErr)
					}
					if diff := cmp.Diff(before, snapshot(t, d)); diff != "" {
						t.Errorf("a refused transition changed the projection:\n%s", diff)
					}
					if got := len(events(t, d)); got != len(beforeEvents) {
						t.Errorf("a refused transition wrote %d events", got-len(beforeEvents))
					}
					return
				}
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				if got != want {
					t.Errorf("returned node = %+v, want %+v", got, want)
				}
				evs := events(t, d)
				if len(evs) != len(beforeEvents)+1 {
					t.Fatalf("journal grew by %d, want 1", len(evs)-len(beforeEvents))
				}
				last := evs[len(evs)-1]
				if last.Kind != contracts.EventNodeTransition || last.NodeID != "nd_1" {
					t.Errorf("journal entry = %+v", last)
				}
				row := snapshot(t, d).Nodes[0]
				if row.State != want.State || row.PrevState != want.PrevState || row.BlockedReason != want.BlockedReason ||
					row.Attempt != want.Attempt || row.Round != want.Round {
					t.Errorf("projection = %+v, want %+v", row, want)
				}
			})
		}
	}
}

// TestNodeLimitWritesNothing covers the limit errors of the machine.
func TestNodeLimitWritesNothing(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	n := 4
	for _, tr := range []core.NodeTrigger{core.TriggerDepsSatisfied, core.TriggerDispatched, core.TriggerImplementerDone} {
		transitionNode(t, d, n, "nd_1", core.NodeEvent{Trigger: tr})
		n++
	}
	before := len(events(t, d))
	err := d.Tx(context.Background(), func(x *Tx) error {
		_, err := x.TransitionNode("nd_1", core.NodeEvent{Trigger: core.TriggerReviewRejected}, core.Limits{MaxAttempts: 3, MaxReviewRounds: 1}, hdr(n))
		return err
	})
	if !errors.Is(err, core.ErrLimitReached) {
		t.Fatalf("error = %v, want ErrLimitReached", err)
	}
	if got := len(events(t, d)); got != before {
		t.Errorf("journal grew by %d", got-before)
	}
}

func workspaceTriggers() []core.WorkspaceTrigger {
	return []core.WorkspaceTrigger{
		core.TriggerCreate, core.TriggerCreateWithPlan, core.TriggerPlanProposed, core.TriggerPlanRejected,
		core.TriggerPlanApproved, core.TriggerStart, core.TriggerPause, core.TriggerResume, core.TriggerAllIntegrated,
		core.TriggerClosingDone, core.TriggerE2EFailed, core.TriggerMergedToMain, core.TriggerArchive,
		core.TriggerCancelAll, core.TriggerFatal,
	}
}

func workspacePathTo(t *testing.T, target core.WorkspaceState) []core.WorkspaceTrigger {
	t.Helper()
	type step struct {
		state core.WorkspaceState
		path  []core.WorkspaceTrigger
	}
	queue := []step{{state: core.WorkspaceNone}}
	seen := map[core.WorkspaceState]bool{core.WorkspaceNone: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.state == target {
			return cur.path
		}
		for _, tr := range workspaceTriggers() {
			next, err := core.NextWorkspace(cur.state, tr)
			if err != nil || seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, step{next, append(append([]core.WorkspaceTrigger{}, cur.path...), tr)})
		}
	}
	t.Fatalf("no path to workspace state %q", target)
	return nil
}

// TestWorkspaceTransitions is TestNodeTransitions for the workspace machine.
func TestWorkspaceTransitions(t *testing.T) {
	for _, from := range append([]core.WorkspaceState{core.WorkspaceNone}, core.WorkspaceStates...) {
		path := workspacePathTo(t, from)
		for _, tr := range workspaceTriggers() {
			t.Run(fmt.Sprintf("%q/%s", from, tr), func(t *testing.T) {
				t.Parallel()
				d := openTemp(t)
				tx(t, d, func(x *Tx) error {
					_, err := x.Append(Event{Header: hdr(1), Kind: contracts.EventWorkspaceCreated,
						Payload: payload(contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v0.1", BaseSHA: "abc"})})
					return err
				})
				n := 2
				for _, step := range path {
					tx(t, d, func(x *Tx) error { _, err := x.TransitionWorkspace(step, hdr(n)); return err })
					n++
				}
				before, beforeEvents := snapshot(t, d), len(events(t, d))

				want, wantErr := core.NextWorkspace(from, tr)
				var got core.WorkspaceState
				err := d.Tx(context.Background(), func(x *Tx) (err error) {
					got, err = x.TransitionWorkspace(tr, hdr(n))
					return err
				})
				if wantErr != nil {
					if !sameKind(err, wantErr) {
						t.Fatalf("error = %v, want %v", err, wantErr)
					}
					if diff := cmp.Diff(before, snapshot(t, d)); diff != "" || len(events(t, d)) != beforeEvents {
						t.Errorf("a refused transition wrote something:\n%s", diff)
					}
					return
				}
				if err != nil || got != want {
					t.Fatalf("got (%q, %v), want %q", got, err, want)
				}
				if len(events(t, d)) != beforeEvents+1 {
					t.Errorf("journal grew by %d, want 1", len(events(t, d))-beforeEvents)
				}
				if state := snapshot(t, d).Workspace.State; state != want {
					t.Errorf("projection state = %q, want %q", state, want)
				}
			})
		}
	}
}

// TestRebuildEqualsIncremental is the proof that the journal is the source of
// truth: the projections recomputed from it equal the ones kept incrementally
// (docs/contracts/data-model.md), and a corrupted projection is repaired.
func TestRebuildEqualsIncremental(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	for i, id := range []string{"nd_1", "nd_2", "nd_3"} {
		addNode(t, d, 3+i, id, fmt.Sprint(i+1))
	}
	tx(t, d, func(x *Tx) error {
		for i, e := range []contracts.EdgeAdded{
			{From: "nd_1", To: "nd_2", Kind: "blocks", Source: "tracker"},
			{From: "nd_2", To: "nd_3", Kind: "contract", Source: "plan"},
		} {
			if _, err := x.Append(Event{Header: hdr(10 + i), Kind: contracts.EventEdgeAdded, Payload: payload(e)}); err != nil {
				return err
			}
		}
		return nil
	})
	n := 20
	run := func(id string, evs ...core.NodeEvent) {
		for _, ev := range evs {
			transitionNode(t, d, n, id, ev)
			n++
		}
	}
	run("nd_1", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, core.NodeEvent{Trigger: core.TriggerDispatched},
		core.NodeEvent{Trigger: core.TriggerImplementerDone}, core.NodeEvent{Trigger: core.TriggerReviewRejected},
		core.NodeEvent{Trigger: core.TriggerImplementerDone}, core.NodeEvent{Trigger: core.TriggerReviewApproved},
		core.NodeEvent{Trigger: core.TriggerGatesPassed}, core.NodeEvent{Trigger: core.TriggerQueueTurn},
		core.NodeEvent{Trigger: core.TriggerMerged})
	run("nd_2", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, core.NodeEvent{Trigger: core.TriggerBlock, Reason: core.BlockedQuestion})
	run("nd_3", core.NodeEvent{Trigger: core.TriggerCancel})
	tx(t, d, func(x *Tx) error {
		if _, err := x.Append(Event{Header: hdr(n), Kind: contracts.EventMergeDone, NodeID: "nd_1", Payload: payload(contracts.MergeDone{MergeSHA: "m1"})}); err != nil {
			return err
		}
		if _, err := x.Append(Event{Header: hdr(n + 2), Kind: contracts.EventNodeFlow, NodeID: "nd_2", Payload: payload(contracts.NodeFlow{Step: 2, Run: json.RawMessage(`{"Step":2}`)})}); err != nil {
			return err
		}
		// Journal-only kinds do not affect the projections.
		_, err := x.Append(Event{Header: hdr(n + 1), Kind: "tool.call", NodeID: "nd_1", SessionID: "ss_1", Payload: payload(map[string]string{"tool": "bash"})})
		return err
	})
	for _, tr := range []core.WorkspaceTrigger{core.TriggerPlanProposed, core.TriggerPlanApproved, core.TriggerStart} {
		n += 3
		tx(t, d, func(x *Tx) error { _, err := x.TransitionWorkspace(tr, hdr(n)); return err })
	}

	incremental := snapshot(t, d)
	if incremental.Nodes[0].State != core.NodeIntegrated || incremental.Nodes[1].PrevState != core.NodeReady || incremental.Workspace.HeadSHA != "m1" ||
		incremental.Nodes[1].StepIndex != 2 || string(incremental.Nodes[1].Flow) != `{"Step":2}` {
		t.Fatalf("scenario did not produce the expected state: %+v", incremental)
	}

	must(t, d.Rebuild(context.Background()))
	if diff := cmp.Diff(incremental, snapshot(t, d)); diff != "" {
		t.Errorf("rebuild differs from the incremental state (-incremental +rebuilt):\n%s", diff)
	}

	// Corrupt the projections, then rebuild again.
	for _, q := range []string{`UPDATE nodes SET state = 'failed'`, `DELETE FROM edges`, `UPDATE workspace SET state = 'merged'`} {
		_, err := d.sql.Exec(q)
		must(t, err)
	}
	must(t, d.Rebuild(context.Background()))
	if diff := cmp.Diff(incremental, snapshot(t, d)); diff != "" {
		t.Errorf("rebuild did not repair the projections (-want +got):\n%s", diff)
	}
}

func TestRebuildPagesThroughLongJournals(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	tx(t, d, func(x *Tx) error {
		for i := 0; i < replayPage+10; i++ {
			if _, err := x.Append(Event{Header: hdr(100 + i), Kind: "session.message", Payload: payload(map[string]string{"text": "x"})}); err != nil {
				return err
			}
		}
		return nil
	})
	want := snapshot(t, d)
	must(t, d.Rebuild(context.Background()))
	if diff := cmp.Diff(want, snapshot(t, d)); diff != "" {
		t.Errorf("rebuild after %d events:\n%s", replayPage+10, diff)
	}
}
