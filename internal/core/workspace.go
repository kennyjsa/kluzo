package core

import "github.com/kennyjsa/kluzo/internal/ports"

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
	// TriggerMilestoneFailed: the git.milestone effect failed for good (the
	// local and the remote milestone branch diverged).
	TriggerMilestoneFailed WorkspaceTrigger = "milestone_failed"
	TriggerPause           WorkspaceTrigger = "pause"
	TriggerResume          WorkspaceTrigger = "resume"
	TriggerAllIntegrated   WorkspaceTrigger = "all_integrated"
	TriggerClosingDone     WorkspaceTrigger = "closing_done"
	TriggerE2EFailed       WorkspaceTrigger = "e2e_failed"
	TriggerMergedToMain    WorkspaceTrigger = "merged_to_main"
	TriggerArchive         WorkspaceTrigger = "archive"
	TriggerCancelAll       WorkspaceTrigger = "cancel"
	TriggerFatal           WorkspaceTrigger = "fatal"
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
	{WorkspaceApproved, TriggerMilestoneFailed, WorkspacePaused},
	{WorkspaceRunning, TriggerMilestoneFailed, WorkspacePaused},
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

// Effects of the workspace (docs/contracts/state-machine.md#workspace).
const (
	EffectEnsureMilestone  EffectKind = "git.milestone"
	EffectOpenPR           EffectKind = "scm.open_pr"
	EffectCleanupWorkspace EffectKind = "cleanup.workspace"
)

// MilestoneState is the state of the last git.milestone effect of the
// workspace: the scheduler dispatches nothing until it is done.
type MilestoneState string

// States of the milestone branch effect (those of the outbox).
const (
	MilestonePending MilestoneState = "pending"
	MilestoneDone    MilestoneState = "done"
	MilestoneFailed  MilestoneState = "failed"
)

// MilestoneTrigger is what the milestone branch effect means for the
// workspace (docs/contracts/state-machine.md): an approved workspace starts
// once it is done; an approved or running workspace whose effect failed for
// good is paused, for a person to reconcile the branch and resume.
func MilestoneTrigger(ws WorkspaceState, m MilestoneState) (WorkspaceTrigger, bool) {
	switch {
	case ws == WorkspaceApproved && m == MilestoneDone:
		return TriggerStart, true
	case (ws == WorkspaceApproved || ws == WorkspaceRunning) && m == MilestoneFailed:
		return TriggerMilestoneFailed, true
	}
	return "", false
}

// Dispatching reports whether the scheduler may dispatch work on nodes:
// sessions, gates and the integration only run in a running workspace whose
// milestone branch is ready.
func Dispatching(ws WorkspaceState, m MilestoneState) bool {
	return ws == WorkspaceRunning && m == MilestoneDone
}

// WorkspaceMove is a workspace transition, with the state of the milestone
// branch effect when it happens.
type WorkspaceMove struct {
	From, To  WorkspaceState
	Trigger   WorkspaceTrigger
	Milestone MilestoneState
}

// WorkspaceEffects are the effects of a workspace transition: the creation
// creates or adopts the milestone branch, a resume after it failed queues it
// again, its failure tells a person, the closing opens the PR
// milestone → main (v0.1: no E2E and no report), and a workspace merged or
// cancelled is cleaned up (ADR 0025).
func WorkspaceEffects(m WorkspaceMove) []Effect {
	switch {
	case m.From == WorkspaceNone:
		return []Effect{{Kind: EffectEnsureMilestone, Key: "milestone"}}
	case m.Trigger == TriggerResume && m.Milestone == MilestoneFailed:
		return []Effect{{Kind: EffectEnsureMilestone, Key: "milestone"}}
	case m.Trigger == TriggerMilestoneFailed:
		return []Effect{{Kind: EffectNotify, Key: "notify", Priority: ports.PriorityHigh, Title: "Workspace paused: the milestone branch",
			Body: "The local and the remote milestone branch diverged. Reconcile them, then resume: the branch is checked again before any node is dispatched."}}
	case m.To == WorkspaceClosing:
		return []Effect{{Kind: EffectOpenPR, Key: "pr"}}
	case m.To == WorkspaceMerged, m.To == WorkspaceCancelled:
		return []Effect{{Kind: EffectCleanupWorkspace, Key: "cleanup"}}
	}
	return nil
}

// AllIntegrated reports whether the workspace can close: every node
// integrated or cancelled, and at least one integrated.
func AllIntegrated(nodes []NodeState) bool {
	integrated := false
	for _, s := range nodes {
		switch s {
		case NodeIntegrated:
			integrated = true
		case NodeCancelled:
		default:
			return false
		}
	}
	return integrated
}
