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
		Session: &HarnessSession{Ref: "h1"}, Head: "c1", Base: "b0"}
}

func review(step int, met bool, findings ...map[string]any) SessionDone {
	raw, _ := json.Marshal(map[string]any{"criteria": []map[string]any{{"id": "AC1", "met": met}}, "findings": findings, "previous": []any{}})
	return SessionDone{Step: step, State: ports.StateFinished, Raw: raw}
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
	if !s.Run.Ready || s.Run.Step != 4 || s.Node.State != NodeQueued || s.Run.Base != "b0" {
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
	// Findings stay pending through fast gate retries, until the review.
	s, _ = play(t, Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true, blockingFinding), implDone(0), gates(1, ports.GateFailed))
	if !s.Run.Rejected || len(s.Run.GateFailures) != 1 {
		t.Errorf("rejected round with a red fast gate: %+v", s.Run)
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
		{ports.CauseHostCredentials, BlockedPaused, TriggerPause},
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

func TestFlowSessionRetry(t *testing.T) {
	s, ds := play(t, Dispatch{}, SessionRetry{Step: 0})
	if len(ds[1].Moves) != 0 || len(ds[1].Effects) != 0 || s.Node.State != NodeRunning {
		t.Errorf("after a retry: %+v, %+v", s, ds[1])
	}
	// A retried implementer round does not resume the same conversation again.
	s, _ = play(t, Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true, blockingFinding), SessionRetry{Step: 0})
	if s.Run.Implementer != nil || !s.Run.Rejected {
		t.Errorf("after a retried round: %+v", s.Run)
	}
	if _, err := Apply(s, SessionRetry{Step: 2}, testFlow, flowLimits); !errors.Is(err, ErrStaleInput) {
		t.Errorf("retry of another step: %v", err)
	}
}

func TestFlowReady(t *testing.T) {
	d, err := Apply(NodeFlow{Node: Node{State: NodePlanned}}, Ready{}, testFlow, flowLimits)
	if err != nil || d.Next.Node.State != NodeReady || len(d.Effects) != 2 || d.Effects[0].Label != "orch/ready" {
		t.Errorf("ready = %+v, %v", d, err)
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
	if _, err := Apply(s, Ready{}, testFlow, flowLimits); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("ready of a running node: %v", err)
	}
}

func TestFlowValidate(t *testing.T) {
	if err := testFlow.Validate(); err != nil {
		t.Fatal(err)
	}
	mutate := func(f func([]FlowStep) []FlowStep) Flow { return Flow{Steps: f(slices.Clone(testFlow.Steps))} }
	for name, f := range map[string]Flow{
		"reviewer first":       mutate(func(s []FlowStep) []FlowStep { s[0], s[2] = s[2], s[0]; return s }),
		"merge not last":       mutate(func(s []FlowStep) []FlowStep { s[3], s[4] = s[4], s[3]; return s }),
		"no reviewer":          mutate(func(s []FlowStep) []FlowStep { return slices.Delete(s, 2, 4) }),
		"fast after review":    mutate(func(s []FlowStep) []FlowStep { s[3].Fast = true; return s }),
		"full before review":   mutate(func(s []FlowStep) []FlowStep { s[1].Fast = false; return s }),
		"no max_rounds":        mutate(func(s []FlowStep) []FlowStep { s[2].MaxRounds = 0; return s }),
		"review not in_review": mutate(func(s []FlowStep) []FlowStep { s[2].Phase = ""; return s }),
		"implementer phase":    mutate(func(s []FlowStep) []FlowStep { s[0].Phase = NodeInReview; return s }),
		"two reviewers":        mutate(func(s []FlowStep) []FlowStep { return slices.Insert(s, 3, s[2]) }),
		"fail to the review":   mutate(func(s []FlowStep) []FlowStep { s[3].OnFail = 2; return s }),
		"too short":            {Steps: testFlow.Steps[:1]},
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

// The check after an implementer round (ADR 0020): a change that needs a
// human blocks the node as a proposal; a new suppression is an automatic
// blocking finding that only the check resolves.
func TestFlowGuaranteesAfterTheRound(t *testing.T) {
	suppression := Finding{Category: "style", File: "a.go", Line: 3, Message: "new lint suppression `//nolint`", Check: "golangci-lint run"}
	withAuto := func(step int, auto ...Finding) SessionDone {
		d := implDone(step)
		d.Auto = auto
		return d
	}
	for _, tt := range []struct {
		name   string
		inputs []Input
		check  func(t *testing.T, s NodeFlow, ds []Decision)
	}{
		{"a protected path blocks as a proposal", []Input{Dispatch{}, func() SessionDone {
			d := implDone(0)
			d.Proposal = []string{".kluzo/kluzo.yaml is a protected path", "a_test.go is a test file and was deleted"}
			return d
		}()}, func(t *testing.T, s NodeFlow, ds []Decision) {
			if s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedProposal || s.Run.Step != 0 || s.Run.Head != "c1" {
				t.Errorf("node %+v, run %+v", s.Node, s.Run)
			}
			if !slices.Contains(kindsOf(ds[1]), EffectNotify) || slices.Contains(kindsOf(ds[1]), EffectGates) {
				t.Errorf("effects = %v", kindsOf(ds[1]))
			}
		}},
		{"a suppression is an automatic blocking finding", []Input{Dispatch{}, withAuto(0, suppression), gates(1, ports.GatePassed), review(2, true)},
			func(t *testing.T, s NodeFlow, _ []Decision) {
				open := s.Run.Findings.Open()
				if len(open) != 1 || !open[0].Auto || open[0].Severity != SeverityBlocking || open[0].ID != "F1" || s.Node.State != NodeRunning || !s.Run.Rejected {
					t.Errorf("findings %+v, node %+v", open, s.Node)
				}
			}},
		{"the reviewer cannot resolve it, the check does", []Input{Dispatch{}, withAuto(0, suppression), gates(1, ports.GatePassed), review(2, true),
			withAuto(0, suppression), gates(1, ports.GatePassed), reviewPrev(2, "F1")},
			func(t *testing.T, s NodeFlow, _ []Decision) {
				if open := s.Run.Findings.Open(); len(open) != 1 || s.Node.State != NodeBlocked || s.Node.BlockedReason != BlockedTriage {
					t.Errorf("open %+v, node %+v (max_rounds 2: the rounds are over)", open, s.Node)
				}
			}},
		{"fixed in the next round", []Input{Dispatch{}, withAuto(0, suppression), gates(1, ports.GatePassed), review(2, true),
			withAuto(0), gates(1, ports.GatePassed), reviewPrev(2, "F1"), gates(3, ports.GatePassed)},
			func(t *testing.T, s NodeFlow, _ []Decision) {
				if len(s.Run.Findings.Open()) != 0 || s.Run.Findings.List[0].Status != FindingResolved || s.Node.State != NodeQueued {
					t.Errorf("findings %+v, node %+v", s.Run.Findings, s.Node)
				}
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, ds := play(t, tt.inputs...)
			tt.check(t, s, ds)
		})
	}
}

// reviewPrev approves, marking the previous findings resolved.
func reviewPrev(step int, ids ...string) SessionDone {
	prev := make([]map[string]any, len(ids))
	for i, id := range ids {
		prev[i] = map[string]any{"id": id, "status": "resolved", "note": "n"}
	}
	raw, _ := json.Marshal(map[string]any{"criteria": []map[string]any{{"id": "AC1", "met": true}}, "findings": []any{}, "previous": prev})
	return SessionDone{Step: step, State: ports.StateFinished, Raw: raw}
}

func TestSyncAuto(t *testing.T) {
	a := Finding{File: "a.go", Line: 3, Message: "m1"}
	b := Finding{File: "b.go", Line: 1, Message: "m2"}
	manual := Finding{File: "a.go", Line: 3, Message: "m1", Severity: SeverityMajor}
	fs, _ := Findings{}.Add(1, 1, manual)
	fs = fs.SyncAuto(1, 1, []Finding{a, a, b})
	if len(fs.List) != 3 || fs.List[1].ID != "F2" || !fs.List[1].Auto || fs.List[0].Auto || fs.List[2].Message != "m2" {
		t.Fatalf("first sync = %+v", fs.List)
	}
	moved := a
	moved.Line = 9
	fs = fs.SyncAuto(1, 2, []Finding{moved})
	if fs.List[1].Status != FindingOpen || fs.List[1].Line != 9 || fs.List[2].Status != FindingResolved || fs.List[0].Status != FindingOpen || len(fs.List) != 3 {
		t.Errorf("second sync = %+v", fs.List)
	}
	fs = fs.SyncAuto(2, 1, []Finding{b})
	if fs.List[1].Status != FindingResolved || fs.List[2].Status != FindingOpen || len(fs.List) != 3 {
		t.Errorf("third sync (b again) = %+v", fs.List)
	}
}

// toQueue plays a node to the merge queue: approved in the first round, from
// base b0, at c1.
var toQueue = []Input{Dispatch{}, implDone(0), gates(1, ports.GatePassed), review(2, true), gates(3, ports.GatePassed)}

// The merge queue (ADR 0009), by table: the turn starts the rebase; a clean
// rebase on the reviewed tip merges, on a moved tip runs the full gates again
// (freshness); a conflict or red freshness gates start a new attempt with the
// cause, or block the node when the attempts are over; the merge integrates
// the node and cleans it up.
func TestFlowMergeQueue(t *testing.T) {
	conflict := &IntegrationFailure{ConflictFiles: []string{"a.go"}, Markers: "<<<<<<<"}
	for _, tt := range []struct {
		name    string
		inputs  []Input
		state   NodeState
		effects []EffectKind // of the last decision, without labels and comments
		check   func(t *testing.T, s NodeFlow, last Decision)
	}{
		{"the turn starts the rebase", []Input{QueueTurn{}}, NodeIntegrating, []EffectKind{EffectRebase}, nil},
		{"a clean rebase on the reviewed tip merges", []Input{QueueTurn{}, Rebased{Tip: "b0", Head: "c1"}}, NodeIntegrating, []EffectKind{EffectMerge},
			func(t *testing.T, _ NodeFlow, last Decision) {
				if f := last.Freshness; f == nil || f.Rerun || f.Reviewed != "b0" || f.Tip != "b0" {
					t.Errorf("freshness = %+v", f)
				}
			}},
		{"a moved tip runs the full gates again", []Input{QueueTurn{}, Rebased{Tip: "t1", Head: "c2"}}, NodeIntegrating, []EffectKind{EffectGates},
			func(t *testing.T, s NodeFlow, last Decision) {
				if f := last.Freshness; f == nil || !f.Rerun || s.Run.Head != "c2" || s.Run.Tip != "t1" || !last.Effects[0].Freshness || last.Effects[0].Step != 4 {
					t.Errorf("freshness = %+v, run %+v, effects %+v", f, s.Run, last.Effects)
				}
			}},
		{"green freshness gates merge", []Input{QueueTurn{}, Rebased{Tip: "t1", Head: "c2"}, GatesDone{Step: 4, Freshness: true, Results: []ports.GateResult{{Gate: "test", State: ports.GatePassed}}}},
			NodeIntegrating, []EffectKind{EffectMerge}, nil},
		{"red freshness gates start a new attempt", []Input{QueueTurn{}, Rebased{Tip: "t1", Head: "c2"},
			GatesDone{Step: 4, Freshness: true, Changed: "x.go", Results: []ports.GateResult{{Gate: "test", State: ports.GateFailed}}}},
			NodeRunning, []EffectKind{EffectSession},
			func(t *testing.T, s NodeFlow, _ Decision) {
				if s.Node.Attempt != 2 || !s.Run.Fresh || s.Run.Ready || s.Run.Integration == nil || s.Run.Integration.Freshness != "x.go" || len(s.Run.GateFailures) != 1 {
					t.Errorf("after red freshness gates: %+v", s)
				}
			}},
		{"a conflict starts a new attempt with the cause", []Input{QueueTurn{}, Rebased{Conflict: conflict}}, NodeRunning, []EffectKind{EffectSession},
			func(t *testing.T, s NodeFlow, _ Decision) {
				if s.Node.Attempt != 2 || s.Run.Step != 0 || !s.Run.Fresh || s.Run.Implementer != nil || s.Run.Integration != conflict {
					t.Errorf("after a conflict: %+v", s)
				}
			}},
		{"the cause clears when the implementer delivers", []Input{QueueTurn{}, Rebased{Conflict: conflict}, implDone(0)}, NodeRunning, []EffectKind{EffectGates},
			func(t *testing.T, s NodeFlow, _ Decision) {
				if s.Run.Integration != nil {
					t.Errorf("integration cause kept: %+v", s.Run.Integration)
				}
			}},
		{"a conflict on the last attempt blocks", []Input{QueueTurn{}, Rebased{Conflict: conflict}, implDone(0), gates(1, ports.GatePassed), review(2, true), gates(3, ports.GatePassed), QueueTurn{}, Rebased{Conflict: conflict}},
			NodeBlocked, []EffectKind{EffectNotify},
			func(t *testing.T, s NodeFlow, _ Decision) {
				if s.Node.BlockedReason != BlockedTriage {
					t.Errorf("blocked for %s", s.Node.BlockedReason)
				}
			}},
		{"a tip moved before the merge rebases again", []Input{QueueTurn{}, Rebased{Tip: "b0", Head: "c1"}, TipMoved{}}, NodeIntegrating, []EffectKind{EffectRebase}, nil},
		{"the merge integrates and cleans up", []Input{QueueTurn{}, Rebased{Tip: "b0", Head: "c1"}, Merged{SHA: "m1"}}, NodeIntegrated, []EffectKind{EffectCleanupNode}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, ds := play(t, append(slices.Clone(toQueue), tt.inputs...)...)
			last := ds[len(ds)-1]
			if s.Node.State != tt.state {
				t.Fatalf("state = %s, want %s (moves %v)", s.Node.State, tt.state, moves(ds))
			}
			var got []EffectKind
			for _, k := range kindsOf(last) {
				if k != EffectLabel && k != EffectComment {
					got = append(got, k)
				}
			}
			if !slices.Equal(got, tt.effects) {
				t.Errorf("effects = %v, want %v", got, tt.effects)
			}
			if tt.check != nil {
				tt.check(t, s, last)
			}
		})
	}
}

// Inputs of the merge queue for a node that is not integrating are stale, and
// a cancelled node is cleaned up like an integrated one; a failed one is not.
func TestFlowMergeQueueStaleAndEndings(t *testing.T) {
	s, _ := play(t, toQueue...)
	for name, in := range map[string]Input{
		"rebase while queued":          Rebased{Tip: "b0", Head: "c1"},
		"merge while queued":           Merged{SHA: "m"},
		"moved tip while queued":       TipMoved{},
		"freshness gates while queued": GatesDone{Step: 4, Freshness: true},
	} {
		if _, err := Apply(s, in, testFlow, flowLimits); !errors.Is(err, ErrStaleInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	d := &decider{d: Decision{Next: s}, flow: testFlow, lim: flowLimits}
	if err := d.move(NodeEvent{Trigger: TriggerCancel}, flowLimits, ""); err != nil || !slices.Contains(kindsOf(d.d), EffectCleanupNode) {
		t.Errorf("cancel: %v, effects %v", err, kindsOf(d.d))
	}
	d = &decider{d: Decision{Next: s}, flow: testFlow, lim: flowLimits}
	if err := d.fail("x"); err != nil || slices.Contains(kindsOf(d.d), EffectCleanupNode) {
		t.Errorf("fail: %v, effects %v", err, kindsOf(d.d))
	}
}

func TestFullGates(t *testing.T) {
	if got := testFlow.FullGates(); !slices.Equal(got, []string{"test"}) {
		t.Errorf("FullGates = %v", got)
	}
}
