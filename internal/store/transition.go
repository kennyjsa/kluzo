package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

// jsonOf marshals a payload made of plain strings and numbers, which cannot fail.
func jsonOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("store: marshal %T: %v", v, err)) // impossible invariant
	}
	return b
}

// TransitionNode moves a node through core's state machine and journals the
// move. The node is loaded, core.NextNode decides, and one node.transition
// event (plus its projection and effects) is written. If the machine refuses
// the move (core.ErrInvalidTransition, core.ErrLimitReached), nothing is
// written and the error is returned as is.
func (t *Tx) TransitionNode(nodeID string, ev core.NodeEvent, lim core.Limits, h Header, effects ...EffectSpec) (core.Node, error) {
	cur, err := t.node(nodeID)
	if err != nil {
		return core.Node{}, err
	}
	next, err := core.NextNode(cur, ev, lim)
	if err != nil {
		return cur, fmt.Errorf("node %s: %w", nodeID, err)
	}
	_, err = t.Append(Event{
		Header: h, Kind: contracts.EventNodeTransition, NodeID: nodeID,
		Payload: jsonOf(contracts.NodeTransition{
			From: string(cur.State), To: string(next.State), Reason: string(ev.Trigger),
			Attempt: next.Attempt, Round: next.Round, BlockedReason: string(next.BlockedReason),
		}),
	}, effects...)
	if err != nil {
		return cur, err
	}
	return next, nil
}

// TransitionWorkspace moves the workspace through core's state machine and
// journals the move; a refused move writes nothing.
func (t *Tx) TransitionWorkspace(trigger core.WorkspaceTrigger, h Header, effects ...EffectSpec) (core.WorkspaceState, error) {
	cur, err := t.WorkspaceState()
	if err != nil {
		return "", err
	}
	next, err := core.NextWorkspace(cur, trigger)
	if err != nil {
		return cur, fmt.Errorf("workspace: %w", err)
	}
	_, err = t.Append(Event{
		Header: h, Kind: contracts.EventWorkspaceTransition,
		Payload: jsonOf(contracts.WorkspaceTransition{From: string(cur), To: string(next), Reason: string(trigger)}),
	}, effects...)
	if err != nil {
		return cur, err
	}
	return next, nil
}

// NodeSlot is a node with its issue and state, as a transaction sees it.
type NodeSlot struct {
	ID, Issue string
	State     core.NodeState
}

// NodeSlots lists the nodes of the workspace, as this transaction sees them.
func (t *Tx) NodeSlots() ([]NodeSlot, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id, issue_ref, state FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NodeSlot
	for rows.Next() {
		var n NodeSlot
		var state string
		if err := rows.Scan(&n.ID, &n.Issue, &state); err != nil {
			return nil, err
		}
		n.State = core.NodeState(state)
		out = append(out, n)
	}
	return out, rows.Err()
}

// Node loads the part of a node row the state machine needs, as this
// transaction sees it.
func (t *Tx) Node(id string) (core.Node, error) { return t.node(id) }

// node loads the part of a node row the state machine needs.
func (t *Tx) node(id string) (core.Node, error) {
	var n core.Node
	var state string
	var prev, reason sql.NullString
	err := t.tx.QueryRowContext(t.ctx, `SELECT state, prev_state, blocked_reason, attempt, round FROM nodes WHERE id = ?`, id).
		Scan(&state, &prev, &reason, &n.Attempt, &n.Round)
	if errors.Is(err, sql.ErrNoRows) {
		return n, fmt.Errorf("%w: node %s", ErrNotFound, id)
	}
	if err != nil {
		return n, err
	}
	n.State, n.PrevState, n.BlockedReason = core.NodeState(state), core.NodeState(prev.String), core.BlockedReason(reason.String)
	return n, nil
}

// WorkspaceState is the state of the workspace.
func (t *Tx) WorkspaceState() (core.WorkspaceState, error) {
	var state string
	err := t.tx.QueryRowContext(t.ctx, `SELECT state FROM workspace`).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return core.WorkspaceNone, fmt.Errorf("%w: workspace", ErrNotFound)
	}
	return core.WorkspaceState(state), err
}

// milestoneQuery reads the state of the last git.milestone effect, pending
// when there is none yet.
const milestoneQuery = `SELECT COALESCE((SELECT state FROM effects WHERE kind = '` + string(core.EffectEnsureMilestone) + `' ORDER BY event_seq DESC, id DESC LIMIT 1), '` + string(core.MilestonePending) + `')`

// MilestoneState is the state of the last git.milestone effect of the
// workspace.
func (t *Tx) MilestoneState() (core.MilestoneState, error) {
	var m string
	if err := t.tx.QueryRowContext(t.ctx, milestoneQuery).Scan(&m); err != nil {
		return "", fmt.Errorf("read the milestone effect: %w", err)
	}
	return core.MilestoneState(m), nil
}
