package core

import (
	"errors"
	"testing"
)

func TestNextWorkspace(t *testing.T) {
	tests := []struct {
		from    WorkspaceState
		trigger WorkspaceTrigger
		want    WorkspaceState
		wantErr bool
	}{
		{WorkspaceNone, TriggerCreate, WorkspaceRefining, false},
		{WorkspaceNone, TriggerCreateWithPlan, WorkspaceAwaitingApproval, false},
		{WorkspaceRefining, TriggerPlanProposed, WorkspaceAwaitingApproval, false},
		{WorkspaceAwaitingApproval, TriggerPlanRejected, WorkspaceRefining, false},
		{WorkspaceAwaitingApproval, TriggerPlanApproved, WorkspaceApproved, false},
		{WorkspaceApproved, TriggerStart, WorkspaceRunning, false},
		{WorkspaceRunning, TriggerPause, WorkspacePaused, false},
		{WorkspacePaused, TriggerResume, WorkspaceRunning, false},
		{WorkspaceRunning, TriggerAllIntegrated, WorkspaceClosing, false},
		{WorkspaceClosing, TriggerClosingDone, WorkspaceReadyToMerge, false},
		{WorkspaceClosing, TriggerE2EFailed, WorkspaceRunning, false},
		{WorkspaceReadyToMerge, TriggerMergedToMain, WorkspaceMerged, false},
		{WorkspaceMerged, TriggerArchive, WorkspaceArchived, false},
		{WorkspacePaused, TriggerCancelAll, WorkspaceCancelled, false},
		{WorkspaceClosing, TriggerFatal, WorkspaceFailed, false},
		// Forbidden moves.
		{WorkspaceRefining, TriggerPlanApproved, WorkspaceRefining, true},
		{WorkspaceApproved, TriggerPause, WorkspaceApproved, true},
		{WorkspaceMerged, TriggerCancelAll, WorkspaceMerged, true},
		{WorkspaceCancelled, TriggerFatal, WorkspaceCancelled, true},
		{WorkspaceNone, TriggerCancelAll, WorkspaceNone, true},
	}
	for _, tt := range tests {
		got, err := NextWorkspace(tt.from, tt.trigger)
		if (err != nil) != tt.wantErr || (err != nil && !errors.Is(err, ErrInvalidTransition)) {
			t.Errorf("%q + %s: err = %v, wantErr %v", tt.from, tt.trigger, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("%q + %s = %q, want %q", tt.from, tt.trigger, got, tt.want)
		}
	}
}

func TestWorkspaceEffects(t *testing.T) {
	for _, tt := range []struct {
		from, to WorkspaceState
		want     EffectKind
	}{
		{WorkspaceNone, WorkspaceAwaitingApproval, EffectEnsureMilestone},
		{WorkspaceNone, WorkspaceRefining, EffectEnsureMilestone},
		{WorkspaceRunning, WorkspaceClosing, EffectOpenPR},
		{WorkspaceReadyToMerge, WorkspaceMerged, EffectCleanupWorkspace},
		{WorkspaceRunning, WorkspaceCancelled, EffectCleanupWorkspace},
		{WorkspaceRunning, WorkspacePaused, ""},
		{WorkspaceClosing, WorkspaceReadyToMerge, ""},
		{WorkspaceMerged, WorkspaceArchived, ""},
	} {
		got := WorkspaceEffects(tt.from, tt.to)
		if (tt.want == "") != (len(got) == 0) || (len(got) > 0 && (len(got) != 1 || got[0].Kind != tt.want)) {
			t.Errorf("%q → %q: %+v, want %q", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestAllIntegrated(t *testing.T) {
	for _, tt := range []struct {
		nodes []NodeState
		want  bool
	}{
		{nil, false},
		{[]NodeState{NodeIntegrated}, true},
		{[]NodeState{NodeIntegrated, NodeCancelled}, true},
		{[]NodeState{NodeCancelled}, false},
		{[]NodeState{NodeIntegrated, NodeQueued}, false},
		{[]NodeState{NodeIntegrated, NodeFailed}, false},
	} {
		if got := AllIntegrated(tt.nodes); got != tt.want {
			t.Errorf("AllIntegrated(%v) = %v", tt.nodes, got)
		}
	}
}
