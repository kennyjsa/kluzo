package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

// WorkspaceRow is the workspace projection.
type WorkspaceRow struct {
	ID              string
	State           core.WorkspaceState
	Profile         string
	Branch          string
	BaseSHA         string
	HeadSHA         string
	BudgetUSDMicros int64
	UpdatedAt       string
}

// NodeRow is a node projection.
type NodeRow struct {
	ID            string
	IssueRef      string
	Title         string
	State         core.NodeState
	PrevState     core.NodeState
	BlockedReason core.BlockedReason
	Areas         []string
	Attempt       int
	Round         int
	Budget        contracts.NodeBudget
	MergedSHA     string
	Flow          json.RawMessage // the run of the node flow (node.flow); nil before dispatch
	UpdatedAt     string
}

// EdgeRow is an edge projection.
type EdgeRow struct{ From, To, Kind, Source string }

// Snapshot is the state held in the projection tables.
type Snapshot struct {
	Workspace *WorkspaceRow // nil before workspace.created
	Nodes     []NodeRow     // ordered by id
	Edges     []EdgeRow     // ordered by from, to, kind
}

// Snapshot reads the projections.
func (d *DB) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	var w WorkspaceRow
	var state string
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, state, COALESCE(profile, ''), branch, base_sha, COALESCE(head_sha, ''), COALESCE(budget_usd_micros, 0), updated_at FROM workspace`).
		Scan(&w.ID, &state, &w.Profile, &w.Branch, &w.BaseSHA, &w.HeadSHA, &w.BudgetUSDMicros, &w.UpdatedAt)
	switch {
	case err == nil:
		w.State = core.WorkspaceState(state)
		s.Workspace = &w
	case !errors.Is(err, sql.ErrNoRows):
		return s, fmt.Errorf("read workspace: %w", err)
	}
	if s.Nodes, err = d.nodeRows(ctx); err != nil {
		return s, err
	}
	s.Edges, err = d.edgeRows(ctx)
	return s, err
}

func (d *DB) nodeRows(ctx context.Context) ([]NodeRow, error) {
	var out []NodeRow
	err := each(ctx, d.sql,
		`SELECT id, issue_ref, title, state, COALESCE(prev_state, ''), COALESCE(blocked_reason, ''), areas, attempt, round, budget, COALESCE(merged_sha, ''), COALESCE(flow, ''), updated_at
		   FROM nodes ORDER BY id`, nil,
		func(rows *sql.Rows) error {
			var n NodeRow
			var state, prev, reason, areas, budget, flow string
			if err := rows.Scan(&n.ID, &n.IssueRef, &n.Title, &state, &prev, &reason, &areas, &n.Attempt, &n.Round, &budget, &n.MergedSHA, &flow, &n.UpdatedAt); err != nil {
				return err
			}
			if flow != "" {
				n.Flow = json.RawMessage(flow)
			}
			n.State, n.PrevState, n.BlockedReason = core.NodeState(state), core.NodeState(prev), core.BlockedReason(reason)
			if err := json.Unmarshal([]byte(areas), &n.Areas); err != nil {
				return fmt.Errorf("node %s: bad areas: %w", n.ID, err)
			}
			if err := json.Unmarshal([]byte(budget), &n.Budget); err != nil {
				return fmt.Errorf("node %s: bad budget: %w", n.ID, err)
			}
			out = append(out, n)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read nodes: %w", err)
	}
	return out, nil
}

func (d *DB) edgeRows(ctx context.Context) ([]EdgeRow, error) {
	var out []EdgeRow
	err := each(ctx, d.sql, `SELECT from_node, to_node, kind, source FROM edges ORDER BY from_node, to_node, kind`, nil,
		func(rows *sql.Rows) error {
			var e EdgeRow
			if err := rows.Scan(&e.From, &e.To, &e.Kind, &e.Source); err != nil {
				return err
			}
			out = append(out, e)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read edges: %w", err)
	}
	return out, nil
}
