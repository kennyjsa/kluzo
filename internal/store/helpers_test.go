package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

var epoch = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// hdr returns the header of the n-th event of a test: fixed id, fixed time.
func hdr(n int) Header {
	return Header{ID: fmt.Sprintf("ev_%04d", n), TS: epoch.Add(time.Duration(n) * time.Second)}
}

func openAt(t *testing.T, path string) *DB {
	t.Helper()
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func openTemp(t *testing.T) *DB {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "ws", "state.db"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func tx(t *testing.T, d *DB, fn func(*Tx) error) {
	t.Helper()
	must(t, d.Tx(context.Background(), fn))
}

func payload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// createWorkspace journals workspace.created plus the create transition, using
// events n and n+1.
func createWorkspace(t *testing.T, d *DB, n int) {
	t.Helper()
	tx(t, d, func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(n), Kind: contracts.EventWorkspaceCreated,
			Payload: payload(contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v0.1", BaseSHA: "abc123", BudgetUSDMicros: 60_000_000})})
		if err != nil {
			return err
		}
		_, err = x.TransitionWorkspace(core.TriggerCreate, hdr(n+1))
		return err
	})
}

// addNode journals node.added for the node with the given id and issue.
func addNode(t *testing.T, d *DB, n int, id, issue string) {
	t.Helper()
	tx(t, d, func(x *Tx) error {
		_, err := x.Append(Event{Header: hdr(n), Kind: contracts.EventNodeAdded, NodeID: id,
			Payload: payload(contracts.NodeAdded{IssueRef: issue, Title: "Issue " + issue, Areas: []string{"agenda"},
				Budget: contracts.NodeBudget{Attempts: 3, Minutes: 60, USDMicros: 6_000_000}})})
		return err
	})
}

var limits = core.Limits{MaxAttempts: 3, MaxReviewRounds: 3}

func transitionNode(t *testing.T, d *DB, n int, id string, ev core.NodeEvent) core.Node {
	t.Helper()
	var next core.Node
	tx(t, d, func(x *Tx) (err error) {
		next, err = x.TransitionNode(id, ev, limits, hdr(n))
		return err
	})
	return next
}

func snapshot(t *testing.T, d *DB) Snapshot {
	t.Helper()
	s, err := d.Snapshot(context.Background())
	must(t, err)
	return s
}

func events(t *testing.T, d *DB) []Event {
	t.Helper()
	e, err := d.Events(context.Background(), 0, 0)
	must(t, err)
	return e
}
