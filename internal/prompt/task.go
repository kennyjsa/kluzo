package prompt

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/text"
)

// Task is the data of segment 3 (docs/contracts/prompt.md#segmento-3--tarefa).
// Every field of free text comes from outside Kluzo, or from another role, and
// is put in an envelope; what Kluzo or the project configuration wrote stays
// plain.
type Task struct {
	Step        string // step id in the flow
	Attempt     int
	MaxAttempts int
	Round       int
	MaxRounds   int

	Issue  string // tracker reference of the node
	Branch string
	Focus  Focus

	IssueTitle string
	IssueBody  string
	Author     string // issue author, for the envelope

	Criteria  []core.Criterion
	Verify    []Command // gates frozen at the milestone tip at dispatch
	Neighbors Neighbors
	Decisions []Decision

	// Why is set on a retry or a new round.
	Why *Why
	// Previous is set on a new session from attempt 2.
	Previous *PreviousAttempt

	// Reviewer only.
	PreviousFindings  []Finding // from round 2: the open findings
	ImplementerReport string    // the implementer's result
	Diff              *Diff     // from round 2: the diff of the round

	// Reduced is the message sent to a resumed session: only Why and Deliver.
	Reduced bool
}

// Focus is the starting point of the work, not a limit.
type Focus struct {
	Areas     []AreaGlobs
	Contracts []string // paths of neighboring contracts
}

// AreaGlobs is an area of kluzo.yaml and its globs.
type AreaGlobs struct {
	Name  string
	Globs []string
}

// Command is a gate command.
type Command struct{ Name, Command string }

// Neighbors are the integrated dependencies and the contracts of the node.
type Neighbors struct {
	Integrated []Integrated
	Contracts  []string
}

// Integrated is a dependency already integrated in the milestone.
type Integrated struct {
	Issue, Title string
	Summary      string // from the node result of that dependency
}

// Decision is a decision of the milestone relevant to the node.
type Decision struct {
	Node             string
	Title, Rationale string
}

// Finding is a review finding of the ledger of the node (core.Finding); ID is
// assigned by Kluzo.
type Finding = core.Finding

// Why gathers the causes of a retry; each present cause gets a subsection.
type Why struct {
	Review      *ReviewCause
	Gate        []GateFailure
	Integration *IntegrationCause
	Triage      *TriageCause
}

// ReviewCause carries the findings of the last review.
type ReviewCause struct{ Findings []Finding }

// GateFailure is one failed gate.
type GateFailure struct {
	Name     string
	Command  string
	ExitCode int
	Failures []TestFailure // structured, when the runner provides them
	Log      string
	LogPath  string // where the full log is
	Report   string // where the full report is
}

// TestFailure is a structured test failure.
type TestFailure struct {
	Test, File string
	Line       int
	Message    string
}

// IntegrationCause is a rebase conflict or a freshness failure.
type IntegrationCause struct {
	ConflictFiles []string
	Markers       string // the conflict markers, as text
	Freshness     string // what changed in the milestone since the review
}

// TriageCause is the decision of the triage.
type TriageCause struct {
	Summary string
	Params  map[string]string
}

// PreviousAttempt carries the facts of an earlier attempt, from the journal.
type PreviousAttempt struct {
	Attempt int
	Facts   []string
}

// Diff is the diff of the round and the files it touches.
type Diff struct {
	Text  string
	Files []DiffFile
}

// DiffFile is a changed file with its counts.
type DiffFile struct {
	Path           string
	Added, Removed int
}

// sectionOrder is the fixed order of the sections of segment 3; sections that
// do not apply are omitted.
var sectionOrder = []string{
	"## Task", "## Issue", "## Acceptance criteria", "## Verify", "## Neighbors", "## Decisions so far",
	"## Why you are here again", "## Previous attempt (facts)", "## Previous findings",
	"## Implementer report", "## Round diff", "## Instructions", "## Deliver",
}

// renderer builds segment 3 for one nonce.
type renderer struct {
	nonce    string
	collided bool
	limits   config.PromptLimits
	in       Input
}

func (r *renderer) wrap(source, kind, author, text string) string {
	out, collided := envelope(r.nonce, source, kind, author, text)
	r.collided = r.collided || collided
	return out
}

// renderTask renders segment 3.
func (r *renderer) renderTask() string {
	t := r.in.Task
	bodies := map[string]func() string{
		"## Task": r.header, "## Issue": r.issue, "## Acceptance criteria": r.criteria,
		"## Verify": r.verify, "## Neighbors": r.neighbors, "## Decisions so far": r.decisions,
		"## Why you are here again": r.why, "## Previous attempt (facts)": r.previous,
		"## Previous findings": r.previousFindings, "## Implementer report": r.report,
		"## Round diff": r.roundDiff, "## Instructions": r.instructions, "## Deliver": r.deliver,
	}
	var parts []string
	for _, h := range sectionOrder {
		if t.Reduced && h != "## Why you are here again" && h != "## Deliver" {
			continue
		}
		if body := bodies[h](); body != "" {
			parts = append(parts, h+"\n"+body)
		}
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func (r *renderer) header() string {
	t := r.in.Task
	var b strings.Builder
	fmt.Fprintf(&b, "Step: %s (%s) · Attempt %d of %d · Round %d of %d\n", t.Step, r.in.Role.Name, t.Attempt, t.MaxAttempts, t.Round, t.MaxRounds)
	fmt.Fprintf(&b, "Node: #%s · Branch: %s", t.Issue, t.Branch)
	var focus []string
	for _, a := range t.Focus.Areas {
		focus = append(focus, a.Name+" → "+strings.Join(a.Globs, ", "))
	}
	if len(t.Focus.Contracts) > 0 {
		focus = append(focus, "neighboring contracts → "+strings.Join(t.Focus.Contracts, ", "))
	}
	if len(focus) > 0 {
		b.WriteString("\nFocus (a starting point, not a limit): " + strings.Join(focus, "; "))
	}
	return b.String()
}

func (r *renderer) issue() string {
	t := r.in.Task
	if t.IssueTitle == "" && t.IssueBody == "" {
		return ""
	}
	return r.wrap("tracker", "issue", t.Author, "# "+t.IssueTitle+"\n\n"+t.IssueBody)
}

func (r *renderer) criteria() string {
	t := r.in.Task
	if len(t.Criteria) == 0 {
		return ""
	}
	lines := make([]string, len(t.Criteria))
	for i, c := range t.Criteria {
		lines[i] = c.ID + ": " + c.Text
	}
	return "Cover exactly these ids.\n" + r.wrap("plan", "acceptance", "", strings.Join(lines, "\n"))
}

func (r *renderer) verify() string {
	t := r.in.Task
	if len(t.Verify) == 0 {
		return ""
	}
	lines := make([]string, len(t.Verify))
	for i, c := range t.Verify {
		lines[i] = "- " + c.Name + ": `" + c.Command + "`"
	}
	return "Run the commands that cover what you changed (the official gates run again after you):\n" + strings.Join(lines, "\n")
}

func (r *renderer) neighbors() string {
	n := r.in.Task.Neighbors
	var parts []string
	if len(n.Integrated) > 0 {
		var lines []string
		for _, d := range n.Integrated {
			lines = append(lines, "#"+d.Issue+" "+d.Title+"\n"+d.Summary)
		}
		parts = append(parts, "Integrated dependencies:\n"+r.wrap("milestone", "neighbors", "", strings.Join(lines, "\n\n")))
	}
	if len(n.Contracts) > 0 {
		parts = append(parts, "Contracts: "+strings.Join(n.Contracts, ", "))
	}
	return strings.Join(parts, "\n")
}

func (r *renderer) decisions() string {
	ds := r.in.Task.Decisions
	if len(ds) == 0 {
		return ""
	}
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = "- #" + d.Node + " " + d.Title + ": " + d.Rationale
	}
	return r.wrap("decisions", "milestone", "", strings.Join(lines, "\n"))
}

func (r *renderer) why() string {
	w := r.in.Task.Why
	if w == nil {
		return ""
	}
	var parts []string
	if w.Review != nil {
		parts = append(parts, "### Review findings\n"+r.findings(w.Review.Findings, "review", true))
	}
	if len(w.Gate) > 0 {
		parts = append(parts, "### Gate failure\n"+r.gates(w.Gate))
	}
	if w.Integration != nil {
		parts = append(parts, "### Integration failure\n"+r.integration(*w.Integration))
	}
	if w.Triage != nil {
		parts = append(parts, "### Triage note\n"+r.triage(*w.Triage))
	}
	return strings.Join(parts, "\n\n")
}

// severityRank orders findings, the most serious first.
func severityRank(s core.Severity) int {
	return slices.Index([]core.Severity{core.SeverityBlocking, core.SeverityMajor, core.SeverityMinor}, s)
}

// findings lists findings up to the limit. With blockingOnly, the minor ones
// are only counted.
func (r *renderer) findings(fs []Finding, source string, blockingOnly bool) string {
	listed := slices.DeleteFunc(slices.Clone(fs), func(f Finding) bool { return blockingOnly && f.Severity == core.SeverityMinor })
	minor := len(fs) - len(listed)
	slices.SortStableFunc(listed, func(a, b Finding) int { return severityRank(a.Severity) - severityRank(b.Severity) })
	omitted := max(len(listed)-r.limits.Findings, 0)
	listed = listed[:len(listed)-omitted]

	lines := make([]string, len(listed))
	for i, f := range listed {
		head := fmt.Sprintf("%s [%s/%s] %s:%d", f.ID, f.Severity, f.Category, f.File, f.Line)
		if f.Auto {
			head += " (automatic: cannot be contested)"
		}
		lines[i] = "- " + head + "\n  " + f.Message
		if f.Check != "" {
			lines[i] += "\n  check: " + f.Check
		}
	}
	out := r.wrap(source, "findings", "", strings.Join(lines, "\n"))
	if omitted > 0 {
		out += fmt.Sprintf("\n+%d findings omitted", omitted)
	}
	if minor > 0 {
		out += fmt.Sprintf("\n%d minor findings not listed", minor)
	}
	return out
}

func (r *renderer) gates(gs []GateFailure) string {
	var parts []string
	for _, g := range gs {
		var b strings.Builder
		fmt.Fprintf(&b, "Gate `%s` · command `%s` · exit code %d", g.Name, g.Command, g.ExitCode)
		if len(g.Failures) > 0 {
			listed := g.Failures[:min(len(g.Failures), r.limits.TestFailures)]
			lines := make([]string, len(listed))
			for i, f := range listed {
				loc := f.File
				if f.Line > 0 {
					loc += ":" + strconv.Itoa(f.Line)
				}
				lines[i] = fmt.Sprintf("- %s (%s): %s", f.Test, loc, text.Clip(f.Message, r.limits.FailureMessage, truncatedMark))
			}
			b.WriteString("\n" + r.wrap("gate", "failures", "", strings.Join(lines, "\n")))
			if n := len(g.Failures) - len(listed); n > 0 {
				fmt.Fprintf(&b, "\n+%d failures omitted", n)
				if g.Report != "" {
					b.WriteString(" (full report: " + g.Report + ")")
				}
			}
		}
		if g.Log != "" {
			b.WriteString("\n" + r.wrap("gate", "log", "", clipLog(g.Log, g.LogPath, r.limits.LogHead, r.limits.LogTail, r.limits.LogChars)))
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

func (r *renderer) integration(c IntegrationCause) string {
	var parts []string
	if len(c.ConflictFiles) > 0 {
		parts = append(parts, "Rebase conflict in:\n"+r.wrap("git", "conflict-files", "", r.fileList(c.ConflictFiles)))
	}
	if c.Markers != "" {
		parts = append(parts, r.wrap("git", "conflict-markers", "", c.Markers))
	}
	if c.Freshness != "" {
		parts = append(parts, "The milestone changed since the review:\n"+r.wrap("git", "freshness", "", c.Freshness))
	}
	return strings.Join(parts, "\n")
}

func (r *renderer) triage(c TriageCause) string {
	lines := []string{c.Summary}
	for _, k := range slices.Sorted(maps.Keys(c.Params)) {
		lines = append(lines, k+": "+c.Params[k])
	}
	return r.wrap("triage-result", "triage-note", "", strings.Join(lines, "\n"))
}

// fileList lists files up to the limit of changed files.
func (r *renderer) fileList(files []string) string {
	listed := files[:min(len(files), r.limits.ChangedFiles)]
	out := strings.Join(listed, "\n")
	if n := len(files) - len(listed); n > 0 {
		out += fmt.Sprintf("\n+%d files", n)
	}
	return out
}

func (r *renderer) previous() string {
	p := r.in.Task.Previous
	if p == nil {
		return ""
	}
	lines := make([]string, len(p.Facts))
	for i, f := range p.Facts {
		lines[i] = "- " + f
	}
	return fmt.Sprintf("Attempt %d, from the journal:\n", p.Attempt) + r.wrap("journal", "previous-attempt", "", strings.Join(lines, "\n"))
}

func (r *renderer) previousFindings() string {
	fs := r.in.Task.PreviousFindings
	if len(fs) == 0 {
		return ""
	}
	return "Mark each id resolved or open.\n" + r.findings(fs, "review", false)
}

func (r *renderer) report() string {
	rep := r.in.Task.ImplementerReport
	if rep == "" {
		return ""
	}
	return r.wrap("node-result", "implementer-report", "", rep)
}

func (r *renderer) roundDiff() string {
	d := r.in.Task.Diff
	if d == nil {
		return ""
	}
	if d.Text == "" {
		return "The round changed no file."
	}
	lines := strings.Count(strings.TrimRight(d.Text, "\n"), "\n") + 1
	if lines <= r.limits.DiffLines {
		return r.wrap("git", "diff", "", d.Text)
	}
	files := make([]string, len(d.Files))
	for i, f := range d.Files {
		files[i] = fmt.Sprintf("%s (+%d -%d)", f.Path, f.Added, f.Removed)
	}
	return fmt.Sprintf("The diff has %d lines, over the limit of %d; here are the files it touches. Read the rest with your tools.\n%s",
		lines, r.limits.DiffLines, r.wrap("git", "file-list", "", r.fileList(files)))
}

// instructions are specific to the role and to the causes present.
func (r *renderer) instructions() string {
	t := r.in.Task
	var lines []string
	switch r.in.Role.Name {
	case "implementer":
		lines = append(lines, "Implement the node, covering every acceptance criterion with its id.")
	case "reviewer":
		lines = append(lines, "Review the change against every acceptance criterion with its id. Report facts; Kluzo derives the verdict.")
	default:
		lines = append(lines, "Do the work of your role for this node.")
	}
	if w := t.Why; w != nil {
		if w.Review != nil {
			lines = append(lines, "Resolve each finding, or contest it with a justification and evidence in `responses`.")
		}
		if len(w.Gate) > 0 {
			lines = append(lines, "Diagnose the root cause before changing code. Do not weaken the test.")
		}
		if w.Integration != nil {
			lines = append(lines, "Redo the work on top of the current tip of the milestone.")
		}
		if w.Triage != nil {
			lines = append(lines, "Follow the instruction of the triage note.")
		}
	}
	if len(t.PreviousFindings) > 0 {
		lines = append(lines, "Report `previous` for every finding listed above, then review only what changed in this round and what it affects.")
	}
	for i, l := range lines {
		lines[i] = "- " + l
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) deliver() string {
	out := r.in.Role.Output
	msg := fmt.Sprintf("Call `kluzo_result` once, with a JSON object valid for the `%s` schema.", out)
	if req := requiredFields(r.in.Schema); len(req) > 0 {
		msg += " Required fields: " + strings.Join(req, ", ") + "."
	}
	return msg
}

// requiredFields lists the top-level required properties of a JSON Schema.
func requiredFields(schema []byte) []string {
	var s struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	return s.Required
}
