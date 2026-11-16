package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
	CostUSDMicros   int64              // what the sessions of the workspace spent
	PR              contracts.PROpened // the PR milestone → main, once opened
	// Milestone is the state of the last git.milestone effect: pending until
	// one is done.
	Milestone core.MilestoneState
	UpdatedAt string
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
	CostUSDMicros int64           // what the sessions of the node spent
	Flow          json.RawMessage // the run of the node flow (node.flow); nil before dispatch
	UpdatedAt     string
}

// EdgeRow is an edge projection.
type EdgeRow struct{ From, To, Kind, Source string }

// SessionRow is a session projection.
type SessionRow struct {
	ID, NodeID, Role, Harness, Model, HarnessRef string
	Attempt, Round                               int
	State                                        string
	Result                                       json.RawMessage // nil until a result was accepted
	CostUSDMicros                                int64
	StartedAt, FinishedAt                        string
}

// Snapshot is the state held in the projection tables.
type Snapshot struct {
	Workspace *WorkspaceRow // nil before workspace.created
	Nodes     []NodeRow     // ordered by id
	Edges     []EdgeRow     // ordered by from, to, kind
	Sessions  []SessionRow  // ordered by id
	GateRuns  []GateRunRow  // ordered by start
}

// Node is the node of the snapshot with this id.
func (s Snapshot) Node(id string) (NodeRow, error) {
	if i := slices.IndexFunc(s.Nodes, func(n NodeRow) bool { return n.ID == id }); i >= 0 {
		return s.Nodes[i], nil
	}
	return NodeRow{}, fmt.Errorf("%w: node %s", ErrNotFound, id)
}

// NodeByIssue is the node of the snapshot for an issue.
func (s Snapshot) NodeByIssue(issue string) (NodeRow, error) {
	if i := slices.IndexFunc(s.Nodes, func(n NodeRow) bool { return n.IssueRef == issue }); i >= 0 {
		return s.Nodes[i], nil
	}
	return NodeRow{}, fmt.Errorf("%w: no node for issue #%s", ErrNotFound, issue)
}

// Snapshot reads the projections.
func (d *DB) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	var w WorkspaceRow
	var state, cursor string
	var milestone string
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, state, COALESCE(profile, ''), branch, base_sha, COALESCE(head_sha, ''), COALESCE(budget_usd_micros, 0), cost_usd_micros, COALESCE(flow_cursor, ''), updated_at, (`+milestoneQuery+`) FROM workspace`).
		Scan(&w.ID, &state, &w.Profile, &w.Branch, &w.BaseSHA, &w.HeadSHA, &w.BudgetUSDMicros, &w.CostUSDMicros, &cursor, &w.UpdatedAt, &milestone)
	switch {
	case err == nil:
		w.State, w.Milestone = core.WorkspaceState(state), core.MilestoneState(milestone)
		if cursor != "" {
			var c closingCursor
			if err := json.Unmarshal([]byte(cursor), &c); err != nil {
				return s, fmt.Errorf("workspace: bad flow_cursor: %w", err)
			}
			w.PR = c.PR
		}
		s.Workspace = &w
	case !errors.Is(err, sql.ErrNoRows):
		return s, fmt.Errorf("read workspace: %w", err)
	}
	if s.Nodes, err = d.nodeRows(ctx); err != nil {
		return s, err
	}
	if s.Edges, err = d.edgeRows(ctx); err != nil {
		return s, err
	}
	if s.Sessions, err = d.sessionRows(ctx); err != nil {
		return s, err
	}
	s.GateRuns, err = d.gateRunRows(ctx)
	return s, err
}

// GateRunRow is a gate run projection.
type GateRunRow struct {
	ID, NodeID, Gate, SHA, State, LogRef string
	StartedAt, FinishedAt                string
}

func (d *DB) gateRunRows(ctx context.Context) ([]GateRunRow, error) {
	var out []GateRunRow
	err := each(ctx, d.sql,
		`SELECT id, COALESCE(node_id, ''), gate, sha, state, COALESCE(log_ref, ''), started_at, COALESCE(finished_at, '') FROM gate_runs ORDER BY started_at, id`, nil,
		func(rows *sql.Rows) error {
			var r GateRunRow
			if err := rows.Scan(&r.ID, &r.NodeID, &r.Gate, &r.SHA, &r.State, &r.LogRef, &r.StartedAt, &r.FinishedAt); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read gate runs: %w", err)
	}
	return out, nil
}

func (d *DB) nodeRows(ctx context.Context) ([]NodeRow, error) {
	var out []NodeRow
	err := each(ctx, d.sql,
		`SELECT id, issue_ref, title, state, COALESCE(prev_state, ''), COALESCE(blocked_reason, ''), areas, attempt, round, budget, COALESCE(merged_sha, ''), cost_usd_micros, COALESCE(flow, ''), updated_at
		   FROM nodes ORDER BY id`, nil,
		func(rows *sql.Rows) error {
			var n NodeRow
			var state, prev, reason, areas, budget, flow string
			if err := rows.Scan(&n.ID, &n.IssueRef, &n.Title, &state, &prev, &reason, &areas, &n.Attempt, &n.Round, &budget, &n.MergedSHA, &n.CostUSDMicros, &flow, &n.UpdatedAt); err != nil {
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

func (d *DB) sessionRows(ctx context.Context) ([]SessionRow, error) {
	var out []SessionRow
	err := each(ctx, d.sql,
		`SELECT id, COALESCE(node_id, ''), role, harness, model, COALESCE(harness_session_ref, ''), COALESCE(attempt, 0), COALESCE(round, 0),
		        state, COALESCE(result, ''), cost_usd_micros, started_at, COALESCE(finished_at, '')
		   FROM sessions ORDER BY id`, nil,
		func(rows *sql.Rows) error {
			var r SessionRow
			var result string
			if err := rows.Scan(&r.ID, &r.NodeID, &r.Role, &r.Harness, &r.Model, &r.HarnessRef, &r.Attempt, &r.Round,
				&r.State, &result, &r.CostUSDMicros, &r.StartedAt, &r.FinishedAt); err != nil {
				return err
			}
			if result != "" {
				r.Result = json.RawMessage(result)
			}
			out = append(out, r)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	return out, nil
}

// ProposalRow is a proposal projection.
type ProposalRow struct {
	ID, Node, Kind, State, DecidedBy, CreatedAt, DecidedAt string
	Payload                                                json.RawMessage
	Blocking                                               bool
}

// Proposals reads the proposals of the workspace, oldest first.
func (d *DB) Proposals(ctx context.Context) ([]ProposalRow, error) {
	var out []ProposalRow
	err := each(ctx, d.sql,
		`SELECT id, COALESCE(origin_node, ''), kind, payload, blocking, state, COALESCE(decided_by, ''), created_at, COALESCE(decided_at, '')
		   FROM proposals ORDER BY created_at, id`, nil,
		func(rows *sql.Rows) error {
			var p ProposalRow
			var payload string
			if err := rows.Scan(&p.ID, &p.Node, &p.Kind, &payload, &p.Blocking, &p.State, &p.DecidedBy, &p.CreatedAt, &p.DecidedAt); err != nil {
				return err
			}
			p.Payload = json.RawMessage(payload)
			out = append(out, p)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read proposals: %w", err)
	}
	return out, nil
}
