package core

import "fmt"

// NodeState is the state of a node (docs/contracts/state-machine.md#nó).
type NodeState string

// Node states.
const (
	NodePlanned     NodeState = "planned"
	NodeReady       NodeState = "ready"
	NodeRunning     NodeState = "running"
	NodeInReview    NodeState = "in_review"
	NodeGating      NodeState = "gating"
	NodeQueued      NodeState = "queued"
	NodeIntegrating NodeState = "integrating"
	NodeIntegrated  NodeState = "integrated"
	NodeBlocked     NodeState = "blocked"
	NodeFailed      NodeState = "failed"
	NodeCancelled   NodeState = "cancelled"
)

// NodeStates lists every node state, in lifecycle order.
var NodeStates = []NodeState{
	NodePlanned, NodeReady, NodeRunning, NodeInReview, NodeGating, NodeQueued,
	NodeIntegrating, NodeIntegrated, NodeBlocked, NodeFailed, NodeCancelled,
}

// Final reports whether no further transition leaves the state.
func (s NodeState) Final() bool {
	return s == NodeIntegrated || s == NodeFailed || s == NodeCancelled
}

// Label is the tracker label mirroring the state (one orch/* label per issue).
func (s NodeState) Label() string {
	switch s {
	case NodeInReview:
		return "orch/review"
	case NodeGating:
		return "orch/gates"
	case NodeIntegrating:
		return "orch/queued"
	default:
		return "orch/" + string(s)
	}
}

// BlockedReason explains why a node is blocked.
type BlockedReason string

// Blocked reasons.
const (
	BlockedQuestion         BlockedReason = "question"
	BlockedProposal         BlockedReason = "proposal"
	BlockedBudget           BlockedReason = "budget"
	BlockedTriage           BlockedReason = "triage"
	BlockedDependencyFailed BlockedReason = "dependency_failed"
	BlockedPaused           BlockedReason = "paused"
)

// NodeTrigger is something that happened to a node.
type NodeTrigger string

// Node triggers, one per row of the node transition table.
const (
	TriggerDepsSatisfied     NodeTrigger = "deps_satisfied"
	TriggerDispatched        NodeTrigger = "dispatched"
	TriggerImplementerDone   NodeTrigger = "implementer_done"
	TriggerReviewRejected    NodeTrigger = "review_rejected"
	TriggerReviewApproved    NodeTrigger = "review_approved"
	TriggerGateFailed        NodeTrigger = "gate_failed"
	TriggerGatesPassed       NodeTrigger = "gates_passed"
	TriggerQueueTurn         NodeTrigger = "queue_turn"
	TriggerMerged            NodeTrigger = "merged"
	TriggerIntegrationFailed NodeTrigger = "integration_failed"
	TriggerBlock             NodeTrigger = "block"
	TriggerUnblock           NodeTrigger = "unblock"
	TriggerFail              NodeTrigger = "fail"
	TriggerCancel            NodeTrigger = "cancel"
)

// Node is the part of a node's state the machine reasons about.
type Node struct {
	State         NodeState
	PrevState     NodeState     // state to return to when unblocked
	BlockedReason BlockedReason // set while State == NodeBlocked
	Attempt       int           // passes through the node flow; starts at 1 when dispatched
	Round         int           // review rounds within the current attempt
	// FastGateFailures counts the failures of the fast gates in the current
	// attempt; they do not use review rounds (ADR 0023, item 7).
	FastGateFailures int
}

// Limits bounds retries for a node.
type Limits struct {
	MaxAttempts     int // attempts per node (budget.attempts)
	MaxReviewRounds int // review rounds per attempt (flow step max_rounds)
	MaxFastRetries  int // fast gate failures per attempt (max_retries of the gate step, default 3)
}

// NodeEvent is a trigger plus its payload.
type NodeEvent struct {
	Trigger NodeTrigger
	Reason  BlockedReason // only for TriggerBlock
}

// nodeEdge is one allowed transition.
type nodeEdge struct {
	from    NodeState
	trigger NodeTrigger
	to      NodeState
}

// nodeTable mirrors "Transições do nó" in docs/contracts/state-machine.md.
// Rows that apply to "any non-final state" are handled in NextNode.
var nodeTable = []nodeEdge{
	{NodePlanned, TriggerDepsSatisfied, NodeReady},
	{NodeReady, TriggerDispatched, NodeRunning},
	{NodeRunning, TriggerImplementerDone, NodeInReview},
	{NodeInReview, TriggerReviewRejected, NodeRunning},
	{NodeInReview, TriggerReviewApproved, NodeGating},
	{NodeGating, TriggerGateFailed, NodeRunning},
	{NodeGating, TriggerGatesPassed, NodeQueued},
	{NodeQueued, TriggerQueueTurn, NodeIntegrating},
	{NodeIntegrating, TriggerMerged, NodeIntegrated},
	{NodeIntegrating, TriggerIntegrationFailed, NodeRunning},
}

// NextNode applies ev to n and returns the resulting node.
// It returns ErrInvalidTransition when ev is not allowed from n.State, and
// ErrLimitReached when a retry limit forbids the move (the node is unchanged).
func NextNode(n Node, ev NodeEvent, lim Limits) (Node, error) {
	if n.State.Final() {
		return n, invalid(n.State, ev.Trigger)
	}
	switch ev.Trigger {
	case TriggerBlock:
		if n.State == NodeBlocked {
			return n, invalid(n.State, ev.Trigger)
		}
		if ev.Reason == "" {
			return n, fmt.Errorf("%w: block requires a reason", ErrInvalidTransition)
		}
		n.PrevState, n.State, n.BlockedReason = n.State, NodeBlocked, ev.Reason
		return n, nil
	case TriggerUnblock:
		if n.State != NodeBlocked {
			return n, invalid(n.State, ev.Trigger)
		}
		n.State, n.PrevState, n.BlockedReason = n.PrevState, "", ""
		return n, nil
	case TriggerFail:
		n.State, n.PrevState, n.BlockedReason = NodeFailed, "", ""
		return n, nil
	case TriggerCancel:
		n.State, n.PrevState, n.BlockedReason = NodeCancelled, "", ""
		return n, nil
	}

	to, ok := lookupNode(n.State, ev.Trigger)
	if !ok {
		return n, invalid(n.State, ev.Trigger)
	}
	switch {
	case to == NodeRunning && n.State == NodeInReview:
		// A review rejection is a new round within the same attempt.
		if n.Round+1 >= lim.MaxReviewRounds {
			return n, fmt.Errorf("%w: %d review rounds", ErrLimitReached, lim.MaxReviewRounds)
		}
		n.Round++
	case to == NodeRunning:
		// Dispatch, gate failure or integration failure starts a new attempt.
		if n.State != NodeReady && n.Attempt >= lim.MaxAttempts {
			return n, fmt.Errorf("%w: %d attempts", ErrLimitReached, lim.MaxAttempts)
		}
		n.Attempt++
		n.Round, n.FastGateFailures = 0, 0
	}
	n.State = to
	return n, nil
}

// FastGateFailed counts a failure of the fast gates, which run while the node
// is running, between the implementer and the reviewer
// (docs/contracts/state-machine.md). Below max_retries the node stays running
// and the implementer gets the failure in the same session and attempt; the
// failure that passes the limit blocks the node for triage.
func FastGateFailed(n Node, lim Limits) (Node, error) {
	if n.State != NodeRunning {
		return n, invalid(n.State, NodeTrigger("fast_gate_failed"))
	}
	n.FastGateFailures++
	if n.FastGateFailures <= lim.MaxFastRetries {
		return n, nil
	}
	return NextNode(n, NodeEvent{Trigger: TriggerBlock, Reason: BlockedTriage}, lim)
}

func lookupNode(from NodeState, t NodeTrigger) (NodeState, bool) {
	for _, e := range nodeTable {
		if e.from == from && e.trigger == t {
			return e.to, true
		}
	}
	return "", false
}

func invalid[S ~string, T ~string](from S, t T) error {
	return fmt.Errorf("%w: %s from %s", ErrInvalidTransition, t, from)
}
