package core

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

var (
	testCriteria = []Criterion{{ID: "AC1", Text: "creates"}, {ID: "AC2", Text: "lists"}}
	testFiles    = fakes.Files{
		"internal/agenda/service.go":      strings.Repeat("x\n", 50),
		"internal/agenda/service_test.go": strings.Repeat("x\n", 20),
	}
	openF1 = Finding{ID: "F1", Severity: SeverityBlocking, Category: "correctness", Status: FindingOpen}
	openF2 = Finding{ID: "F2", Severity: SeverityMajor, Category: "tests", Status: FindingOpen}
	autoF3 = Finding{ID: "F3", Severity: SeverityBlocking, Category: "style", Status: FindingOpen, Auto: true}
)

// brokenFiles fails every read, as a worktree that cannot be read.
type brokenFiles struct{}

var errDisk = errors.New("disk")

func (brokenFiles) Lines(string) (int, bool, error) { return 0, false, errDisk }

func TestFindingIDsAreStable(t *testing.T) {
	var fs Findings
	fs, first := fs.Add(1, 1, Finding{Severity: SeverityBlocking}, Finding{Severity: SeverityMinor})
	fs, second := fs.Add(1, 2, Finding{Severity: SeverityMajor, Auto: true})
	// A new attempt keeps counting: ids are per node, never reused.
	fs, third := fs.Add(2, 1, Finding{Severity: SeverityMajor})
	got := []string{first[0].ID, first[1].ID, second[0].ID, third[0].ID}
	if want := []string{"F1", "F2", "F3", "F4"}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if f := fs.List[2]; f.Status != FindingOpen || f.Attempt != 1 || f.Round != 2 || !f.Auto {
		t.Errorf("F3 = %+v", f)
	}
	if f := fs.List[3]; f.Attempt != 2 || f.Round != 1 {
		t.Errorf("F4 = %+v", f)
	}
	// Open lists blocking and major findings that are open; minors never.
	fs.List[3].Status = FindingResolved
	if got := findingIDs(fs.Open()); !slices.Equal(got, []string{"F1", "F3"}) {
		t.Errorf("open = %v", got)
	}
	// Add does not touch the ledger it was given.
	var empty Findings
	if _, _ = empty.Add(1, 1, Finding{}); empty.Last != 0 || len(empty.List) != 0 {
		t.Errorf("Add changed its receiver: %+v", empty)
	}
}

func TestCheckNodeResult(t *testing.T) {
	ev := func(id string, evidence ...string) map[string]any {
		return map[string]any{"id": id, "evidence": evidence}
	}
	resp := func(id, action string) map[string]any { return map[string]any{"id": id, "action": action, "note": "n"} }
	good := []map[string]any{ev("AC1", "internal/agenda/service.go:41"), ev("AC2", "internal/agenda/service_test.go:TestList")}
	tests := []struct {
		name      string
		status    string
		criteria  []map[string]any
		responses []map[string]any
		open      []Finding
		want      []string // substrings, one per problem; none: valid
	}{
		{name: "valid first round", status: "done", criteria: good},
		{name: "valid with responses", status: "done", criteria: good, open: []Finding{openF1, openF2},
			responses: []map[string]any{resp("F1", ActionFixed), resp("F2", ActionContested)}},
		{name: "automatic finding fixed", status: "done", criteria: good, open: []Finding{autoF3}, responses: []map[string]any{resp("F3", ActionFixed)}},
		{name: "missing criterion", status: "done", criteria: good[:1], want: []string{"criteria: AC2 is missing"}},
		{name: "unknown criterion", status: "done", criteria: append(slices.Clone(good), ev("AC9", "internal/agenda/service.go:1")), want: []string{"criteria: AC9 is not one of AC1, AC2"}},
		{name: "criterion twice", status: "done", criteria: append(slices.Clone(good), ev("AC1", "internal/agenda/service.go:1")), want: []string{"criteria: AC1 is listed twice"}},
		{name: "done without evidence", status: "done", criteria: []map[string]any{good[0], ev("AC2")}, want: []string{"AC2 has no evidence"}},
		{name: "failed may lack evidence", status: "failed", criteria: []map[string]any{good[0], ev("AC2")}},
		{name: "file missing", status: "done", criteria: []map[string]any{good[0], ev("AC2", "internal/agenda/gone.go:3")}, want: []string{"internal/agenda/gone.go does not exist"}},
		{name: "line past the end", status: "done", criteria: []map[string]any{good[0], ev("AC2", "internal/agenda/service_test.go:21")}, want: []string{"has 20 lines"}},
		{name: "last line", status: "done", criteria: []map[string]any{good[0], ev("AC2", "internal/agenda/service_test.go:20")}},
		{name: "test name in a missing file", status: "done", criteria: []map[string]any{good[0], ev("AC2", "internal/x_test.go:TestX")}, want: []string{"internal/x_test.go does not exist"}},
		{name: "not path:line nor path:Name", status: "done", criteria: []map[string]any{good[0], ev("AC2", "service.go", "x.go:")}, want: []string{`"service.go" is not path:line`, `"x.go:" is not path:line`}},
		{name: "outside the worktree", status: "done", criteria: []map[string]any{good[0], ev("AC2", "../secret.go:1")}, want: []string{"../secret.go does not exist"}},
		{name: "response missing", status: "done", criteria: good, open: []Finding{openF1, openF2}, responses: []map[string]any{resp("F1", ActionFixed)}, want: []string{"responses: F2 is missing"}},
		{name: "response to an unknown finding", status: "done", criteria: good, responses: []map[string]any{resp("F7", ActionFixed)}, want: []string{"responses: F7 is not one of the expected ids (none are expected)"}},
		{name: "response twice", status: "done", criteria: good, open: []Finding{openF1}, responses: []map[string]any{resp("F1", ActionFixed), resp("F1", ActionContested)}, want: []string{"responses: F1 is listed twice"}},
		{name: "automatic finding contested", status: "done", criteria: good, open: []Finding{autoF3}, responses: []map[string]any{resp("F3", ActionContested)}, want: []string{"F3 is an automatic finding of Kluzo and cannot be contested"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"status": tt.status, "criteria": tt.criteria, "responses": tt.responses})
			got, err := CheckNodeResult(raw, NodeCheck{Criteria: testCriteria, Open: tt.open, Files: testFiles})
			if err != nil {
				t.Fatal(err)
			}
			assertProblems(t, got, tt.want)
		})
	}
	if _, err := CheckNodeResult(json.RawMessage(`{"status":"done","criteria":[{"id":"AC1","evidence":["a.go:1"]}]}`), NodeCheck{Criteria: testCriteria, Files: brokenFiles{}}); !errors.Is(err, errDisk) {
		t.Errorf("a read failure = %v, want it returned", err)
	}
	if got, _ := CheckNodeResult(json.RawMessage(`[]`), NodeCheck{Criteria: testCriteria, Files: testFiles}); len(got) != 1 {
		t.Errorf("not an object: %v", got)
	}
}

func TestCheckReviewResult(t *testing.T) {
	crit := func(id string, met bool, evidence ...string) map[string]any {
		return map[string]any{"id": id, "met": met, "evidence": evidence}
	}
	prev := func(id, status string) map[string]any { return map[string]any{"id": id, "status": status, "note": "n"} }
	finding := func(auto bool) map[string]any {
		return map[string]any{"severity": "major", "category": "tests", "file": "a.go", "line": 1, "message": "m", "check": "c", "auto": auto}
	}
	good := []map[string]any{crit("AC1", true, "internal/agenda/service.go:41"), crit("AC2", false)}
	tests := []struct {
		name     string
		criteria []map[string]any
		previous []map[string]any
		findings []map[string]any
		open     []Finding
		want     []string
	}{
		{name: "valid first round", criteria: good, findings: []map[string]any{finding(false)}},
		{name: "valid with previous", criteria: good, open: []Finding{openF1, autoF3}, previous: []map[string]any{prev("F1", "resolved"), prev("F3", "open")}},
		{name: "previous in the first round", criteria: good, previous: []map[string]any{prev("F1", "open")}, want: []string{"previous: F1 is not one of the expected ids"}},
		{name: "previous missing", criteria: good, open: []Finding{openF1, openF2}, previous: []map[string]any{prev("F2", "open")}, want: []string{"previous: F1 is missing"}},
		{name: "previous twice", criteria: good, open: []Finding{openF1}, previous: []map[string]any{prev("F1", "open"), prev("F1", "resolved")}, want: []string{"previous: F1 is listed twice"}},
		{name: "criterion missing", criteria: good[:1], want: []string{"criteria: AC2 is missing"}},
		{name: "evidence of the reviewer checked too", criteria: []map[string]any{good[0], crit("AC2", true, "nope.go:1")}, want: []string{"nope.go does not exist"}},
		{name: "auto claimed by the reviewer", criteria: good, findings: []map[string]any{finding(true)}, want: []string{"findings[0]: auto is set by Kluzo only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"criteria": tt.criteria, "previous": tt.previous, "findings": tt.findings})
			got, err := CheckReviewResult(raw, ReviewCheck{Criteria: testCriteria, Open: tt.open, Files: testFiles})
			if err != nil {
				t.Fatal(err)
			}
			assertProblems(t, got, tt.want)
		})
	}
	if _, err := CheckReviewResult(json.RawMessage(`{"criteria":[{"id":"AC1","met":true,"evidence":["a.go:1"]}]}`), ReviewCheck{Criteria: testCriteria, Files: brokenFiles{}}); !errors.Is(err, errDisk) {
		t.Errorf("a read failure = %v, want it returned", err)
	}
}

func assertProblems(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("problems = %q, want %d matching %q", got, len(want), want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("problem %d = %q, want it to contain %q", i, got[i], w)
		}
	}
}

func TestDeriveReview(t *testing.T) {
	met := []CriterionVerdict{{ID: "AC1", Met: true}, {ID: "AC2", Met: true}}
	unmet := []CriterionVerdict{{ID: "AC1", Met: true}, {ID: "AC2", Met: false}}
	ledger := func(fs ...Finding) Findings { return Findings{List: fs, Last: len(fs)} }
	rf := func(sev Severity, cat, file string, line int, causedBy string) ReportedFinding {
		f := ReportedFinding{Severity: sev, Category: cat, File: file, Line: line, Message: "m", Check: "c"}
		if causedBy != "" {
			f.CausedBy = &causedBy
		}
		return f
	}
	changed := map[string][]LineRange{"a.go": {{From: 10, To: 20}}}
	approve := NodeEvent{Trigger: TriggerReviewApproved}
	reject := NodeEvent{Trigger: TriggerReviewRejected}
	triage := NodeEvent{Trigger: TriggerBlock, Reason: BlockedTriage}

	tests := []struct {
		name      string
		in        ReviewInput
		verdict   Verdict
		event     NodeEvent
		severity  []Severity // of the new findings, in order
		gaps      []string
		statuses  map[string]FindingStatus
		newIDs    []string
		accepted  []string // findings whose contest was accepted
		contested []string
	}{
		{name: "clean review approves", in: ReviewInput{Result: ReviewResult{Criteria: met}, Round: 1, MaxRounds: 3}, verdict: VerdictApprove, event: approve},
		{name: "a minor does not block", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityMinor, "style", "a.go", 1, "")}}, Round: 1, MaxRounds: 3},
			verdict: VerdictApprove, event: approve, severity: []Severity{SeverityMinor}, newIDs: []string{"F1"}},
		{name: "a blocking finding asks for changes", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityBlocking, "correctness", "a.go", 1, "")}}, Round: 1, MaxRounds: 3},
			verdict: VerdictRequestChanges, event: reject, severity: []Severity{SeverityBlocking}, newIDs: []string{"F1"}},
		{name: "round 2: a major outside the changes becomes minor", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityMajor, "tests", "a.go", 1, "")}}, Round: 2, MaxRounds: 3, Changed: changed},
			verdict: VerdictApprove, event: approve, severity: []Severity{SeverityMinor}, newIDs: []string{"F1"}},
		{name: "a major inside the changed lines", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityMajor, "tests", "a.go", 15, "")}}, Round: 2, MaxRounds: 3, Changed: changed},
			verdict: VerdictRequestChanges, event: reject, severity: []Severity{SeverityMajor}, newIDs: []string{"F1"}},
		{name: "an unmet criterion asks for changes", in: ReviewInput{Result: ReviewResult{Criteria: unmet}, Round: 1, MaxRounds: 3}, verdict: VerdictRequestChanges, event: reject},
		{name: "a previous finding still open", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F1", Status: FindingOpen}}}, Findings: ledger(openF1), Round: 2, MaxRounds: 3},
			verdict: VerdictRequestChanges, event: reject, statuses: map[string]FindingStatus{"F1": FindingOpen}},
		{name: "previous findings resolved", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F1", Status: FindingResolved}, {ID: "F2", Status: FindingResolved}}}, Findings: ledger(openF1, openF2), Round: 2, MaxRounds: 3,
			Responses: []Response{{ID: "F1", Action: ActionFixed}, {ID: "F2", Action: ActionContested}}},
			verdict: VerdictApprove, event: approve, statuses: map[string]FindingStatus{"F1": FindingResolved, "F2": FindingResolved}, accepted: []string{"F2"}, contested: []string{"F2"}},
		{name: "a contest the reviewer rejects", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F1", Status: FindingOpen}}}, Findings: ledger(openF1), Round: 2, MaxRounds: 3,
			Responses: []Response{{ID: "F1", Action: ActionContested}}},
			verdict: VerdictRequestChanges, event: reject, statuses: map[string]FindingStatus{"F1": FindingOpen}, contested: []string{"F1"}},
		{name: "an automatic finding of the round blocks", in: ReviewInput{Result: ReviewResult{Criteria: met}, Findings: ledger(autoF3), Round: 1, MaxRounds: 3}, verdict: VerdictRequestChanges, event: reject},
		{name: "last round: a major becomes a gap", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F2", Status: FindingOpen}}, Findings: []ReportedFinding{rf(SeverityMajor, "tests", "a.go", 12, "")}}, Findings: ledger(openF1, openF2), Round: 3, MaxRounds: 3, Changed: changed},
			verdict: VerdictRequestChanges, event: triage, severity: []Severity{SeverityMajor}, newIDs: []string{"F3"}, gaps: []string{"F2", "F3"}, statuses: map[string]FindingStatus{"F1": FindingOpen, "F2": FindingGap, "F3": FindingGap}},
		{name: "last round: only majors left approves with gaps", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F1", Status: FindingResolved}, {ID: "F2", Status: FindingOpen}}}, Findings: ledger(openF1, openF2), Round: 3, MaxRounds: 3},
			verdict: VerdictApprove, event: approve, gaps: []string{"F2"}, statuses: map[string]FindingStatus{"F1": FindingResolved, "F2": FindingGap}},
		{name: "last round: an open blocking goes to triage", in: ReviewInput{Result: ReviewResult{Criteria: met, Previous: []PreviousStatus{{ID: "F1", Status: FindingOpen}}}, Findings: ledger(openF1), Round: 2, MaxRounds: 2},
			verdict: VerdictRequestChanges, event: triage, statuses: map[string]FindingStatus{"F1": FindingOpen}},
		{name: "last round: an unmet criterion goes to triage", in: ReviewInput{Result: ReviewResult{Criteria: unmet}, Round: 1, MaxRounds: 1}, verdict: VerdictRequestChanges, event: triage},
		{name: "outside the changes with caused_by keeps its severity", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityBlocking, "correctness", "b.go", 3, "a.go:11")}}, Round: 2, MaxRounds: 3, Changed: changed},
			verdict: VerdictRequestChanges, event: reject, severity: []Severity{SeverityBlocking}, newIDs: []string{"F1"}},
		{name: "a caused_by outside the changes does not count", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityBlocking, "correctness", "b.go", 3, "a.go:30"), rf(SeverityBlocking, "correctness", "b.go", 4, "a.go")}}, Round: 2, MaxRounds: 3, Changed: changed},
			verdict: VerdictApprove, event: approve, severity: []Severity{SeverityMinor, SeverityMinor}, newIDs: []string{"F1", "F2"}},
		{name: "security outside the changes keeps its severity", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityBlocking, CategorySecurity, "b.go", 3, "")}}, Round: 2, MaxRounds: 3, Changed: changed},
			verdict: VerdictRequestChanges, event: reject, severity: []Severity{SeverityBlocking}, newIDs: []string{"F1"}},
		{name: "the first round judges every line", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityBlocking, "correctness", "b.go", 3, "")}}, Round: 1, MaxRounds: 3},
			verdict: VerdictRequestChanges, event: reject, severity: []Severity{SeverityBlocking}, newIDs: []string{"F1"}},
		{name: "a new attempt continues the ids", in: ReviewInput{Result: ReviewResult{Criteria: met, Findings: []ReportedFinding{rf(SeverityMinor, "style", "a.go", 1, "")}}, Findings: Findings{List: []Finding{{ID: "F1", Severity: SeverityBlocking, Status: FindingResolved}}, Last: 4}, Attempt: 2, Round: 1, MaxRounds: 3},
			verdict: VerdictApprove, event: approve, severity: []Severity{SeverityMinor}, newIDs: []string{"F5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(tt.in.Findings.List)
			var statuses []FindingStatus
			for _, f := range tt.in.Findings.List {
				statuses = append(statuses, f.Status)
			}
			out := DeriveReview(tt.in)
			if out.Verdict != tt.verdict || out.Event != tt.event {
				t.Errorf("verdict = %s %+v, want %s %+v", out.Verdict, out.Event, tt.verdict, tt.event)
			}
			var sev []Severity
			for _, f := range out.New {
				sev = append(sev, f.Severity)
			}
			if !slices.Equal(sev, tt.severity) || !slices.Equal(findingIDs(out.New), tt.newIDs) {
				t.Errorf("new = %+v, want severities %v ids %v", out.New, tt.severity, tt.newIDs)
			}
			if !slices.Equal(findingIDs(out.Gaps), tt.gaps) {
				t.Errorf("gaps = %v, want %v", findingIDs(out.Gaps), tt.gaps)
			}
			for id, st := range tt.statuses {
				if i := out.Findings.index(id); i < 0 || out.Findings.List[i].Status != st {
					t.Errorf("%s status = %+v, want %s", id, out.Findings.List, st)
				}
			}
			var accepted, contested []string
			for _, f := range out.Findings.List {
				if f.ContestAccepted {
					accepted = append(accepted, f.ID)
				}
				if f.Contested {
					contested = append(contested, f.ID)
				}
			}
			if !slices.Equal(accepted, tt.accepted) || !slices.Equal(contested, tt.contested) {
				t.Errorf("contested %v accepted %v, want %v %v", contested, accepted, tt.contested, tt.accepted)
			}
			if len(out.Findings.List) != before+len(tt.in.Result.Findings) {
				t.Errorf("ledger has %d findings, want %d", len(out.Findings.List), before+len(tt.in.Result.Findings))
			}
			for i, f := range tt.in.Findings.List {
				if f.Status != statuses[i] {
					t.Errorf("DeriveReview changed its input: %s is %s", f.ID, f.Status)
				}
			}
		})
	}
}

// The outcome event is valid for a node in review: approve and triage always,
// reject while rounds remain (Round is 0-based in Node, 1-based here).
func TestDeriveReviewEventsApplyToTheNode(t *testing.T) {
	lim := Limits{MaxAttempts: 3, MaxReviewRounds: 2}
	for round := 1; round <= lim.MaxReviewRounds; round++ {
		n := Node{State: NodeInReview, Attempt: 1, Round: round - 1}
		for _, criteria := range [][]CriterionVerdict{{{ID: "AC1", Met: true}}, {{ID: "AC1", Met: false}}} {
			out := DeriveReview(ReviewInput{Result: ReviewResult{Criteria: criteria}, Attempt: 1, Round: round, MaxRounds: lim.MaxReviewRounds})
			if _, err := NextNode(n, out.Event, lim); err != nil {
				t.Errorf("round %d, %s: %v", round, out.Verdict, err)
			}
		}
	}
}
