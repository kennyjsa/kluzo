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
	case contracts.EventGateStarted:
		return t.projectGateStarted(e)
	case contracts.EventGateFinished:
		return t.projectGateFinished(e)
	case contracts.EventAreaDeviation:
		return t.projectAreaDeviation(e)
	case contracts.EventPROpened:
		return t.projectPROpened(e)
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
		   harness_session_ref = excluded.harness_session_ref, state = 'running', result = NULL, finished_at = NULL`,
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
		`UPDATE sessions SET state = ?, result = ?, cost_usd_micros = cost_usd_micros + ?, finished_at = ? WHERE id = ?`,
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

// projectGateStarted records a gate run, running, under the id of its event.
// A run of the same gate of the same node still running was cut by a crash:
// it becomes interrupted, finished when the new run starts
// (docs/contracts/data-model.md), so no row stays running for ever.
func (t *Tx) projectGateStarted(e Event) error {
	var p contracts.GateRun
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	ts := formatTS(e.TS)
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE gate_runs SET state = 'interrupted', finished_at = ? WHERE COALESCE(node_id, '') = ? AND gate = ? AND state = 'running'`,
		ts, e.NodeID, p.Gate); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO gate_runs (id, node_id, gate, sha, state, started_at) VALUES (?, NULLIF(?, ''), ?, ?, 'running', ?)`,
		e.ID, e.NodeID, p.Gate, p.SHA, ts)
	return err
}

// projectGateFinished closes the last running run of the gate on the same
// commit. A gate.finished without its gate.started (an older journal) is
// recorded as a finished run of its own.
func (t *Tx) projectGateFinished(e Event) error {
	var p contracts.GateRun
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	ts := formatTS(e.TS)
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE gate_runs SET state = ?, log_ref = NULLIF(?, ''), finished_at = ?
		  WHERE id = (SELECT id FROM gate_runs WHERE COALESCE(node_id, '') = ? AND gate = ? AND sha = ? AND state = 'running' ORDER BY started_at DESC, id DESC LIMIT 1)`,
		p.State, p.LogRef, ts, e.NodeID, p.Gate, p.SHA)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx,
		`INSERT INTO gate_runs (id, node_id, gate, sha, state, log_ref, started_at, finished_at) VALUES (?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		e.ID, e.NodeID, p.Gate, p.SHA, p.State, p.LogRef, ts, ts)
	return err
}

// projectAreaDeviation adds to the node the areas its diff touched, but the
// shared area (ADR 0011).
func (t *Tx) projectAreaDeviation(e Event) error {
	var p contracts.AreaDeviation
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, `SELECT areas FROM nodes WHERE id = ?`, e.NodeID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: node %s", ErrNotFound, e.NodeID)
	}
	if err != nil {
		return err
	}
	var areas []string
	if err := json.Unmarshal([]byte(raw), &areas); err != nil {
		return fmt.Errorf("node %s: bad areas: %w", e.NodeID, err)
	}
	for _, a := range p.Touched {
		if a != contracts.SharedArea && !slices.Contains(areas, a) {
			areas = append(areas, a)
		}
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE nodes SET areas = ?, updated_at = ? WHERE id = ?`, string(jsonOf(areas)), formatTS(e.TS), e.NodeID)
	return err
}

// projectPROpened keeps the PR milestone → main in the cursor of the closing
// flow.
func (t *Tx) projectPROpened(e Event) error {
	var p contracts.PROpened
	if err := decodePayload(e, &p); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `UPDATE workspace SET flow_cursor = ?, updated_at = ?`, string(jsonOf(closingCursor{PR: p})), formatTS(e.TS))
	return err
}

// closingCursor is workspace.flow_cursor during the closing.
type closingCursor struct {
	PR contracts.PROpened `json:"pr"`
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
		for _, table := range []string{"gate_runs", "sessions", "edges", "nodes", "workspace"} {
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
