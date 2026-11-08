package core

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Review rules of ADR 0023 and docs/contracts/results.md: the ids of the
// findings, the coverage of criteria, responses and previous findings, the
// evidence check, and the verdict, which Kluzo derives instead of the
// reviewer. Everything here is pure; the only reads go through ports.Files.

// Severity of a review finding.
type Severity string

// Severities, the most serious first.
const (
	SeverityBlocking Severity = "blocking"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
)

// Blocks reports whether an open finding of this severity keeps the node from
// being approved (before the last round).
func (s Severity) Blocks() bool { return s == SeverityBlocking || s == SeverityMajor }

// FindingStatus is where a finding stands.
type FindingStatus string

// Finding statuses.
const (
	FindingOpen     FindingStatus = "open"
	FindingResolved FindingStatus = "resolved"
	// FindingGap is a major finding still open at the last round: it no
	// longer blocks and is recorded as a gap of the node.
	FindingGap FindingStatus = "gap"
)

// Implementer responses to a finding (node-result).
const (
	ActionFixed     = "fixed"
	ActionContested = "contested"
)

// CategorySecurity is the category a finding outside the changed lines keeps
// its severity with, even without caused_by.
const CategorySecurity = "security"

// Finding is a review finding with the id Kluzo gave it.
type Finding struct {
	ID       string // F1, F2…: stable for the node, across rounds and attempts
	Severity Severity
	Category string
	File     string
	Line     int
	Message  string
	Check    string
	CausedBy string // path:line changed in the round that caused it
	// Auto marks the findings Kluzo generates itself (ADR 0020): they cannot
	// be contested.
	Auto    bool
	Status  FindingStatus
	Attempt int // where the finding was first reported
	Round   int
	// Contested is the last answer of the implementer; ContestAccepted
	// records that the reviewer resolved a contested finding, which the
	// milestone report counts (ADR 0023).
	Contested       bool
	ContestAccepted bool
}

// Findings is the ledger of the findings of one node, across every round and
// attempt. The supervisor keeps it with the node; ids are never reused.
type Findings struct {
	List []Finding
	Last int // number of the last id given
}

// Open returns the findings the implementer must answer and the reviewer must
// judge again: open, blocking or major.
func (fs Findings) Open() []Finding {
	var out []Finding
	for _, f := range fs.List {
		if f.Status == FindingOpen && f.Severity.Blocks() {
			out = append(out, f)
		}
	}
	return out
}

func (fs Findings) index(id string) int {
	return slices.IndexFunc(fs.List, func(f Finding) bool { return f.ID == id })
}

// Add gives the next ids to new findings, in order, and appends them open.
// It returns the ledger and the findings with their ids.
func (fs Findings) Add(attempt, round int, news ...Finding) (Findings, []Finding) {
	out := Findings{List: slices.Clone(fs.List), Last: fs.Last}
	added := make([]Finding, len(news))
	for i, f := range news {
		out.Last++
		f.ID = "F" + strconv.Itoa(out.Last)
		f.Status, f.Attempt, f.Round = FindingOpen, attempt, round
		out.List = append(out.List, f)
		added[i] = f
	}
	return out, added
}

// LineRange is a range of lines changed in a file, both ends included.
type LineRange struct{ From, To int }

// NodeCheck is what the core needs to judge a node-result.
type NodeCheck struct {
	Criteria []Criterion // of the node, AC1..n
	// Open are the open findings sent to the implementer (Findings.Open when
	// the round started).
	Open  []Finding
	Files ports.Files // the worktree of the node
}

// NodeResult is the part of the node-result the core judges.
type NodeResult struct {
	Status    string              `json:"status"`
	Criteria  []CriterionEvidence `json:"criteria"`
	Responses []Response          `json:"responses"`
}

// CriterionEvidence is the evidence the implementer gives for a criterion.
type CriterionEvidence struct {
	ID       string   `json:"id"`
	Evidence []string `json:"evidence"`
}

// Response is the answer of the implementer to an open finding.
type Response struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Note   string `json:"note"`
}

// CheckNodeResult validates a node-result that passed its schema (results.md):
// criteria cover exactly the criteria of the node, each with evidence when the
// status is done; every evidence exists in the worktree; responses cover
// exactly the open findings, and an automatic finding is not contested. It
// returns the problems for the implementer to fix (none: valid), or an error
// when the worktree could not be read.
func CheckNodeResult(raw json.RawMessage, c NodeCheck) ([]string, error) {
	var r NodeResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return []string{"result is not a node-result: " + err.Error()}, nil
	}
	ids := make([]string, len(r.Criteria))
	var problems []string
	for i, cr := range r.Criteria {
		ids[i] = cr.ID
		if r.Status == "done" && len(cr.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("criteria: %s has no evidence; status done needs at least one per criterion", cr.ID))
		}
	}
	problems = append(problems, coverage("criteria", ids, criterionIDs(c.Criteria))...)
	evidence, err := checkEvidence(r.Criteria, c.Files)
	if err != nil {
		return nil, err
	}
	problems = append(problems, evidence...)

	ids = ids[:0]
	open := map[string]Finding{}
	for _, f := range c.Open {
		open[f.ID] = f
	}
	for _, resp := range r.Responses {
		ids = append(ids, resp.ID)
		if f, ok := open[resp.ID]; ok && f.Auto && resp.Action == ActionContested {
			problems = append(problems, fmt.Sprintf("responses: %s is an automatic finding of Kluzo and cannot be contested; fix it", resp.ID))
		}
	}
	return append(problems, coverage("responses", ids, findingIDs(c.Open))...), nil
}

// ReviewCheck is what the core needs to judge a review-result.
type ReviewCheck struct {
	Criteria []Criterion
	// Open are the open findings the implementer answered in this round
	// (Findings.Open when the round started), from earlier rounds and
	// attempts. They are empty in the first round of a node.
	Open  []Finding
	Files ports.Files
}

// ReviewResult is the review-result of the reviewer (results.md).
type ReviewResult struct {
	Criteria []CriterionVerdict `json:"criteria"`
	Previous []PreviousStatus   `json:"previous"`
	Findings []ReportedFinding  `json:"findings"`
}

// CriterionVerdict is the reviewer's judgment of a criterion.
type CriterionVerdict struct {
	ID       string   `json:"id"`
	Met      bool     `json:"met"`
	Evidence []string `json:"evidence"`
}

// PreviousStatus is the reviewer's judgment of an open finding.
type PreviousStatus struct {
	ID     string        `json:"id"`
	Status FindingStatus `json:"status"`
	Note   string        `json:"note"`
}

// ReportedFinding is a new finding as the reviewer reports it, without id.
type ReportedFinding struct {
	Severity Severity `json:"severity"`
	Category string   `json:"category"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Message  string   `json:"message"`
	Check    string   `json:"check"`
	CausedBy *string  `json:"caused_by"`
	Auto     bool     `json:"auto"`
}

// CheckReviewResult validates a review-result that passed its schema
// (results.md): criteria cover exactly the criteria of the node and their
// evidence exists; previous covers exactly the open findings of earlier
// rounds and attempts (none in the first round);
// only Kluzo marks a finding automatic. It returns the problems for the
// reviewer to fix, or an error when the worktree could not be read.
func CheckReviewResult(raw json.RawMessage, c ReviewCheck) ([]string, error) {
	var r ReviewResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return []string{"result is not a review-result: " + err.Error()}, nil
	}
	var problems []string
	ids := make([]string, len(r.Criteria))
	evidence := make([]CriterionEvidence, len(r.Criteria))
	for i, cr := range r.Criteria {
		ids[i] = cr.ID
		evidence[i] = CriterionEvidence{ID: cr.ID, Evidence: cr.Evidence}
	}
	problems = append(problems, coverage("criteria", ids, criterionIDs(c.Criteria))...)
	found, err := checkEvidence(evidence, c.Files)
	if err != nil {
		return nil, err
	}
	problems = append(problems, found...)

	ids = ids[:0]
	for _, p := range r.Previous {
		ids = append(ids, p.ID)
	}
	problems = append(problems, coverage("previous", ids, findingIDs(c.Open))...)
	for i, f := range r.Findings {
		if f.Auto {
			problems = append(problems, fmt.Sprintf("findings[%d]: auto is set by Kluzo only; remove it", i))
		}
	}
	return problems, nil
}

// ReviewInput is a valid review and what the core needs to derive the verdict.
type ReviewInput struct {
	Result   ReviewResult
	Findings Findings // the ledger before the review, automatic findings of the round included
	Attempt  int
	Round    int // review round within the attempt, from 1
	// MaxRounds is max_rounds of the review step; Round == MaxRounds is the
	// last round.
	MaxRounds int
	// Changed are the lines the round changed, by file. From round 2, a new
	// finding outside them needs caused_by pointing into them, or it becomes
	// minor (except security).
	Changed map[string][]LineRange
	// Responses are the answers of the implementer in this round.
	Responses []Response
}

// Verdict is the outcome Kluzo derives from a review (ADR 0023, item 3).
type Verdict string

// Verdicts.
const (
	VerdictApprove        Verdict = "approve"
	VerdictRequestChanges Verdict = "request_changes"
)

// ReviewOutcome is the derived verdict, the updated ledger and what happens to
// the node.
type ReviewOutcome struct {
	Verdict  Verdict
	Findings Findings
	New      []Finding // the findings of this review, with their ids
	Gaps     []Finding // majors left open at the last round, recorded as gaps
	// Event is the trigger for the node in in_review: approved, rejected for
	// a new round, or blocked for triage when the rounds are over.
	Event NodeEvent
}

// DeriveReview applies a valid review to the ledger and derives the verdict:
// approve only with every criterion met and no blocking or major finding
// open. At the last round a major no longer blocks: it becomes a gap; a
// blocking finding or an unmet criterion sends the node to triage.
func DeriveReview(in ReviewInput) ReviewOutcome {
	fs := Findings{List: slices.Clone(in.Findings.List), Last: in.Findings.Last}
	for _, r := range in.Responses {
		if i := fs.index(r.ID); i >= 0 {
			fs.List[i].Contested = r.Action == ActionContested
		}
	}
	for _, p := range in.Result.Previous {
		i := fs.index(p.ID)
		if i < 0 || fs.List[i].Status != FindingOpen {
			continue
		}
		fs.List[i].Status = p.Status
		fs.List[i].ContestAccepted = p.Status == FindingResolved && fs.List[i].Contested
	}
	news := make([]Finding, len(in.Result.Findings))
	for i, r := range in.Result.Findings {
		news[i] = Finding{Severity: r.Severity, Category: r.Category, File: r.File, Line: r.Line, Message: r.Message, Check: r.Check}
		if r.CausedBy != nil {
			news[i].CausedBy = *r.CausedBy
		}
		if in.Round >= 2 && !inChanged(in.Changed, r.File, r.Line) && !causedByChange(in.Changed, news[i].CausedBy) && r.Category != CategorySecurity {
			news[i].Severity = SeverityMinor
		}
	}
	fs, added := fs.Add(in.Attempt, in.Round, news...)

	met := true
	for _, c := range in.Result.Criteria {
		met = met && c.Met
	}
	last := in.Round >= in.MaxRounds
	out := ReviewOutcome{Findings: fs, New: added}
	blocking := false
	for i, f := range fs.List {
		switch {
		case f.Status != FindingOpen || !f.Severity.Blocks():
		case f.Severity == SeverityMajor && last:
			fs.List[i].Status = FindingGap
			out.Gaps = append(out.Gaps, fs.List[i])
		default:
			blocking = true
		}
	}
	switch {
	case met && !blocking:
		out.Verdict, out.Event = VerdictApprove, NodeEvent{Trigger: TriggerReviewApproved}
	case last:
		out.Verdict, out.Event = VerdictRequestChanges, NodeEvent{Trigger: TriggerBlock, Reason: BlockedTriage}
	default:
		out.Verdict, out.Event = VerdictRequestChanges, NodeEvent{Trigger: TriggerReviewRejected}
	}
	return out
}

// inChanged reports whether file:line is inside the lines the round changed.
func inChanged(changed map[string][]LineRange, file string, line int) bool {
	return slices.ContainsFunc(changed[file], func(r LineRange) bool { return line >= r.From && line <= r.To })
}

// causedByChange reports whether a caused_by reference (path:line) points
// into the lines the round changed.
func causedByChange(changed map[string][]LineRange, ref string) bool {
	path, line, ok := splitLine(ref)
	return ok && inChanged(changed, path, line)
}

// splitLine splits "path:line"; ok is false when the part after the last
// colon is not a positive line number.
func splitLine(ref string) (string, int, bool) {
	i := strings.LastIndexByte(ref, ':')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(ref[i+1:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return ref[:i], n, true
}

// checkEvidence verifies every evidence of the criteria in the worktree:
// path:line needs the file with at least that many lines, path:Name needs the
// file (ADR 0023, item 5; the test name is checked from v0.2).
func checkEvidence(criteria []CriterionEvidence, files ports.Files) ([]string, error) {
	var problems []string
	for _, c := range criteria {
		for _, ev := range c.Evidence {
			path, line, isLine := splitLine(ev)
			if !isLine {
				j := strings.LastIndexByte(ev, ':')
				if j <= 0 || j == len(ev)-1 {
					problems = append(problems, fmt.Sprintf("criteria: %s evidence %q is not path:line or path:Name", c.ID, ev))
					continue
				}
				path = ev[:j]
			}
			n, ok, err := files.Lines(path)
			switch {
			case err != nil:
				return nil, fmt.Errorf("check the evidence %q of %s: %w", ev, c.ID, err)
			case !ok:
				problems = append(problems, fmt.Sprintf("criteria: %s evidence %q: %s does not exist in the worktree", c.ID, ev, path))
			case isLine && line > n:
				problems = append(problems, fmt.Sprintf("criteria: %s evidence %q: %s has %d lines", c.ID, ev, path, n))
			}
		}
	}
	return problems, nil
}

// coverage compares the ids a result lists with the ids it must cover,
// exactly: none missing, none unknown, none twice.
func coverage(field string, got, want []string) []string {
	var problems []string
	seen := map[string]bool{}
	for _, id := range got {
		switch {
		case seen[id]:
			problems = append(problems, fmt.Sprintf("%s: %s is listed twice", field, id))
		case !slices.Contains(want, id):
			problems = append(problems, fmt.Sprintf("%s: %s is not one of %s", field, id, list(want)))
		}
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			problems = append(problems, fmt.Sprintf("%s: %s is missing", field, id))
		}
	}
	return problems
}

func list(ids []string) string {
	if len(ids) == 0 {
		return "the expected ids (none are expected)"
	}
	return strings.Join(ids, ", ")
}

func criterionIDs(cs []Criterion) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

func findingIDs(fs []Finding) []string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	return ids
}
