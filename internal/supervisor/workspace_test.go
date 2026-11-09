package supervisor_test

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// The life of a workspace from its plan: created awaiting approval (the
// milestone branch is made ready, nothing runs), approved by a person (the
// plan is committed on the milestone branch and pushed), started, run to
// ready to merge; after the human merge, Run returns on its own.
func TestWorkspaceFromPlan(t *testing.T) {
	e := newEnvWith(t, envOpts{fromPlan: true}, impl(implemented()), rev(reviewed(true, nil)))
	ctx := context.Background()
	e.drain()
	snap, _ := e.db.Snapshot(ctx)
	if snap.Workspace.State != core.WorkspaceAwaitingApproval || snap.Workspace.Milestone != core.MilestoneDone || len(e.moves()) != 0 {
		t.Fatalf("before the approval: workspace %s, milestone %s, moves %v", snap.Workspace.State, snap.Workspace.Milestone, e.moves())
	}
	plan := []byte("version: 1\nmilestone: v1\n")
	if err := supervisor.ApprovePlan(ctx, e.db, ids.NewGenerator(rand.Reader), e.clock, supervisor.PlanApproval{
		Approved: contracts.PlanApproved{PlanVersion: 1, By: "kenny"}, Path: ".kluzo/plans/v1.yaml", Content: plan,
	}); err != nil {
		t.Fatal(err)
	}
	approved := e.events(contracts.EventPlanApproved)
	if len(approved) != 1 {
		t.Fatal("plan.approved not journaled")
	}
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
		t.Fatalf("node %s, moves %v", n.State, e.moves())
	}
	// The commit says who approved it and when, with the values of plan.approved (Q64).
	stamped := "version: 1\nmilestone: v1\napproved_by: \"kenny\"\napproved_at: " + approved[0].TS.UTC().Format("2006-01-02T15:04:05.000Z") + "\n"
	if got := e.git(e.bare(), "show", "milestone/v1:.kluzo/plans/v1.yaml"); got+"\n" != stamped {
		t.Errorf("plan on the remote milestone = %q, want %q", got, stamped)
	}
	if !strings.Contains(e.git(e.bare(), "log", "--format=%s", "milestone/v1"), "docs(plan): approve plan v1") {
		t.Error("no commit of the plan")
	}
	snap, _ = e.db.Snapshot(ctx)
	if err := e.scm.Merge(snap.Workspace.PR.Ref); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	done := make(chan error, 1)
	go func() { done <- e.sup.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the workspace was merged")
	}
	if snap, _ = e.db.Snapshot(ctx); snap.Workspace.State != core.WorkspaceMerged {
		t.Errorf("workspace %s", snap.Workspace.State)
	}
}

// The supervisor refuses to start without any of its required dependencies:
// a production assembly that forgets one fails here, not in the middle of a
// milestone.
func TestNewNeedsEveryDependency(t *testing.T) {
	dir := t.TempDir()
	makeRepo(t, filepath.Join(dir, "repo"))
	full, cfg := assemble(t, dir, deps{db: openDB(t, filepath.Join(dir, "state.db")), clock: clock.NewManual(epoch), ids: ids.NewGenerator(rand.Reader),
		tracker: fakes.NewTracker(clock.NewManual(epoch)), runtime: &script{t: t}, gates: fakes.NewGates(), notifier: &fakes.Notifier{}}, "1", "")
	optional := map[string]bool{"GitToken": true, "Log": true}
	typ := reflect.TypeOf(full)
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if optional[name] {
			continue
		}
		d := full
		reflect.ValueOf(&d).Elem().Field(i).SetZero()
		if _, err := supervisor.New(d, cfg); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: %v", name, err)
		}
	}
	if _, err := supervisor.New(full, cfg); err != nil {
		t.Errorf("with every dependency: %v", err)
	}
}
