package core

import (
	"errors"
	"testing"
)

var testLimits = Limits{MaxAttempts: 3, MaxReviewRounds: 2, MaxFastRetries: 3}

func TestNextNode(t *testing.T) {
	tests := []struct {
		name    string
		from    Node
		ev      NodeEvent
		want    Node
		wantErr error
	}{
		{"deps satisfied", Node{State: NodePlanned}, NodeEvent{Trigger: TriggerDepsSatisfied}, Node{State: NodeReady}, nil},
		{"dispatch starts attempt 1", Node{State: NodeReady}, NodeEvent{Trigger: TriggerDispatched}, Node{State: NodeRunning, Attempt: 1}, nil},
		{"implementer done", Node{State: NodeRunning, Attempt: 1}, NodeEvent{Trigger: TriggerImplementerDone}, Node{State: NodeInReview, Attempt: 1}, nil},
		{"review rejected is a new round", Node{State: NodeInReview, Attempt: 1}, NodeEvent{Trigger: TriggerReviewRejected}, Node{State: NodeRunning, Attempt: 1, Round: 1}, nil},
		{"review rejected beyond max rounds", Node{State: NodeInReview, Attempt: 1, Round: 1}, NodeEvent{Trigger: TriggerReviewRejected}, Node{State: NodeInReview, Attempt: 1, Round: 1}, ErrLimitReached},
		{"review approved", Node{State: NodeInReview, Attempt: 1, Round: 1}, NodeEvent{Trigger: TriggerReviewApproved}, Node{State: NodeGating, Attempt: 1, Round: 1}, nil},
		{"gate failed is a new attempt", Node{State: NodeGating, Attempt: 1, Round: 1, FastGateFailures: 2}, NodeEvent{Trigger: TriggerGateFailed}, Node{State: NodeRunning, Attempt: 2}, nil},
		{"gate failed without attempts left", Node{State: NodeGating, Attempt: 3}, NodeEvent{Trigger: TriggerGateFailed}, Node{State: NodeGating, Attempt: 3}, ErrLimitReached},
		{"gates passed", Node{State: NodeGating, Attempt: 1}, NodeEvent{Trigger: TriggerGatesPassed}, Node{State: NodeQueued, Attempt: 1}, nil},
		{"queue turn", Node{State: NodeQueued, Attempt: 1}, NodeEvent{Trigger: TriggerQueueTurn}, Node{State: NodeIntegrating, Attempt: 1}, nil},
		{"merged", Node{State: NodeIntegrating, Attempt: 1}, NodeEvent{Trigger: TriggerMerged}, Node{State: NodeIntegrated, Attempt: 1}, nil},
		{"integration failed is a new attempt", Node{State: NodeIntegrating, Attempt: 1}, NodeEvent{Trigger: TriggerIntegrationFailed}, Node{State: NodeRunning, Attempt: 2}, nil},
		{"integration failed without attempts left", Node{State: NodeIntegrating, Attempt: 3}, NodeEvent{Trigger: TriggerIntegrationFailed}, Node{State: NodeIntegrating, Attempt: 3}, ErrLimitReached},
		{"block keeps previous state", Node{State: NodeInReview, Attempt: 1}, NodeEvent{Trigger: TriggerBlock, Reason: BlockedQuestion}, Node{State: NodeBlocked, PrevState: NodeInReview, BlockedReason: BlockedQuestion, Attempt: 1}, nil},
		{"block needs a reason", Node{State: NodeRunning}, NodeEvent{Trigger: TriggerBlock}, Node{State: NodeRunning}, ErrInvalidTransition},
		{"block twice", Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedBudget}, NodeEvent{Trigger: TriggerBlock, Reason: BlockedQuestion}, Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedBudget}, ErrInvalidTransition},
		{"unblock returns to previous", Node{State: NodeBlocked, PrevState: NodeInReview, BlockedReason: BlockedQuestion, Attempt: 1}, NodeEvent{Trigger: TriggerUnblock}, Node{State: NodeInReview, Attempt: 1}, nil},
		{"unblock when not blocked", Node{State: NodeRunning}, NodeEvent{Trigger: TriggerUnblock}, Node{State: NodeRunning}, ErrInvalidTransition},
		{"fail from blocked", Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage}, NodeEvent{Trigger: TriggerFail}, Node{State: NodeFailed}, nil},
		{"cancel from blocked", Node{State: NodeBlocked, PrevState: NodeReady, BlockedReason: BlockedQuestion}, NodeEvent{Trigger: TriggerCancel}, Node{State: NodeCancelled}, nil},
		{"cancel from planned", Node{State: NodePlanned}, NodeEvent{Trigger: TriggerCancel}, Node{State: NodeCancelled}, nil},
		{"integrated is final", Node{State: NodeIntegrated}, NodeEvent{Trigger: TriggerCancel}, Node{State: NodeIntegrated}, ErrInvalidTransition},
		{"failed is final", Node{State: NodeFailed}, NodeEvent{Trigger: TriggerUnblock}, Node{State: NodeFailed}, ErrInvalidTransition},
		{"no skipping review", Node{State: NodeRunning}, NodeEvent{Trigger: TriggerGatesPassed}, Node{State: NodeRunning}, ErrInvalidTransition},
		{"retry is a new attempt", Node{State: NodeBlocked, PrevState: NodeInReview, BlockedReason: BlockedTriage, Attempt: 1, Round: 1, FastGateFailures: 2}, NodeEvent{Trigger: TriggerRetry}, Node{State: NodeRunning, Attempt: 2}, nil},
		{"retry without attempts left", Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage, Attempt: 3}, NodeEvent{Trigger: TriggerRetry}, Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage, Attempt: 3}, ErrLimitReached},
		{"retry of a node never dispatched", Node{State: NodeBlocked, PrevState: NodePlanned, BlockedReason: BlockedDependencyFailed}, NodeEvent{Trigger: TriggerRetry}, Node{State: NodeBlocked, PrevState: NodePlanned, BlockedReason: BlockedDependencyFailed}, ErrInvalidTransition},
		{"retry when not blocked", Node{State: NodeRunning, Attempt: 1}, NodeEvent{Trigger: TriggerRetry}, Node{State: NodeRunning, Attempt: 1}, ErrInvalidTransition},
		{"planned cannot be dispatched", Node{State: NodePlanned}, NodeEvent{Trigger: TriggerDispatched}, Node{State: NodePlanned}, ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextNode(tt.from, tt.ev, testLimits)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("node = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFastGateFailed(t *testing.T) {
	tests := []struct {
		name    string
		from    Node
		want    Node
		wantErr error
	}{
		{"first failure goes back to the implementer", Node{State: NodeRunning, Attempt: 1}, Node{State: NodeRunning, Attempt: 1, FastGateFailures: 1}, nil},
		{"the review rounds are not used", Node{State: NodeRunning, Attempt: 1, Round: 1, FastGateFailures: 1}, Node{State: NodeRunning, Attempt: 1, Round: 1, FastGateFailures: 2}, nil},
		{"the last allowed failure", Node{State: NodeRunning, Attempt: 2, FastGateFailures: 2}, Node{State: NodeRunning, Attempt: 2, FastGateFailures: 3}, nil},
		{"past max_retries blocks for triage", Node{State: NodeRunning, Attempt: 2, FastGateFailures: 3}, Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage, Attempt: 2, FastGateFailures: 4}, nil},
		{"only while running", Node{State: NodeInReview, Attempt: 1}, Node{State: NodeInReview, Attempt: 1}, ErrInvalidTransition},
		{"not when blocked", Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage}, Node{State: NodeBlocked, PrevState: NodeRunning, BlockedReason: BlockedTriage}, ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FastGateFailed(tt.from, testLimits)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("node = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestNodeHappyPath walks a node from planned to integrated, with one review
// round and one gate failure, as the downstream flow does.
func TestNodeHappyPath(t *testing.T) {
	n := Node{State: NodePlanned}
	steps := []NodeTrigger{
		TriggerDepsSatisfied, TriggerDispatched, TriggerImplementerDone,
		TriggerReviewRejected, TriggerImplementerDone, TriggerReviewApproved,
		TriggerGateFailed, TriggerImplementerDone, TriggerReviewApproved,
		TriggerGatesPassed, TriggerQueueTurn, TriggerMerged,
	}
	for _, s := range steps {
		var err error
		if n, err = NextNode(n, NodeEvent{Trigger: s}, testLimits); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if n.State != NodeIntegrated || n.Attempt != 2 || n.Round != 0 {
		t.Fatalf("final node = %+v, want integrated at attempt 2", n)
	}
}

func TestNodeLabels(t *testing.T) {
	want := map[NodeState]string{
		NodePlanned: "orch/planned", NodeReady: "orch/ready", NodeRunning: "orch/running",
		NodeInReview: "orch/review", NodeGating: "orch/gates", NodeQueued: "orch/queued",
		NodeIntegrating: "orch/queued", NodeIntegrated: "orch/integrated", NodeBlocked: "orch/blocked",
		NodeFailed: "orch/failed", NodeCancelled: "orch/cancelled",
	}
	for _, s := range NodeStates {
		if got := s.Label(); got != want[s] {
			t.Errorf("%s.Label() = %q, want %q", s, got, want[s])
		}
	}
}
