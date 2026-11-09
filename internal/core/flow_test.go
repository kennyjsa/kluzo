package core

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// testFlow is the default node.yaml: implement, quick (fast), review, gates, merge.
var testFlow = Flow{Steps: []FlowStep{
	{ID: "implement", Kind: StepRole, Role: RoleImplementer, Phase: NodeRunning},
	{ID: "quick", Kind: StepGate, Gates: []string{"build"}, Fast: true, MaxRetries: 2},
	{ID: "review", Kind: StepRole, Role: RoleReviewer, Phase: NodeInReview, MaxRounds: 2},
	{ID: "gates", Kind: StepGate, Gates: []string{"test"}},
	{ID: "merge", Kind: StepMergeQueue},
}}

var flowLimits = Limits{MaxAttempts: 2}

func implDone(step int) SessionDone {
	return SessionDone{Step: step, State: ports.StateFinished, Raw: json.RawMessage(`{"status":"done","responses":[]}`),
		Session: &HarnessSession{Ref: "h1"}, Head: "c1", CostUSD: 100}
}

func review(step int, met bool, findings ...map[string]any) SessionDone {
	raw, _ := json.Marshal(map[string]any{"criteria": []map[string]any{{"id": "AC1", "met": met}}, "findings": findings, "previous": []any{}})
	return SessionDone{Step: step, State: ports.StateFinished, Raw: raw, CostUSD: 50}
}

func gates(step int, states ...string) GatesDone {
	g := GatesDone{Step: step}
	for i, s := range states {
		g.Results = append(g.Results, ports.GateResult{Gate: string(rune('a' + i)), State: s})
	}
	return g
}

var blockingFinding = map[string]any{"severity": "blocking", "category": "correctness", "file": "a.go", "line": 1, "message": "bug", "check": "c"}

// play applies the inputs in order to a ready node and returns every decision.
func play(t *testing.T, inputs ...Input) (NodeFlow, []Decision) {
	t.Helper()
	s := NodeFlow{Node: Node{State: NodeReady}}
	var ds []Decision
	for i, in := range inputs {
		d, err := Apply(s, in, testFlow, flowLimits)
		if err != nil {
			t.Fatalf("input %d (%T): %v", i, in, err)
		}
		s, ds = d.Next, append(ds, d)
	}
	return s, ds
}

func kindsOf(d Decision) []EffectKind {
	var out []EffectKind
	for _, e := range d.Effects {
		out = append(out, e.Kind)
	}
	return out
}

func moves(ds []Decision) []NodeState {
	var out []NodeState
	for _, d := range ds {
		for _, m := range d.Moves {
			out = append(out, m.To)
		}
	}
	return out
}

func lastSession(t *testing.T, d Decision) Effect {
	t.Helper()
	for _, e := range d.Effects {
		if e.Kind == EffectSession {
			return e
		}
	}
	t.Fatalf("no session effect in %v", kindsOf(d))
	return Effect{}
}

func TestFlowApprovedInTheFirstRound(t *testing.T) {
	s, ds := play(t, Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true), gates(3, ports.GatePassed))
	if want := []NodeState{NodeRunning, NodeInReview, NodeGating, NodeQueued}; !slices.Equal(moves(ds), want) {
		t.Errorf("moves = %v, want %v", moves(ds), want)
	}
	if !s.Run.Ready || s.Run.Step != 4 || s.Node.State != NodeQueued || s.Run.CostUSD != 150 {
		t.Errorf("end = %+v", s)
	}
	if e := lastSession(t, ds[0]); e.Role != RoleImplementer || e.Resume || !slices.Contains(kindsOf(ds[0]), EffectLabel) || !slices.Contains(kindsOf(ds[0]), EffectComment) {
		t.Errorf("dispatch effects = %+v", ds[0].Effects)
	}
	if k := kindsOf(ds[1]); !slices.Equal(k, []EffectKind{EffectGates}) {
		t.Errorf("after the implementer: %v, want only the fast gates (no move)", k)
	}
	if e := lastSession(t, ds[2]); e.Role != RoleReviewer || e.Step != 2 {
		t.Errorf("after the fast gates: %+v", e)
	}
}

func TestFlowReturnedWithFindings(t *testing.T) {
	s, ds := play(t, Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true, blockingFinding))
	if s.Node.State != NodeRunning || s.Node.Round != 1 || s.Run.Step != 0 || !s.Run.Rejected || s.Run.RoundBase != "c1" {
		t.Fatalf("after the rejection: %+v", s)
	}
	if e := lastSession(t, ds[3]); e.Role != RoleImplementer || !e.Resume {
		t.Errorf("the implementer resumes its session: %+v", e)
	}
	if open := s.Run.Findings.Open(); len(open) != 1 || open[0].ID != "F1" {
		t.Errorf("open = %+v", open)
	}
	if c := ds[3].Effects[1].Body; !strings.Contains(c, "review requested changes (attempt 1, round 1)") {
		t.Errorf("comment = %q", c)
	}
}

func TestFlowContestedFinding(t *testing.T) {
	contest := SessionDone{Step: 0, State: ports.StateFinished, Session: &HarnessSession{Ref: "h1"}, Head: "c2",
		Raw: json.RawMessage(`{"status":"done","responses":[{"id":"F1","action":"contested","note":"not a bug"}]}`)}
	resolved := func(status string) SessionDone {
		raw, _ := json.Marshal(map[string]any{"criteria": []map[string]any{{"id": "AC1", "met": true}}, "findings": []any{},
			"previous": []map[string]any{{"id": "F1", "status": status, "note": "n"}}})
		return SessionDone{Step: 2, State: ports.StateFinished, Raw: raw, Changed: map[string][]LineRange{"a.go": {{1, 2}}}}
	}
	base := []Input{Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true, blockingFinding), contest, gates(1, ports.GatePassed)}

	s, _ := play(t, append(slices.Clone(base), resolved("resolved"))...)
	if f := s.Run.Findings.List[0]; s.Node.State != NodeGating || !f.Contested || !f.ContestAccepted {
		t.Errorf("accepted contest: node %s, finding %+v", s.Node.State, f)
	}
	s, _ = play(t, append(slices.Clone(base), resolved("open"))...)
	if f := s.Run.Findings.List[0]; s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage || f.ContestAccepted {
		t.Errorf("refused contest at the last round: node %+v, finding %+v", s.Node, f)
	}
}

func TestFlowRoundsOver(t *testing.T) {
	s, ds := play(t, Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true, blockingFinding),
		implDone(0), gates(1, ports.GatePassed), review(2, false))
	if s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage {
		t.Fatalf("node = %+v", s.Node)
	}
	if k := kindsOf(ds[len(ds)-1]); !slices.Contains(k, EffectNotify) || slices.Contains(k, EffectSession) {
		t.Errorf("effects = %v", k)
	}
}

func TestFlowFastGateRed(t *testing.T) {
	s, ds := play(t, Dispatch{}, implDone(0), gates(1, ports.GateFailed))
	if s.Node.State != NodeRunning || s.Node.Attempt != 1 || s.Run.FastGateFailures != 1 || s.Run.Step != 0 || len(s.Run.GateFailures) != 1 {
		t.Fatalf("after a red fast gate: %+v", s)
	}
	if e := lastSession(t, ds[2]); !e.Resume || len(ds[2].Moves) != 0 {
		t.Errorf("same session, no transition: %+v, moves %v", e, ds[2].Moves)
	}
	// max_retries 2: the third failure blocks.
	s, _ = play(t, Dispatch{}, implDone(0), gates(1, ports.GateFailed), implDone(0), gates(1, ports.GateError), implDone(0), gates(1, ports.GateFailed))
	if s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage || s.Node.Round != 0 {
		t.Errorf("after max_retries: %+v", s.Node)
	}
	// A green run moves on and keeps the counter for the attempt.
	s, _ = play(t, Dispatch{}, implDone(0), gates(1, ports.GateFailed), implDone(0), gates(1, ports.GatePassed))
	if s.Node.State != NodeInReview || s.Run.FastGateFailures != 1 || s.Run.GateFailures != nil {
		t.Errorf("after a green retry: %+v", s)
	}
}

func TestFlowFullGateRedStartsANewAttempt(t *testing.T) {
	ok := []Input{Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true)}
	s, ds := play(t, append(ok, gates(3, ports.GatePassed, ports.GateFailed))...)
	if s.Node.State != NodeRunning || s.Node.Attempt != 2 || s.Run.Step != 0 || !s.Run.Fresh || s.Run.Implementer != nil {
		t.Fatalf("after a red gate: %+v", s)
	}
	if e := lastSession(t, ds[len(ds)-1]); e.Resume || e.Role != RoleImplementer {
		t.Errorf("a new attempt starts a new session: %+v", e)
	}
	// Attempts over (MaxAttempts 2): blocked for triage.
	again := append(slices.Clone(ok), gates(3, ports.GateFailed), implDone(0), gates(1, ports.GatePassed), review(2, true), gates(3, ports.GateFailed))
	s, _ = play(t, again...)
	if s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage || s.Node.Attempt != 2 {
		t.Errorf("attempts over: %+v", s.Node)
	}
}

func TestFlowSessionCauses(t *testing.T) {
	for _, tt := range []struct {
		cause     ports.Cause
		reason    BlockedReason
		workspace WorkspaceTrigger
	}{
		{ports.CauseAuth, BlockedPaused, TriggerPause},
		{ports.CauseBudget, BlockedBudget, ""},
		{ports.CauseRateLimit, BlockedTriage, ""}, // the new sessions of the attempt ran out
		{ports.CauseRuntime, BlockedTriage, ""},
		{ports.CauseGuard, BlockedTriage, ""},
		{ports.CauseMaxTurns, BlockedTriage, ""},
		{ports.CauseRefusal, BlockedTriage, ""},
		{ports.CauseNoResult, BlockedTriage, ""},
	} {
		t.Run(string(tt.cause), func(t *testing.T) {
			s, ds := play(t, Dispatch{}, SessionDone{Step: 0, State: ports.StateFailed, Cause: tt.cause, Detail: "why"})
			if s.Node.State != NodeBlocked || s.Node.BlockedReason != tt.reason || ds[1].Workspace != tt.workspace {
				t.Errorf("node %+v, workspace %q", s.Node, ds[1].Workspace)
			}
			if c := ds[1].Effects[1].Body; !strings.Contains(c, "blocked: "+string(tt.reason)) || !strings.Contains(c, "\n\nimplementer session: "+string(tt.cause)+": why") {
				t.Errorf("comment = %q", c)
			}
			// Unblocking runs the step again, in a new session.
			s2, ds2 := play(t, Dispatch{}, SessionDone{Step: 0, State: ports.StateFailed, Cause: tt.cause}, Unblock{})
			if s2.Node.State != NodeRunning || lastSession(t, ds2[2]).Role != RoleImplementer {
				t.Errorf("after unblock: %+v", s2.Node)
			}
		})
	}
}

func TestFlowEndings(t *testing.T) {
	s, _ := play(t, Dispatch{}, SessionDone{Step: 0, State: ports.StateFinished, Raw: json.RawMessage(`{}`), Tampered: true, Detail: "refs/heads/main moved"})
	if s.Node.State != NodeFailed {
		t.Errorf("tampered: %+v", s.Node)
	}
	s, _ = play(t, Dispatch{}, SessionDone{Step: 0, State: ports.StateFinished, Raw: json.RawMessage(`{"status":"failed","reason":"x"}`)})
	if s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage {
		t.Errorf("implementer gave up: %+v", s.Node)
	}
	s, _ = play(t, Dispatch{}, SessionDone{Step: 0, State: ports.StateFinished})
	if s.Node.BlockedReason != BlockedTriage {
		t.Errorf("finished without a result: %+v", s.Node)
	}
}

func TestFlowRefusesStaleInputs(t *testing.T) {
	s, _ := play(t, Dispatch{})
	for name, in := range map[string]Input{
		"session of another step": review(2, true),
		"gates while in a role":   gates(0, ports.GatePassed),
	} {
		if _, err := Apply(s, in, testFlow, flowLimits); !errors.Is(err, ErrStaleInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := Apply(s, Unblock{}, testFlow, flowLimits); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("unblock of a running node: %v", err)
	}
}

func TestFlowValidate(t *testing.T) {
	if err := testFlow.Validate(); err != nil {
		t.Fatal(err)
	}
	mutate := func(f func([]FlowStep) []FlowStep) Flow { return Flow{Steps: f(slices.Clone(testFlow.Steps))} }
	for name, f := range map[string]Flow{
		"reviewer first":     mutate(func(s []FlowStep) []FlowStep { s[0], s[2] = s[2], s[0]; return s }),
		"merge not last":     mutate(func(s []FlowStep) []FlowStep { s[3], s[4] = s[4], s[3]; return s }),
		"no reviewer":        mutate(func(s []FlowStep) []FlowStep { return slices.Delete(s, 2, 4) }),
		"fast after review":  mutate(func(s []FlowStep) []FlowStep { s[3].Fast = true; return s }),
		"full before review": mutate(func(s []FlowStep) []FlowStep { s[1].Fast = false; return s }),
		"no max_rounds":      mutate(func(s []FlowStep) []FlowStep { s[2].MaxRounds = 0; return s }),
		"two reviewers":      mutate(func(s []FlowStep) []FlowStep { return slices.Insert(s, 3, s[2]) }),
		"fail to the review": mutate(func(s []FlowStep) []FlowStep { s[3].OnFail = 2; return s }),
		"too short":          {Steps: testFlow.Steps[:1]},
	} {
		if err := f.Validate(); !errors.Is(err, ErrFlowShape) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTransitionComment(t *testing.T) {
	got := TransitionComment(NodeRunning, Node{State: NodeInReview, Attempt: 1}, NodeEvent{Trigger: TriggerImplementerDone}, "ignored")
	if got != "**Kluzo** · `running` → `in_review` · implementer delivered (attempt 1, round 0)" {
		t.Errorf("comment = %q", got)
	}
	got = TransitionComment(NodeRunning, Node{State: NodeBlocked, Attempt: 2, Round: 1}, NodeEvent{Trigger: TriggerBlock, Reason: BlockedBudget}, "spent\n  all ")
	if got != "**Kluzo** · `running` → `blocked` · blocked: budget (attempt 2, round 1)\n\nspent all" {
		t.Errorf("comment = %q", got)
	}
	for trig := range nodeTriggerSet() {
		if _, ok := commentReasons[trig]; !ok && trig != TriggerBlock {
			t.Errorf("no comment words for %s", trig)
		}
	}
}

// nodeTriggerSet lists every trigger of the transition table and the "any state" rows.
func nodeTriggerSet() map[NodeTrigger]bool {
	set := map[NodeTrigger]bool{TriggerBlock: true, TriggerUnblock: true, TriggerFail: true, TriggerCancel: true}
	for _, e := range nodeTable {
		set[e.trigger] = true
	}
	return set
}
