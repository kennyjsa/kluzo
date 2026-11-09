package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
)

// kluzo proposal list|approve|reject and kluzo node retry|unblock|cancel find
// the workspace by the proposal id or the issue, among those of the project.
func TestProposalAndNodeCommands(t *testing.T) {
	ctx := context.Background()
	e := newWorkspaceEnv(t)
	if _, err := (PlanWorkspace{Host: e.host}).Execute(ctx, PlanWorkspaceInput{ConfigDir: e.dir, Milestone: "v0.1"}); err != nil {
		t.Fatal(err)
	}
	h := e.host.fill()
	o, err := open(ctx, h, e.dir, "v0.1")
	if err != nil {
		t.Fatal(err)
	}
	// Node #1 dispatched, and blocked for a proposal by the check after its round.
	var node string
	for _, n := range o.snap.Nodes {
		if n.IssueRef == "1" {
			node = n.ID
		}
	}
	hdr := func() store.Header {
		id, _ := h.IDs.New(ids.Event, h.Clock.Now())
		return store.Header{ID: id, TS: h.Clock.Now()}
	}
	lim := core.Limits{MaxAttempts: 3}
	changes, _ := json.Marshal(map[string]any{"changes": []core.ProposalChange{{Path: "a_test.go", Reason: "a_test.go is a test file and was deleted"}}})
	created, _ := json.Marshal(contracts.ProposalCreated{ProposalID: "pp_1", Kind: contracts.ProposalChange, State: contracts.ProposalPending, Blocking: true, Payload: changes})
	if err := o.db.Tx(ctx, func(x *store.Tx) error {
		for _, ev := range []core.NodeEvent{{Trigger: core.TriggerDepsSatisfied}, {Trigger: core.TriggerDispatched}, {Trigger: core.TriggerBlock, Reason: core.BlockedProposal}} {
			if _, err := x.TransitionNode(node, ev, lim, hdr()); err != nil {
				return err
			}
		}
		_, err := x.Append(store.Event{Header: hdr(), Kind: contracts.EventProposalCreated, NodeID: node, Payload: created})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = o.db.Close()

	items, err := ListProposals{Host: e.host}.Execute(ctx, e.dir)
	if err != nil || len(items) != 1 || items[0].ID != "pp_1" || items[0].Issue != "1" || items[0].Milestone != "v0.1" || items[0].State != "pending" ||
		len(items[0].Changes) != 1 || !strings.Contains(items[0].Changes[0], "deleted") {
		t.Fatalf("list = %+v, %v", items, err)
	}
	node1 := func(a NodeAction) error {
		return DecideNode{Host: e.host}.Execute(ctx, DecideNodeInput{ConfigDir: e.dir, Issue: "1", Action: a})
	}
	if err := node1(NodeUnblock); !errors.Is(err, ErrRefused) {
		t.Errorf("unblock of a node blocked for a proposal: %v", err)
	}
	if err := (DecideProposal{Host: e.host}).Execute(ctx, DecideProposalInput{ConfigDir: e.dir, ID: "pp_9", Approve: true}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown proposal: %v", err)
	}
	if err := (DecideProposal{Host: e.host}).Execute(ctx, DecideProposalInput{ConfigDir: e.dir, ID: "pp_1"}); err != nil {
		t.Fatal(err)
	}
	items, _ = ListProposals{Host: e.host}.Execute(ctx, e.dir)
	if len(items) != 1 || items[0].State != "rejected" || items[0].DecidedBy != "kenny" {
		t.Errorf("after the rejection: %+v", items)
	}
	if err := node1(NodeRetry); !errors.Is(err, ErrRefused) {
		t.Errorf("retry of a running node: %v", err)
	}
	if err := node1(NodeCancel); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "#2") {
		t.Errorf("cancel of a node #2 depends on: %v", err)
	}
	if err := (DecideNode{Host: e.host}).Execute(ctx, DecideNodeInput{ConfigDir: e.dir, Issue: "2", Action: NodeCancel}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "#3") {
		t.Errorf("cancel of a node #3 depends on: %v", err)
	}
	for _, issue := range []string{"3", "2"} {
		if err := (DecideNode{Host: e.host}).Execute(ctx, DecideNodeInput{ConfigDir: e.dir, Issue: issue, Action: NodeCancel}); err != nil {
			t.Fatalf("cancel #%s: %v", issue, err)
		}
	}
	if err := (DecideNode{Host: e.host}).Execute(ctx, DecideNodeInput{ConfigDir: e.dir, Issue: "77", Action: NodeRetry}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown issue: %v", err)
	}
	st, err := Status{Host: e.host}.Execute(ctx, StatusInput{ConfigDir: e.dir, Milestone: "v0.1"})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, n := range st.Nodes {
		states[n.Issue] = n.State
	}
	if states["1"] != "running" || states["2"] != "cancelled" || states["3"] != "cancelled" {
		t.Errorf("states %v", states)
	}
}
