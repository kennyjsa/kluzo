package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

func TestOpenConfiguresSQLite(t *testing.T) {
	d := openTemp(t)
	for pragma, want := range map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1"} {
		var got string
		must(t, d.sql.QueryRow("PRAGMA "+pragma).Scan(&got))
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d := openAt(t, path)
	createWorkspace(t, d, 1)
	must(t, d.Close())

	// Reopening applies nothing again and keeps the data.
	d = openAt(t, path)
	if s := snapshot(t, d); s.Workspace == nil || s.Workspace.State != core.WorkspaceRefining {
		t.Errorf("workspace after reopen = %+v", s.Workspace)
	}
	var n int
	must(t, d.sql.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
	if n != 5 {
		t.Errorf("migrations applied = %d, want 5", n)
	}

	// A database from a newer binary is refused rather than misread.
	_, err := d.sql.Exec(`INSERT INTO schema_migrations (version) VALUES (99)`)
	must(t, err)
	must(t, d.Close())
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Open on a newer schema: error = %v, want ErrFutureSchema", err)
	}
}

func TestJournalIsAppendOnly(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	for _, q := range []string{`UPDATE events SET kind = 'x'`, `DELETE FROM events`} {
		if _, err := d.sql.Exec(q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q: error = %v, want append-only", q, err)
		}
	}
}

func TestTxRollsBackEverything(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	before, beforeEvents := snapshot(t, d), len(events(t, d))

	boom := errors.New("boom")
	err := d.Tx(context.Background(), func(x *Tx) error {
		if _, err := x.TransitionNode("nd_1", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, limits, hdr(4),
			EffectSpec{ID: "ef_1", Kind: "tracker.set_label", Key: "label", Payload: payload(map[string]string{"label": "orch/ready"})}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v, want boom", err)
	}
	if diff := cmp.Diff(before, snapshot(t, d)); diff != "" {
		t.Errorf("projection changed after rollback (-before +after):\n%s", diff)
	}
	if got := len(events(t, d)); got != beforeEvents {
		t.Errorf("journal has %d events after rollback, want %d", got, beforeEvents)
	}
	pending, err := d.PendingEffects(context.Background(), epoch.Add(time.Hour), 10)
	must(t, err)
	if len(pending) != 0 {
		t.Errorf("rollback left %d effects in the outbox", len(pending))
	}

	// A panic rolls back too, and is re-raised.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Tx swallowed the panic")
			}
		}()
		_ = d.Tx(context.Background(), func(x *Tx) error {
			_, _ = x.TransitionNode("nd_1", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, limits, hdr(5))
			panic("crash")
		})
	}()
	if got := len(events(t, d)); got != beforeEvents {
		t.Errorf("journal has %d events after a panic, want %d", got, beforeEvents)
	}
}

func TestAppendWritesJournalProjectionAndEffectsTogether(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "42")

	var seq int64
	tx(t, d, func(x *Tx) error {
		next, err := x.TransitionNode("nd_1", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, limits, hdr(4),
			EffectSpec{ID: "ef_1", Kind: "tracker.set_label", Key: "label", Payload: payload(map[string]string{"label": "orch/ready"})},
			EffectSpec{ID: "ef_2", Kind: "tracker.comment", Key: "comment", Payload: payload(map[string]string{"body": "ready"})})
		if next.State != core.NodeReady {
			t.Errorf("next state = %s", next.State)
		}
		return err
	})
	evs := events(t, d)
	last := evs[len(evs)-1]
	seq = last.Seq
	if last.Kind != contracts.EventNodeTransition || last.NodeID != "nd_1" || last.ID != "ev_0004" {
		t.Errorf("last event = %+v", last)
	}
	if got := snapshot(t, d).Nodes[0].State; got != core.NodeReady {
		t.Errorf("projection state = %s, want ready", got)
	}
	pending, err := d.PendingEffects(context.Background(), epoch.Add(time.Hour), 10)
	must(t, err)
	if len(pending) != 2 || pending[0].EventSeq != seq || pending[0].IdemKey != "ev_0004/label" || pending[1].IdemKey != "ev_0004/comment" {
		t.Errorf("pending effects = %+v", pending)
	}
}

func TestDuplicateEventIsRejected(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	err := d.Tx(context.Background(), func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(2), Kind: "session.message", Payload: payload(map[string]string{"text": "again"})})
		return err
	})
	if !errors.Is(err, ErrDuplicateEvent) {
		t.Errorf("error = %v, want ErrDuplicateEvent", err)
	}
	if got := len(events(t, d)); got != 2 {
		t.Errorf("journal has %d events, want 2", got)
	}
}

func TestProjectionRejectsStaleTransition(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	tests := []struct {
		name string
		ev   Event
	}{
		{"node from the wrong state", Event{Header: hdr(4), Kind: contracts.EventNodeTransition, NodeID: "nd_1",
			Payload: payload(contracts.NodeTransition{From: "running", To: "in_review"})}},
		{"unknown node", Event{Header: hdr(4), Kind: contracts.EventNodeTransition, NodeID: "nd_9",
			Payload: payload(contracts.NodeTransition{From: "planned", To: "ready"})}},
		{"workspace from the wrong state", Event{Header: hdr(4), Kind: contracts.EventWorkspaceTransition,
			Payload: payload(contracts.WorkspaceTransition{From: "running", To: "paused"})}},
		{"merge of an unknown node", Event{Header: hdr(4), Kind: contracts.EventMergeDone, NodeID: "nd_9",
			Payload: payload(contracts.MergeDone{MergeSHA: "x"})}},
		{"malformed payload", Event{Header: hdr(4), Kind: contracts.EventNodeAdded, NodeID: "nd_2", Payload: []byte(`{`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(events(t, d))
			err := d.Tx(context.Background(), func(x *Tx) error { _, err := x.Append(tt.ev); return err })
			if err == nil {
				t.Fatal("Append succeeded")
			}
			if got := len(events(t, d)); got != before {
				t.Errorf("journal grew to %d (was %d)", got, before)
			}
		})
	}
	if err := d.Tx(context.Background(), func(x *Tx) error {
		_, err := x.TransitionNode("nd_9", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, limits, hdr(6))
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("transition of an unknown node: error = %v, want ErrNotFound", err)
	}
	if err := d.Tx(context.Background(), func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(4), Kind: contracts.EventNodeTransition, NodeID: "nd_1",
			Payload: payload(contracts.NodeTransition{From: "running", To: "in_review"})})
		return err
	}); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("stale transition: error = %v, want ErrStateMismatch", err)
	}
}

func TestEventsPaging(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	page, err := d.Events(context.Background(), 1, 1)
	must(t, err)
	if len(page) != 1 || page[0].Seq != 2 {
		t.Errorf("page = %+v, want the event with seq 2", page)
	}
}

func TestOutbox(t *testing.T) {
	d := openTemp(t)
	ctx := context.Background()
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	tx(t, d, func(x *Tx) error {
		_, err := x.TransitionNode("nd_1", core.NodeEvent{Trigger: core.TriggerDepsSatisfied}, limits, hdr(4),
			EffectSpec{ID: "ef_a", Kind: "tracker.comment", Key: "a", Payload: payload("a")},
			EffectSpec{ID: "ef_b", Kind: "tracker.comment", Key: "b", Payload: payload("b")},
			EffectSpec{ID: "ef_c", Kind: "tracker.comment", Key: "c", Payload: payload("c")})
		return err
	})
	now := epoch.Add(time.Hour)

	complete := func(id string) error { return d.Tx(ctx, func(x *Tx) error { return x.CompleteEffect(id, now) }) }
	fail := func(id, msg string) error { return d.Tx(ctx, func(x *Tx) error { return x.FailEffect(id, msg) }) }
	must(t, complete("ef_a"))
	if n, err := d.BeginEffect(ctx, "ef_b"); err != nil || n != 1 {
		t.Fatalf("BeginEffect = %d, %v", n, err)
	}
	must(t, d.Tx(ctx, func(x *Tx) error { return x.RetryEffect("ef_b", "tracker down", now.Add(time.Minute)) }))
	pending, err := d.PendingEffects(ctx, now, 10)
	must(t, err)
	if len(pending) != 1 || pending[0].ID != "ef_c" {
		t.Fatalf("before the backoff only ef_c is pending, got %+v", pending)
	}
	pending, err = d.PendingEffects(ctx, now.Add(time.Minute), 10)
	must(t, err)
	if len(pending) != 2 || pending[0].ID != "ef_b" || pending[0].Attempts != 1 || pending[0].LastError != "tracker down" || !pending[0].NextAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after the backoff ef_b is pending again, got %+v", pending)
	}
	if n, _ := d.BeginEffect(ctx, "ef_b"); n != 2 {
		t.Errorf("second run = %d, want 2", n)
	}
	must(t, fail("ef_b", "gave up"))
	pending, _ = d.PendingEffects(ctx, now.Add(time.Hour), 10)
	if len(pending) != 1 || pending[0].ID != "ef_c" {
		t.Errorf("a failed effect must leave the queue, got %+v", pending)
	}
	// Completing twice, or an unknown effect, is an error.
	if err := complete("ef_a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second completion: error = %v, want ErrNotFound", err)
	}
	if err := fail("ef_zzz", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown effect: error = %v, want ErrNotFound", err)
	}
	if _, err := d.BeginEffect(ctx, "ef_a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("begin of a done effect: error = %v, want ErrNotFound", err)
	}
}

// A database from an older binary gets the projections a migration adds,
// recomputed from its journal; a session that runs again drops the result of
// the run before.
func TestMigrationRebuildsNewProjections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d := openAt(t, path)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	tx(t, d, func(x *Tx) error {
		for i, e := range []Event{
			{Kind: contracts.EventSessionStarted, Payload: payload(contracts.SessionStarted{Role: "implementer", Harness: "h", Model: "m"})},
			{Kind: contracts.EventSessionFinished, Payload: payload(contracts.SessionFinished{State: "finished", CostUSDMicros: 5, Result: json.RawMessage(`{"v":1}`)})},
			{Kind: contracts.EventSessionStarted, Payload: payload(contracts.SessionStarted{Role: "implementer", Harness: "h", Model: "m"})},
			{Kind: contracts.EventSessionFinished, Payload: payload(contracts.SessionFinished{State: "failed", CostUSDMicros: 7})},
			{Kind: contracts.EventGateStarted, Payload: payload(contracts.GateRun{Gate: "test", SHA: "a1"})},
			{Kind: contracts.EventGateFinished, Payload: payload(contracts.GateRun{Gate: "test", SHA: "a1", State: "passed", LogRef: "logs/gates/1.log"})},
			{Kind: contracts.EventProposalCreated, Payload: payload(contracts.ProposalCreated{ProposalID: "pp_1", Kind: contracts.ProposalChange, Blocking: true, Payload: json.RawMessage(`{"changes":[]}`)})},
			{Kind: contracts.EventProposalCreated, Payload: payload(contracts.ProposalCreated{ProposalID: "pp_2", Kind: contracts.ProposalChange, Blocking: true})},
			{Kind: contracts.EventProposalDecided, Payload: payload(contracts.ProposalDecided{ProposalID: "pp_1", Kind: contracts.ProposalChange, State: contracts.ProposalApproved, By: "kenny"})},
		} {
			e.Header, e.NodeID, e.SessionID = hdr(10+i), "nd_1", "ss_1"
			if _, err := x.Append(e); err != nil {
				return err
			}
		}
		return nil
	})
	// Back to version 2: no sessions nor gate_runs table, no cost.
	for _, q := range []string{`DROP TABLE proposals`, `DROP TABLE sessions`, `DROP TABLE gate_runs`, `UPDATE nodes SET cost_usd_micros = 0`, `UPDATE workspace SET cost_usd_micros = 0`, `DELETE FROM schema_migrations WHERE version >= 3`} {
		_, err := d.sql.Exec(q)
		must(t, err)
	}
	must(t, d.Close())
	d = openAt(t, path)
	s := snapshot(t, d)
	if len(s.Sessions) != 1 || s.Sessions[0].State != "failed" || s.Sessions[0].Result != nil || s.Sessions[0].CostUSDMicros != 12 ||
		s.Nodes[0].CostUSDMicros != 12 || s.Workspace.CostUSDMicros != 12 {
		t.Errorf("after the migration: sessions %+v, nodes %+v, workspace %+v", s.Sessions, s.Nodes, s.Workspace)
	}
	if len(s.GateRuns) != 1 || s.GateRuns[0].State != "passed" || s.GateRuns[0].LogRef != "logs/gates/1.log" || s.GateRuns[0].FinishedAt == "" {
		t.Errorf("after the migration: gate runs %+v", s.GateRuns)
	}
	ps, err := d.Proposals(context.Background())
	must(t, err)
	if len(ps) != 2 || ps[0].ID != "pp_1" || ps[0].State != contracts.ProposalApproved || ps[0].DecidedBy != "kenny" || ps[0].Node != "nd_1" || !ps[0].Blocking ||
		string(ps[0].Payload) != `{"changes":[]}` || ps[1].State != contracts.ProposalPending || string(ps[1].Payload) != `{}` {
		t.Errorf("after the migration: proposals %+v", ps)
	}
	// A decision on a proposal that is not pending is refused.
	err = d.Tx(context.Background(), func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(40), Kind: contracts.EventProposalDecided, Payload: payload(contracts.ProposalDecided{ProposalID: "pp_1", State: contracts.ProposalRejected})})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("deciding twice: %v", err)
	}
}

// The projections of the merge queue and the closing: gate runs from
// gate.started/gate.finished (a rerun is a new row), the areas a deviation
// adds, and the PR the closing opened; Rebuild reaches the same state.
func TestMergeQueueProjections(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	gate := func(kind, state string) Event {
		return Event{Kind: kind, NodeID: "nd_1", Payload: payload(contracts.GateRun{Gate: "test", SHA: "a1", State: state})}
	}
	tx(t, d, func(x *Tx) error {
		for i, e := range []Event{
			gate(contracts.EventGateStarted, ""),
			gate(contracts.EventGateFinished, "failed"),
			gate(contracts.EventGateStarted, ""),
			gate(contracts.EventGateFinished, "passed"),
			gate(contracts.EventGateStarted, ""),
			{Kind: contracts.EventAreaDeviation, NodeID: "nd_1", Payload: payload(contracts.AreaDeviation{Declared: []string{"agenda"}, Touched: []string{"agenda", "auth", contracts.SharedArea}, Files: []string{"internal/auth/a.go"}})},
			{Kind: contracts.EventPROpened, Payload: payload(contracts.PROpened{Ref: "7", URL: "https://x/7"})},
		} {
			e.Header = hdr(10 + i)
			if _, err := x.Append(e); err != nil {
				return err
			}
		}
		return nil
	})
	check := func(when string) {
		s := snapshot(t, d)
		var states []string
		for _, r := range s.GateRuns {
			states = append(states, r.State)
		}
		if !slices.Equal(states, []string{"failed", "passed", "running"}) || s.GateRuns[0].ID != "ev_0010" || s.GateRuns[2].FinishedAt != "" {
			t.Errorf("%s: gate runs %+v", when, s.GateRuns)
		}
		if !slices.Equal(s.Nodes[0].Areas, []string{"agenda", "auth"}) {
			t.Errorf("%s: areas %v", when, s.Nodes[0].Areas)
		}
		if s.Workspace.PR != (contracts.PROpened{Ref: "7", URL: "https://x/7"}) {
			t.Errorf("%s: PR %+v", when, s.Workspace.PR)
		}
	}
	check("appended")
	must(t, d.Rebuild(context.Background()))
	check("rebuilt")
}

// A crash cuts a gate run: the gate.started of the next run turns the run
// still running of the same gate of the same node into interrupted, finished
// at the new start; other gates and nodes keep their rows; Rebuild agrees.
func TestGateRunInterruptedByCrash(t *testing.T) {
	d := openTemp(t)
	createWorkspace(t, d, 1)
	addNode(t, d, 3, "nd_1", "1")
	addNode(t, d, 4, "nd_2", "2")
	gate := func(node, name, sha string) Event {
		return Event{Kind: contracts.EventGateStarted, NodeID: node, Payload: payload(contracts.GateRun{Gate: name, SHA: sha})}
	}
	tx(t, d, func(x *Tx) error {
		for i, e := range []Event{
			gate("nd_1", "test", "a1"), // cut by the crash
			gate("nd_1", "lint", "a1"), // another gate: stays running
			gate("nd_2", "test", "b1"), // another node: stays running
			gate("nd_1", "test", "a2"), // the run after the restart, on a new commit
		} {
			e.Header = hdr(10 + i)
			if _, err := x.Append(e); err != nil {
				return err
			}
		}
		return nil
	})
	check := func(when string) {
		got := map[string]GateRunRow{}
		for _, r := range snapshot(t, d).GateRuns {
			got[r.ID] = r
		}
		if r := got["ev_0010"]; r.State != "interrupted" || r.FinishedAt != formatTS(hdr(13).TS) {
			t.Errorf("%s: cut run %+v", when, r)
		}
		for _, id := range []string{"ev_0011", "ev_0012", "ev_0013"} {
			if r := got[id]; r.State != "running" || r.FinishedAt != "" {
				t.Errorf("%s: run %s %+v", when, id, r)
			}
		}
	}
	check("appended")
	must(t, d.Rebuild(context.Background()))
	check("rebuilt")
}

// The journals of other workspaces are read without migrating them: a
// database at another version is refused, and nothing is written.
func TestOpenReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	d := openAt(t, path)
	createWorkspace(t, d, 1)
	must(t, d.Close())
	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := ro.Snapshot(ctx); err != nil || s.Workspace == nil {
		t.Errorf("snapshot = %+v, %v", s.Workspace, err)
	}
	if err := ro.Tx(ctx, func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(50), Kind: "x", Payload: []byte(`{}`)})
		return err
	}); err == nil {
		t.Error("a read-only journal took a write")
	}
	must(t, ro.Close())
	d = openAt(t, path)
	_, err = d.sql.Exec(`DELETE FROM schema_migrations WHERE version = 5`)
	must(t, err)
	must(t, d.Close())
	if _, err := OpenReadOnly(ctx, path); !errors.Is(err, ErrSchema) {
		t.Errorf("older schema: %v", err)
	}
	if _, err := OpenReadOnly(ctx, filepath.Join(t.TempDir(), "none.db")); err == nil {
		t.Error("a missing journal was opened")
	}
}
