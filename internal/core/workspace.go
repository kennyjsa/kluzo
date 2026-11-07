package core

// WorkspaceState is the state of a workspace, i.e. of a milestone being
// delivered (docs/contracts/state-machine.md#workspace).
type WorkspaceState string

// Workspace states. WorkspaceNone is the virtual state before creation.
const (
	WorkspaceNone             WorkspaceState = ""
	WorkspaceRefining         WorkspaceState = "refining"
	WorkspaceAwaitingApproval WorkspaceState = "awaiting_approval"
	WorkspaceApproved         WorkspaceState = "approved"
	WorkspaceRunning          WorkspaceState = "running"
	WorkspacePaused           WorkspaceState = "paused"
	WorkspaceClosing          WorkspaceState = "closing"
	WorkspaceReadyToMerge     WorkspaceState = "ready_to_merge"
	WorkspaceMerged           WorkspaceState = "merged"
	WorkspaceArchived         WorkspaceState = "archived"
	WorkspaceCancelled        WorkspaceState = "cancelled"
	WorkspaceFailed           WorkspaceState = "failed"
)

// WorkspaceStates lists every real workspace state, in lifecycle order.
var WorkspaceStates = []WorkspaceState{
	WorkspaceRefining, WorkspaceAwaitingApproval, WorkspaceApproved, WorkspaceRunning,
	WorkspacePaused, WorkspaceClosing, WorkspaceReadyToMerge, WorkspaceMerged,
	WorkspaceArchived, WorkspaceCancelled, WorkspaceFailed,
}

// Active reports whether the workspace can still be cancelled or fail.
func (s WorkspaceState) Active() bool {
	switch s {
	case WorkspaceNone, WorkspaceMerged, WorkspaceArchived, WorkspaceCancelled, WorkspaceFailed:
		return false
	}
	return true
}

// WorkspaceTrigger is something that happened to a workspace.
type WorkspaceTrigger string

// Workspace triggers, one per row of the workspace transition table.
const (
	TriggerCreate         WorkspaceTrigger = "create"
	TriggerCreateWithPlan WorkspaceTrigger = "create_with_plan"
	TriggerPlanProposed   WorkspaceTrigger = "plan_proposed"
	TriggerPlanRejected   WorkspaceTrigger = "plan_rejected"
	TriggerPlanApproved   WorkspaceTrigger = "plan_approved"
	TriggerStart          WorkspaceTrigger = "start"
	TriggerPause          WorkspaceTrigger = "pause"
	TriggerResume         WorkspaceTrigger = "resume"
	TriggerAllIntegrated  WorkspaceTrigger = "all_integrated"
	TriggerClosingDone    WorkspaceTrigger = "closing_done"
	TriggerE2EFailed      WorkspaceTrigger = "e2e_failed"
	TriggerMergedToMain   WorkspaceTrigger = "merged_to_main"
	TriggerArchive        WorkspaceTrigger = "archive"
	TriggerCancelAll      WorkspaceTrigger = "cancel"
	TriggerFatal          WorkspaceTrigger = "fatal"
)

type workspaceEdge struct {
	from    WorkspaceState
	trigger WorkspaceTrigger
	to      WorkspaceState
}

// workspaceTable mirrors "Workspace" in docs/contracts/state-machine.md.
// Rows for "any active state" are handled in NextWorkspace.
var workspaceTable = []workspaceEdge{
	{WorkspaceNone, TriggerCreate, WorkspaceRefining},
	{WorkspaceNone, TriggerCreateWithPlan, WorkspaceAwaitingApproval},
	{WorkspaceRefining, TriggerPlanProposed, WorkspaceAwaitingApproval},
	{WorkspaceAwaitingApproval, TriggerPlanRejected, WorkspaceRefining},
	{WorkspaceAwaitingApproval, TriggerPlanApproved, WorkspaceApproved},
	{WorkspaceApproved, TriggerStart, WorkspaceRunning},
	{WorkspaceRunning, TriggerPause, WorkspacePaused},
	{WorkspacePaused, TriggerResume, WorkspaceRunning},
	{WorkspaceRunning, TriggerAllIntegrated, WorkspaceClosing},
	{WorkspaceClosing, TriggerClosingDone, WorkspaceReadyToMerge},
	{WorkspaceClosing, TriggerE2EFailed, WorkspaceRunning},
	{WorkspaceReadyToMerge, TriggerMergedToMain, WorkspaceMerged},
	{WorkspaceMerged, TriggerArchive, WorkspaceArchived},
}

// NextWorkspace returns the state after trigger t, or ErrInvalidTransition.
func NextWorkspace(from WorkspaceState, t WorkspaceTrigger) (WorkspaceState, error) {
	switch t {
	case TriggerCancelAll:
		if from.Active() {
			return WorkspaceCancelled, nil
		}
		return from, invalid(from, t)
	case TriggerFatal:
		if from.Active() {
			return WorkspaceFailed, nil
		}
		return from, invalid(from, t)
	}
	for _, e := range workspaceTable {
		if e.from == from && e.trigger == t {
			return e.to, nil
		}
	}
	return from, invalid(from, t)
}
