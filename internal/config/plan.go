package config

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// PlansDir is the directory of .kluzo/ that holds the approved plans.
const PlansDir = "plans"

// Plan is .kluzo/plans/<milestone>.yaml, the approved graph of a milestone
// (docs/contracts/plan.md).
type Plan struct {
	Version     int        `yaml:"version"`
	PlanVersion int        `yaml:"plan_version"`
	Milestone   string     `yaml:"milestone"`
	ApprovedBy  string     `yaml:"approved_by"`
	ApprovedAt  time.Time  `yaml:"approved_at"`
	Profile     string     `yaml:"profile"`
	Budget      PlanBudget `yaml:"budget"`
	Nodes       []PlanNode `yaml:"nodes"`
	NewIssues   []int      `yaml:"new_issues"`

	file      string
	lines     map[int]int // issue number -> line of its entry
	milestone int         // line of the milestone key
}

// PlanBudget limits the spending of the milestone.
type PlanBudget struct {
	USD float64 `yaml:"usd"`
}

// PlanNode is one issue of the plan.
type PlanNode struct {
	Issue             int      `yaml:"issue"`
	Title             string   `yaml:"title"`
	Areas             []string `yaml:"areas"`
	PublishesContract string   `yaml:"publishes_contract"`
	BlocksOn          []int    `yaml:"blocks_on"`
	ContractOn        []int    `yaml:"contract_on"`
	// Acceptance lists the criteria; the n-th is AC<n> (ADR 0023).
	Acceptance []string   `yaml:"acceptance"`
	Subtasks   []string   `yaml:"subtasks"`
	Budget     NodeBudget `yaml:"budget"`
	// Overrides per role, e.g. implementer: {model: sonnet}. The roles and
	// keys are checked where the precedence is applied, not here.
	Overrides map[string]map[string]string `yaml:"overrides"`
	// Excluded marks a milestone issue that Kluzo must leave alone.
	Excluded bool `yaml:"excluded"`
}

// NodeLine returns the line of the plan entry of the issue, or of the plan
// file's first line when the issue is not in the plan.
func (p *Plan) NodeLine(issue int) int {
	if l, ok := p.lines[issue]; ok {
		return l
	}
	return 1
}

// File is the path the plan was read from.
func (p *Plan) File() string { return p.file }

// MilestoneLine is the line of the milestone key.
func (p *Plan) MilestoneLine() int { return p.milestone }

// PlanPath returns the file of the plan of a milestone inside the config directory dir.
func PlanPath(dir, milestone string) string {
	return filepath.Join(dir, PlansDir, milestone+".yaml")
}

// LoadPlan reads and validates the plan of the milestone (its title in the
// tracker) in dir. The returned error is an Errors value listing every problem
// found, with file and line, or nil.
func LoadPlan(dir, milestone string) (*Plan, error) {
	l := &loader{dir: dir}
	if milestone == "" || milestone != filepath.Base(milestone) || strings.ContainsAny(milestone, `/\`) || milestone == ".." {
		return nil, Errors{{File: l.path(PlansDir), Msg: fmt.Sprintf("milestone %q cannot name a plan file", milestone)}}
	}
	file := PlanPath(dir, milestone)
	data, ok := l.read(file)
	if !ok {
		return nil, l.errs.sorted().orNil()
	}
	p := &Plan{file: file}
	src, errs := decode(file, []byte(data), 0, p)
	if errs != nil {
		return nil, errs
	}
	p.validate(src.report(&l.errs), milestone)
	p.index(src)
	if err := l.errs.sorted().orNil(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Plan) index(src *source) {
	p.lines = map[int]int{}
	p.milestone = src.line("milestone")
	for i, n := range p.Nodes {
		if _, dup := p.lines[n.Issue]; !dup {
			p.lines[n.Issue] = src.line(idx("nodes", i))
		}
	}
}

// validate checks the shape of the file. What depends on the tracker (every
// issue covered, dependencies inside the milestone, agreement with the
// tracker) is judged by core.GraphFromPlan.
func (p *Plan) validate(r report, name string) {
	if p.Version != 1 {
		r.add("version", fmt.Sprintf("unsupported plan version %d (want 1)", p.Version))
	}
	if p.PlanVersion < 1 {
		r.add("plan_version", "required, at least 1")
	}
	switch {
	case p.Milestone == "":
		r.add("milestone", "required")
	case p.Milestone != name:
		r.add("milestone", fmt.Sprintf("is %q but the file is the plan of %q", p.Milestone, name))
	}
	if p.Budget.USD < 0 {
		r.add("budget.usd", "must not be negative")
	}
	if len(p.Nodes) == 0 {
		r.add("nodes", "a plan needs at least one node")
	}
	for i, n := range p.NewIssues {
		if n < 1 {
			r.add(idx("new_issues", i), "must be an issue number")
		}
	}
	first := map[int]int{}
	for i, n := range p.Nodes {
		key := idx("nodes", i)
		if prev, dup := first[n.Issue]; dup && n.Issue >= 1 {
			r.add(join(key, "issue"), fmt.Sprintf("duplicate issue #%d (first at nodes[%d])", n.Issue, prev))
		}
		first[n.Issue] = i
		n.validate(r, key)
	}
}

func (n PlanNode) validate(r report, key string) {
	if n.Issue < 1 {
		r.add(join(key, "issue"), "required, an issue number")
	}
	if n.Excluded {
		return // a node left out of Kluzo is only a marker
	}
	for i, a := range n.Areas {
		if strings.TrimSpace(a) == "" {
			r.add(idx(join(key, "areas"), i), "must not be empty")
		}
	}
	for i, c := range n.Acceptance {
		if strings.TrimSpace(c) == "" {
			r.add(idx(join(key, "acceptance"), i), "a criterion must not be empty")
		}
	}
	for i, s := range n.Subtasks {
		if strings.TrimSpace(s) == "" {
			r.add(idx(join(key, "subtasks"), i), "must not be empty")
		}
	}
	for field, refs := range map[string][]int{"blocks_on": n.BlocksOn, "contract_on": n.ContractOn} {
		for i, ref := range refs {
			if ref < 1 {
				r.add(idx(join(key, field), i), "must be an issue number")
			}
		}
	}
	if c := n.PublishesContract; c != "" && (path.IsAbs(c) || path.Clean(c) != c || strings.HasPrefix(c, "../") || c == "..") {
		r.add(join(key, "publishes_contract"), fmt.Sprintf("%q must be a clean path relative to the repository", c))
	}
	if b := n.Budget; b.Attempts < 0 || b.Minutes < 0 || b.USD < 0 {
		r.add(join(key, "budget"), "limits must not be negative")
	}
}
