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
