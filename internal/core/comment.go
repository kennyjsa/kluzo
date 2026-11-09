package core

import (
	"fmt"
	"strings"
)

// commentReasons are the words of each trigger in the transition comment.
var commentReasons = map[NodeTrigger]string{
	TriggerDepsSatisfied:     "dependencies integrated",
	TriggerDispatched:        "dispatched",
	TriggerImplementerDone:   "implementer delivered",
	TriggerReviewRejected:    "review requested changes",
	TriggerReviewApproved:    "review approved",
	TriggerGateFailed:        "a gate failed",
	TriggerGatesPassed:       "gates passed",
	TriggerQueueTurn:         "integration started",
	TriggerMerged:            "merged into the milestone",
	TriggerIntegrationFailed: "integration failed",
	TriggerUnblock:           "unblocked",
	TriggerFail:              "failed",
	TriggerCancel:            "cancelled",
}

// TransitionComment is the comment posted on the issue for a transition
// (docs/architecture/integrations.md): one short line in English, and for
// blocks and failures a second line with the summary, without logs.
func TransitionComment(from NodeState, to Node, ev NodeEvent, detail string) string {
	reason := commentReasons[ev.Trigger]
	if ev.Trigger == TriggerBlock {
		reason = "blocked: " + string(ev.Reason)
	}
	line := fmt.Sprintf("**Kluzo** · `%s` → `%s` · %s (attempt %d, round %d)", from, to.State, reason, to.Attempt, to.Round)
	if detail = strings.TrimSpace(detail); detail != "" && (to.State == NodeBlocked || to.State == NodeFailed) {
		line += "\n\n" + strings.Join(strings.Fields(detail), " ")
	}
	return line
}
