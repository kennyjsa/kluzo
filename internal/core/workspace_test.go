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
		{WorkspaceApproved, TriggerMilestoneFailed, WorkspacePaused, false},
		{WorkspaceRunning, TriggerMilestoneFailed, WorkspacePaused, false},
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
		{WorkspaceRefining, TriggerMilestoneFailed, WorkspaceRefining, true},
		{WorkspacePaused, TriggerMilestoneFailed, WorkspacePaused, true},
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
		from, to  WorkspaceState
		trigger   WorkspaceTrigger
		milestone MilestoneState
		want      EffectKind
	}{
		{WorkspaceNone, WorkspaceAwaitingApproval, TriggerCreateWithPlan, MilestonePending, EffectEnsureMilestone},
		{WorkspaceNone, WorkspaceRefining, TriggerCreate, MilestonePending, EffectEnsureMilestone},
		{WorkspaceRunning, WorkspaceClosing, TriggerAllIntegrated, MilestoneDone, EffectOpenPR},
		{WorkspaceReadyToMerge, WorkspaceMerged, TriggerMergedToMain, MilestoneDone, EffectCleanupWorkspace},
		{WorkspaceRunning, WorkspaceCancelled, TriggerCancelAll, MilestoneDone, EffectCleanupWorkspace},
		{WorkspaceApproved, WorkspacePaused, TriggerMilestoneFailed, MilestoneFailed, EffectNotify},
		{WorkspaceRunning, WorkspacePaused, TriggerMilestoneFailed, MilestoneFailed, EffectNotify},
		// resume queues the milestone branch again only after it failed.
		{WorkspacePaused, WorkspaceRunning, TriggerResume, MilestoneFailed, EffectEnsureMilestone},
		{WorkspacePaused, WorkspaceRunning, TriggerResume, MilestoneDone, ""},
		{WorkspacePaused, WorkspaceRunning, TriggerResume, MilestonePending, ""},
		{WorkspaceApproved, WorkspaceRunning, TriggerStart, MilestoneDone, ""},
		{WorkspaceRunning, WorkspacePaused, TriggerPause, MilestoneDone, ""},
		{WorkspaceClosing, WorkspaceReadyToMerge, TriggerClosingDone, MilestoneDone, ""},
		{WorkspaceMerged, WorkspaceArchived, TriggerArchive, MilestoneDone, ""},
	} {
		got := WorkspaceEffects(WorkspaceMove{From: tt.from, To: tt.to, Trigger: tt.trigger, Milestone: tt.milestone})
		if (tt.want == "") != (len(got) == 0) || (len(got) > 0 && (len(got) != 1 || got[0].Kind != tt.want)) {
			t.Errorf("%q → %q (%s, milestone %s): %+v, want %q", tt.from, tt.to, tt.trigger, tt.milestone, got, tt.want)
		}
	}
}

// The milestone branch effect gates the workspace (state-machine.md): an
// approved workspace starts once it is done and pauses when it failed; a
// running one pauses when it failed again after a resume; nothing is
// dispatched until it is done.
func TestMilestoneGate(t *testing.T) {
	for _, tt := range []struct {
		ws       WorkspaceState
		m        MilestoneState
		trigger  WorkspaceTrigger // "": none
		dispatch bool
	}{
		{WorkspaceApproved, MilestonePending, "", false},
		{WorkspaceApproved, MilestoneDone, TriggerStart, false},
		{WorkspaceApproved, MilestoneFailed, TriggerMilestoneFailed, false},
		{WorkspaceRunning, MilestonePending, "", false},
		{WorkspaceRunning, MilestoneDone, "", true},
		{WorkspaceRunning, MilestoneFailed, TriggerMilestoneFailed, false},
		{WorkspacePaused, MilestoneFailed, "", false},
		{WorkspacePaused, MilestoneDone, "", false},
		{WorkspaceRefining, MilestoneFailed, "", false},
		{WorkspaceAwaitingApproval, MilestoneDone, "", false},
		{WorkspaceClosing, MilestoneDone, "", false},
	} {
		got, ok := MilestoneTrigger(tt.ws, tt.m)
		if got != tt.trigger || ok != (tt.trigger != "") {
			t.Errorf("%s, milestone %s: trigger %q %v, want %q", tt.ws, tt.m, got, ok, tt.trigger)
		}
		if d := Dispatching(tt.ws, tt.m); d != tt.dispatch {
			t.Errorf("%s, milestone %s: dispatching %v", tt.ws, tt.m, d)
		}
		if ok {
			if _, err := NextWorkspace(tt.ws, got); err != nil {
				t.Errorf("%s + %s: %v", tt.ws, got, err)
			}
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
