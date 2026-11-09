package supervisor_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/prompt"
	"github.com/kennyjsa/kluzo/internal/store"
)

// Each session is a row of sessions, and its session.started carries the
// harness conversation and the manifest of the prompt it got.
func TestSessionsAndManifest(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	snap, err := e.db.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 2 {
		t.Fatalf("sessions = %+v", snap.Sessions)
	}
	for _, s := range snap.Sessions {
		if s.NodeID != e.node || s.State != "finished" || s.HarnessRef == "" || s.CostUSDMicros != 10_000 || len(s.Result) == 0 || s.FinishedAt == "" {
			t.Errorf("session %+v", s)
		}
	}
	if roles := snap.Sessions[0].Role + "," + snap.Sessions[1].Role; roles != "implementer,reviewer" {
		t.Errorf("roles = %s", roles)
	}
	for _, ev := range e.events(contracts.EventSessionStarted) {
		var p contracts.SessionStarted
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		var m prompt.Manifest
		if err := json.Unmarshal(p.Manifest, &m); err != nil {
			t.Fatalf("manifest of %s: %v", ev.SessionID, err)
		}
		if p.HarnessSessionRef == "" || m.Protocol != contracts.ProtocolVersion || m.Harness != "script" || m.KluzoVersion != "test" ||
			!strings.HasPrefix(m.Segments.Task, "sha256:") || m.ProjectRules["AGENTS.md"] == nil || m.Limits.Findings == 0 {
			t.Errorf("session.started of %s: ref %q, manifest %+v", ev.SessionID, p.HarnessSessionRef, m)
		}
	}
	for _, ss := range snap.Sessions {
		b, err := os.ReadFile(filepath.Join(e.dir, "ws", "logs", "sessions", ss.ID+".prompt.md"))
		if err != nil || !strings.Contains(string(b), "## Deliver") {
			t.Errorf("prompt log of %s: %v", ss.ID, err)
		}
	}
	if n, _ := e.nodeRow(); n.CostUSDMicros != 20_000 {
		t.Errorf("node cost = %d", n.CostUSDMicros)
	}
	if snap.Workspace.CostUSDMicros != 20_000 {
		t.Errorf("workspace cost = %d", snap.Workspace.CostUSDMicros)
	}
}

// The cost of a session is in the journal before the decision on it: Kluzo
// stopping between the two keeps the cost of the session that ran, and the
// session that runs again adds its own.
func TestSessionCostSurvivesAStopBeforeTheDecision(t *testing.T) {
	e := newEnv(t, impl(implemented()), impl(implemented()), rev(reviewed(true, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	e.runtime.waited = func() { cancel() }
	for i := 0; i < 20 && ctx.Err() == nil; i++ {
		if _, err := e.sup.Step(ctx); err != nil && ctx.Err() == nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() == nil {
		t.Fatal("the session did not run")
	}
	if n, r := e.nodeRow(); n.CostUSDMicros != 10_000 || r.Head != "" {
		t.Fatalf("after the stop: cost %d, head %q (no decision expected)", n.CostUSDMicros, r.Head)
	}
	e.runtime.mu.Lock()
	e.runtime.waited = nil
	e.runtime.mu.Unlock()
	e.drain()
	n, _ := e.nodeRow()
	if n.State != core.NodeIntegrated || n.CostUSDMicros != 30_000 {
		t.Errorf("node %s, cost %d, want integrated with 3 sessions paid", n.State, n.CostUSDMicros)
	}
	snap, _ := e.db.Snapshot(context.Background())
	if len(snap.Sessions) != 2 || snap.Sessions[0].CostUSDMicros != 20_000 {
		t.Errorf("sessions = %+v (the implementer's ran twice)", snap.Sessions)
	}
}

// The milestone budget alert goes out once, when the cost of the workspace
// crosses budgets.milestone.usd × alert_at (US$ 60 × 1.1 by default).
func TestMilestoneBudgetAlert(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	gen := ids.NewGenerator(rand.Reader)
	id, _ := gen.New(ids.Event, epoch)
	spent, _ := json.Marshal(contracts.SessionFinished{State: "finished", CostUSDMicros: 65_995_000})
	if err := e.db.Tx(context.Background(), func(x *store.Tx) error {
		_, err := x.Append(store.Event{Header: store.Header{ID: id, TS: epoch}, Kind: contracts.EventSessionFinished, SessionID: "ss_earlier", Payload: spent})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	alerts := e.events(contracts.EventBudgetAlert)
	if len(alerts) != 1 {
		t.Fatalf("budget alerts = %d, want 1", len(alerts))
	}
	var a contracts.BudgetAlert
	_ = json.Unmarshal(alerts[0].Payload, &a)
	if a != (contracts.BudgetAlert{Scope: contracts.BudgetScopeMilestone, SpentUSDMicros: 66_005_000, LimitUSDMicros: 60_000_000}) {
		t.Errorf("alert = %+v", a)
	}
	found := false
	for _, n := range e.notifier.Sent() {
		found = found || strings.Contains(n.Title, "Milestone budget alert")
	}
	if !found {
		t.Errorf("notifications = %+v", e.notifier.Sent())
	}
}
