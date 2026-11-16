package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
)

// A decision taken on a node another process moved meanwhile is not stored
// on the old state: it is taken again on the node as it is, here refused.
// kluzo node cancel that read the node ready, while kluzo run dispatched it,
// does not cancel a running node.
func TestDecisionOnANodeThatMovedMeanwhile(t *testing.T) {
	ctx := context.Background()
	project, db, gen, clk := testJournal(t)
	node, _ := gen.New(ids.Node, clk.Now())
	if err := CreateWorkspace(ctx, db, gen, clk, NewWorkspace{
		Created: contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v1", BaseSHA: "x"},
		Nodes:   []NewNode{{ID: node, Added: contracts.NodeAdded{IssueRef: "1", Title: "A", Areas: []string{"a"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	s, err := person(db, gen, clk, project)
	if err != nil {
		t.Fatal(err)
	}
	read := func() store.NodeRow {
		snap, err := db.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := snap.Node(node)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := s.decide(ctx, read(), core.Ready{}, nil); err != nil {
		t.Fatal(err)
	}
	stale := read() // kluzo node cancel read it ready
	if err := s.decide(ctx, read(), core.Dispatch{}, nil); err != nil {
		t.Fatal(err) // the loop dispatched it meanwhile
	}
	if err := s.decide(ctx, stale, core.Cancel{By: "kenny"}, nil); !errors.Is(err, core.ErrInvalidTransition) {
		t.Errorf("cancel of a node dispatched meanwhile: %v", err)
	}
	if n := read(); n.State != core.NodeRunning || n.Attempt != 1 {
		t.Errorf("node %s attempt %d", n.State, n.Attempt)
	}
}

// A person cannot put a blocked node back into work while another node holds
// the work slot, nor into the merge queue while another integrates; a
// dependent that is final does not stop a cancel; a proposal of a workspace
// that is over is not decided.
func TestPersonDecisionsRespectTheSlotAndTheQueue(t *testing.T) {
	ctx := context.Background()
	project, db, gen, clk := testJournal(t)
	a, _ := gen.New(ids.Node, clk.Now())
	b, _ := gen.New(ids.Node, clk.Now())
	if err := CreateWorkspace(ctx, db, gen, clk, NewWorkspace{
		Created: contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v1", BaseSHA: "x"},
		Nodes: []NewNode{{ID: a, Added: contracts.NodeAdded{IssueRef: "1", Title: "A", Areas: []string{"a"}}},
			{ID: b, Added: contracts.NodeAdded{IssueRef: "2", Title: "B", Areas: []string{"b"}}}},
		Edges: []core.Edge{{From: "1", To: "2", Kind: contracts.EdgeBlocks}},
	}); err != nil {
		t.Fatal(err)
	}
	move := func(node string, evs ...core.NodeEvent) {
		t.Helper()
		if err := db.Tx(ctx, func(x *store.Tx) error {
			for _, ev := range evs {
				id, _ := gen.New(ids.Event, clk.Now())
				if _, err := x.TransitionNode(node, ev, core.Limits{MaxAttempts: 3}, store.Header{ID: id, TS: clk.Now()}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	ready, dispatch := core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, core.NodeEvent{Trigger: core.TriggerDispatched}
	move(a, ready, dispatch, core.NodeEvent{Trigger: core.TriggerBlock, Reason: core.BlockedTriage})
	move(b, ready, dispatch) // dispatched while #1 was blocked
	for _, in := range []core.Input{core.Unblock{}, core.Retry{}} {
		if err := DecideNode(ctx, db, gen, clk, project, "1", in); !errors.Is(err, core.ErrInvalidTransition) || !strings.Contains(err.Error(), "#2 is running") {
			t.Errorf("%T while #2 runs: %v", in, err)
		}
	}
	move(b, core.NodeEvent{Trigger: core.TriggerFail})
	if err := DecideNode(ctx, db, gen, clk, project, "1", core.Cancel{By: "kenny"}); err != nil {
		t.Errorf("cancel with a failed dependent: %v", err)
	}
	if err := MoveWorkspace(ctx, db, gen, clk, core.TriggerCancelAll); err != nil {
		t.Fatal(err)
	}
	created, _ := json.Marshal(contracts.ProposalCreated{ProposalID: "pp_1", Kind: contracts.ProposalChange, Payload: json.RawMessage(`{"changes":[]}`)})
	if err := db.Tx(ctx, func(x *store.Tx) error {
		id, _ := gen.New(ids.Event, clk.Now())
		_, err := x.Append(store.Event{Header: store.Header{ID: id, TS: clk.Now()}, Kind: contracts.EventProposalCreated, NodeID: a, Payload: created})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := DecideProposal(ctx, db, gen, clk, project, "pp_1", true, "kenny"); !errors.Is(err, core.ErrInvalidTransition) || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("proposal of a cancelled workspace: %v", err)
	}
}

// testJournal is the default project and an empty journal.
func testJournal(t *testing.T) (*config.Project, *store.DB, *ids.Generator, *clock.Manual) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".kluzo")
	if _, err := config.Init(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "kluzo.yaml"))
	raw = []byte(strings.NewReplacer("__PROJECT_NAME__", "demo", "OWNER/REPO", "acme/demo").Replace(string(raw)))
	if err := os.WriteFile(filepath.Join(dir, "kluzo.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return project, db, ids.NewGenerator(rand.Reader), clock.NewManual(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
}
