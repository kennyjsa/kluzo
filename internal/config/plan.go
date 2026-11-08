package config

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
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
	src       *source
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
	// Overrides per role, e.g. implementer: {model: sonnet}. Only harness,
	// model, advisor and effort are accepted; the roles are checked against
	// the project by Plan.CheckProject.
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
	p.src = src
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

// OverrideKeys are the keys a node may override per role (docs/contracts/plan.md).
var OverrideKeys = []string{"harness", "model", "advisor", "effort"}

// CheckProject checks what depends on the project configuration: every area of
// a node exists in areas: of kluzo.yaml (except shared) and every override
// names an existing role, an accepted key and a value that would be valid in
// the role's frontmatter. The errors name the plan file and line.
func (p *Plan) CheckProject(proj *Project) Errors {
	var errs Errors
	r := p.src.report(&errs)
	for i, n := range p.Nodes {
		if n.Excluded {
			continue
		}
		key := idx("nodes", i)
		for j, a := range n.Areas {
			if _, ok := proj.Config.Areas[a]; !ok && a != contracts.SharedArea {
				r.add(idx(join(key, "areas"), j), fmt.Sprintf("area %q is not in areas: of kluzo.yaml (declared: %s; %s needs no entry)", a, strings.Join(slices.Sorted(maps.Keys(proj.Config.Areas)), ", "), contracts.SharedArea))
			}
		}
		for _, name := range slices.Sorted(maps.Keys(n.Overrides)) {
			p.checkOverride(r.under(join(join(key, "overrides"), name)), proj, name, n.Overrides[name])
		}
	}
	return errs.sorted()
}

// checkOverride applies one role override to a copy of the role and validates
// the result the way the role's frontmatter is validated.
func (p *Plan) checkOverride(r report, proj *Project, name string, o map[string]string) {
	role, ok := proj.Roles[name]
	if !ok {
		r.add("", fmt.Sprintf("role %q does not exist in roles/", name))
		return
	}
	eff := *role
	for _, k := range slices.Sorted(maps.Keys(o)) {
		v := o[k]
		switch {
		case !slices.Contains(OverrideKeys, k):
			r.add(k, fmt.Sprintf("unknown override key %q (accepted: %s)", k, strings.Join(OverrideKeys, ", ")))
			continue
		case strings.TrimSpace(v) == "":
			r.add(k, "must not be empty")
			continue
		}
		switch k {
		case "harness":
			eff.Harness = Harness(v)
		case "model":
			eff.Model = v
		case "advisor":
			eff.Advisor = v
		case "effort":
			eff.Effort = v
		}
	}
	eff.validateEffort(r)
	eff.validateModels(r, proj.Config.Defaults)
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

// nonEmpty reports every blank item of the list at path.
func (r report) nonEmpty(path string, items []string, msg string) {
	for i, it := range items {
		if strings.TrimSpace(it) == "" {
			r.add(idx(path, i), msg)
		}
	}
}

func (n PlanNode) validate(r report, key string) {
	if n.Issue < 1 {
		r.add(join(key, "issue"), "required, an issue number")
	}
	if n.Excluded {
		return // a node left out of Kluzo is only a marker
	}
	r.nonEmpty(join(key, "areas"), n.Areas, "must not be empty")
	r.nonEmpty(join(key, "acceptance"), n.Acceptance, "a criterion must not be empty")
	r.nonEmpty(join(key, "subtasks"), n.Subtasks, "must not be empty")
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
