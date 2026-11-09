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

// project applies one journal event to the projection tables. It is the only
// code that writes them, used both when appending and by Rebuild, so the two
// cannot diverge. Kinds without a projection are journal-only.
func (t *Tx) project(e Event) error {
	switch e.Kind {
	case contracts.EventWorkspaceCreated:
		return t.projectWorkspaceCreated(e)
	case contracts.EventWorkspaceTransition:
		return t.projectWorkspaceTransition(e)
	case contracts.EventNodeAdded:
		return t.projectNodeAdded(e)
	case contracts.EventNodeTransition:
		return t.projectNodeTransition(e)
	case contracts.EventEdgeAdded:
		return t.projectEdgeAdded(e)
	case contracts.EventMergeDone:
		return t.projectMergeDone(e)
	case contracts.EventNodeFlow:
		return t.projectNodeFlow(e)
	case contracts.EventSessionStarted:
		return t.projectSessionStarted(e)
	case contracts.EventSessionFinished:
		return t.projectSessionFinished(e)
	}
	return nil
}

func decodePayload(e Event, v any) error {
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("bad payload: %w", err)
	}
	return nil
}

func (t *Tx) projectWorkspaceCreated(e Event) error {
	var p contracts.WorkspaceCreated
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO workspace (id, state, profile, branch, base_sha, budget_usd_micros, updated_at) VALUES (?, '', NULLIF(?, ''), ?, ?, NULLIF(?, 0), ?)`,
		p.ID, p.Profile, p.Branch, p.BaseSHA, p.BudgetUSDMicros, formatTS(e.TS))
	return err
}

func (t *Tx) projectWorkspaceTransition(e Event) error {
	var p contracts.WorkspaceTransition
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, `UPDATE workspace SET state = ?, updated_at = ? WHERE state = ?`, p.To, formatTS(e.TS), p.From)
	return expectOne(res, err, fmt.Sprintf("workspace is not in state %q", p.From))
}

func (t *Tx) projectNodeAdded(e Event) error {
	var p contracts.NodeAdded
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	areas, _ := json.Marshal(append([]string{}, p.Areas...)) // marshaling strings cannot fail
	budget, _ := json.Marshal(p.Budget)                      // nor a struct of numbers
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO nodes (id, issue_ref, title, state, areas, budget, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.NodeID, p.IssueRef, p.Title, string(core.NodePlanned), string(areas), string(budget), formatTS(e.TS))
	return err
}

func (t *Tx) projectNodeTransition(e Event) error {
	var p contracts.NodeTransition
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	prev := ""
	if p.To == string(core.NodeBlocked) {
		prev = p.From // where to return to when unblocked
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE nodes SET state = ?, prev_state = NULLIF(?, ''), blocked_reason = NULLIF(?, ''), attempt = ?, round = ?, updated_at = ? WHERE id = ? AND state = ?`,
		p.To, prev, p.BlockedReason, p.Attempt, p.Round, formatTS(e.TS), e.NodeID, p.From)
	return expectOne(res, err, fmt.Sprintf("node %s is not in state %q", e.NodeID, p.From))
}

func (t *Tx) projectEdgeAdded(e Event) error {
	var p contracts.EdgeAdded
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO edges (from_node, to_node, kind, source) VALUES (?, ?, ?, ?)`, p.From, p.To, p.Kind, p.Source)
	return err
}

func (t *Tx) projectMergeDone(e Event) error {
	var p contracts.MergeDone
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, `UPDATE nodes SET merged_sha = ?, updated_at = ? WHERE id = ?`, p.MergeSHA, formatTS(e.TS), e.NodeID)
	if err := expectOne(res, err, fmt.Sprintf("node %s does not exist", e.NodeID)); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE workspace SET head_sha = ?, updated_at = ?`, p.MergeSHA, formatTS(e.TS))
	return err
}

func (t *Tx) projectNodeFlow(e Event) error {
	var p contracts.NodeFlow
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, `UPDATE nodes SET step_index = ?, flow = ?, updated_at = ? WHERE id = ?`, p.Step, string(p.Run), formatTS(e.TS), e.NodeID)
	return expectOne(res, err, fmt.Sprintf("node %s does not exist", e.NodeID))
}

// projectSessionStarted records a session. A Kluzo session runs again in a
// new harness session (the retries of the outbox), so a later start of the
// same id takes over the row.
func (t *Tx) projectSessionStarted(e Event) error {
	if e.SessionID == "" {
		return fmt.Errorf("%s without a session", e.Kind)
	}
	var p contracts.SessionStarted
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO sessions (id, node_id, role, harness, model, harness_session_ref, attempt, round, state, started_at)
		 VALUES (?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?, ?, 'running', ?)
		 ON CONFLICT (id) DO UPDATE SET harness = excluded.harness, model = excluded.model,
		   harness_session_ref = excluded.harness_session_ref, state = 'running', finished_at = NULL`,
		e.SessionID, e.NodeID, p.Role, p.Harness, p.Model, p.HarnessSessionRef, p.Attempt, p.Round, formatTS(e.TS))
	return err
}

// projectSessionFinished closes a session and adds its cost to the session,
// its node and the workspace: the cost is in the journal before the decision
// on the session, so a crash between the two does not lose it.
func (t *Tx) projectSessionFinished(e Event) error {
	if e.SessionID == "" {
		return fmt.Errorf("%s without a session", e.Kind)
	}
	var p contracts.SessionFinished
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	ts := formatTS(e.TS)
	// A harness that died before its init has no session.started: its row is
	// missing, but its cost still counts.
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE sessions SET state = ?, result = COALESCE(?, result), cost_usd_micros = cost_usd_micros + ?, finished_at = ? WHERE id = ?`,
		p.State, nullJSON(p.Result), p.CostUSDMicros, ts, e.SessionID); err != nil {
		return err
	}
	if e.NodeID != "" {
		res, err := t.tx.ExecContext(t.ctx, `UPDATE nodes SET cost_usd_micros = cost_usd_micros + ?, updated_at = ? WHERE id = ?`, p.CostUSDMicros, ts, e.NodeID)
		if err := expectOne(res, err, fmt.Sprintf("node %s does not exist", e.NodeID)); err != nil {
			return err
		}
	}
	_, err := t.tx.ExecContext(t.ctx, `UPDATE workspace SET cost_usd_micros = cost_usd_micros + ?, updated_at = ?`, p.CostUSDMicros, ts)
	return err
}

// WorkspaceCost is what the sessions of the workspace spent and the budget
// of the milestone (0 when the workspace has none of its own), in
// micro-dollars.
func (t *Tx) WorkspaceCost() (spent, budget int64, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT cost_usd_micros, COALESCE(budget_usd_micros, 0) FROM workspace`).Scan(&spent, &budget)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("%w: workspace", ErrNotFound)
	}
	return spent, budget, err
}

// nullJSON is a JSON column value: NULL when empty.
func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// expectOne turns "no row updated" into ErrStateMismatch.
func expectOne(res sql.Result, err error, msg string) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: %s", ErrStateMismatch, msg)
	}
	return nil
}

// replayPage is how many journal entries Rebuild reads at a time.
const replayPage = 500

// Rebuild drops the projections and recomputes them from the journal. It is
// how the store proves that the journal is the source of truth: the result
// must equal the incrementally maintained state.
func (d *DB) Rebuild(ctx context.Context) error {
	return d.Tx(ctx, func(t *Tx) error {
		for _, table := range []string{"sessions", "edges", "nodes", "workspace"} {
			if _, err := t.tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("clear %s: %w", table, err)
			}
		}
		for after := int64(0); ; {
			page, err := queryEvents(ctx, t.tx, after, replayPage, "")
			if err != nil {
				return err
			}
			for _, e := range page {
				if err := t.project(e); err != nil {
					return fmt.Errorf("replay event %d (%s): %w", e.Seq, e.Kind, err)
				}
				after = e.Seq
			}
			if len(page) < replayPage {
				break
			}
		}
		return nil
	})
}
