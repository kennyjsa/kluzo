package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Flow is flows/<name>.yaml: an ordered list of steps (ADR 0010).
type Flow struct {
	Name    string `yaml:"-"`
	Version int    `yaml:"version"`
	Steps   []Step `yaml:"steps"`
}

// Step is one flow step. Exactly one of Role, Gate, Approval, MergeQueue, Loop
// or OpenPR identifies its kind; the other keys qualify that kind.
type Step struct {
	ID         string   `yaml:"id"`
	Role       string   `yaml:"role"`
	Phase      string   `yaml:"phase"`
	OnReject   string   `yaml:"on_reject"`
	MaxRounds  int      `yaml:"max_rounds"`
	Interview  bool     `yaml:"interview"`
	Gate       []string `yaml:"gate"`
	Scope      string   `yaml:"scope"`
	OnFail     string   `yaml:"on_fail"`
	MaxRetries int      `yaml:"max_retries"` // fast gates: failures allowed per attempt (default 3)
	Approval   string   `yaml:"approval"`
	MergeQueue string   `yaml:"merge_queue"`
	OpenPR     string   `yaml:"open_pr"`
	Loop       *Loop    `yaml:"loop"`
}

// Loop repeats its steps until the last one leaves no blocking findings.
type Loop struct {
	MaxRounds int    `yaml:"max_rounds"`
	Steps     []Step `yaml:"steps"`
}

// flowNames are the flow files the contract defines (ADR 0010).
var flowNames = []string{"upstream", "node", "closing"}

// stepKinds maps each kind key to the optional keys valid with it.
var stepKinds = map[string][]string{
	"role":        {"phase", "on_reject", "max_rounds", "interview"},
	"gate":        {"scope", "on_fail", "max_retries"},
	"approval":    nil,
	"merge_queue": nil,
	"loop":        nil,
	"open_pr":     nil,
}

// qualifierKeys is every optional step key, for the "not valid on this kind" check.
var qualifierKeys = []string{"phase", "on_reject", "max_rounds", "interview", "scope", "on_fail", "max_retries"}

// flowContext is what a flow is validated against.
type flowContext struct {
	roles map[string]*Role
	// invalidRoles are roles whose file failed to load; they exist, so a flow
	// citing them is not reported a second time.
	invalidRoles map[string]bool
	gates        map[string]bool // gates, for scope node
	milestone    map[string]bool // milestone_gates, for scope milestone
}

// flowValidator walks one flow, tracking the ids already seen.
type flowValidator struct {
	r         report
	ctx       flowContext
	seen      []string // ids of the steps already visited
	seenRoles []string // ids of the role steps already visited
}

func (f *Flow) validate(r report, ctx flowContext) {
	if f.Version != 1 {
		r.add("version", fmt.Sprintf("unsupported flow version %d (want 1)", f.Version))
	}
	if len(f.Steps) == 0 {
		r.add("steps", "a flow needs at least one step")
	}
	v := &flowValidator{r: r, ctx: ctx}
	v.steps("steps", f.Steps, false)
	if f.Name == "node" {
		f.validateNode(r)
	}
}

// validateNode applies the restrictions specific to node.yaml.
func (f *Flow) validateNode(r report) {
	last := len(f.Steps) - 1
	count := 0
	for i := range f.Steps {
		if r.src.has(join(idx("steps", i), "merge_queue")) {
			count++
			if i != last {
				r.add(join(idx("steps", i), "merge_queue"), "the merge_queue step must be the last step of node.yaml")
			}
		}
	}
	if count != 1 {
		r.add("steps", fmt.Sprintf("node.yaml needs exactly one merge_queue step, found %d", count))
	}
}

func (v *flowValidator) steps(path string, steps []Step, inLoop bool) {
	for i, s := range steps {
		v.step(idx(path, i), s, inLoop)
	}
}

func (v *flowValidator) step(p string, s Step, inLoop bool) {
	r := v.r
	switch {
	case s.ID == "":
		r.add(join(p, "id"), "required")
	case slices.Contains(v.seen, s.ID):
		r.add(join(p, "id"), fmt.Sprintf("duplicate step id %q", s.ID))
	}
	kind := v.kind(p)
	if kind == "" {
		return
	}
	for _, q := range qualifierKeys {
		if r.src.has(join(p, q)) && !slices.Contains(stepKinds[kind], q) {
			r.add(join(p, q), fmt.Sprintf("%q is not valid on a %s step", q, kind))
		}
	}
	switch kind {
	case "role":
		v.roleStep(p, s)
	case "gate":
		v.gateStep(p, s)
	case "approval":
		v.literal(p, kind, s.Approval, "plan")
	case "merge_queue":
		v.literal(p, kind, s.MergeQueue, "milestone")
	case "open_pr":
		v.literal(p, kind, s.OpenPR, "main")
	case "loop":
		v.loopStep(p, s, inLoop)
	}
	v.seen = append(v.seen, s.ID)
}

// kind returns the single kind key present in the step, or "" after reporting
// a step with none or several.
func (v *flowValidator) kind(p string) string {
	var found []string
	for _, k := range []string{"role", "gate", "approval", "merge_queue", "loop", "open_pr"} {
		if v.r.src.has(join(p, k)) {
			found = append(found, k)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		v.r.add(p, "step needs one of role, gate, approval, merge_queue, loop or open_pr")
	default:
		v.r.add(p, fmt.Sprintf("step mixes several kinds (%v); use one", found))
	}
	return ""
}

func (v *flowValidator) literal(p, kind, got, want string) {
	if got != want {
		v.r.add(join(p, kind), fmt.Sprintf("unknown value %q (want %s)", got, want))
	}
}

func (v *flowValidator) roleStep(p string, s Step) {
	r := v.r
	if _, ok := v.ctx.roles[s.Role]; !ok && !v.ctx.invalidRoles[s.Role] {
		r.add(join(p, "role"), fmt.Sprintf("role %q does not exist (expected roles/%s.md)", s.Role, s.Role))
	}
	if s.Phase != "" && s.Phase != "running" && s.Phase != "in_review" {
		r.add(join(p, "phase"), fmt.Sprintf("unknown phase %q (want running or in_review)", s.Phase))
	}
	if r.src.has(join(p, "max_rounds")) && s.MaxRounds < 1 {
		r.add(join(p, "max_rounds"), "must be at least 1")
	}
	if r.src.has(join(p, "on_reject")) && !slices.Contains(v.seen, s.OnReject) {
		r.add(join(p, "on_reject"), fmt.Sprintf("%q is not an earlier step of this flow", s.OnReject))
	}
	if ro := v.ctx.roles[s.Role]; s.Phase == "in_review" && ro != nil {
		if bad := forbiddenInReview(ro); len(bad) > 0 {
			r.add(join(p, "role"), fmt.Sprintf("role %q runs in phase in_review, so it must be read-only: remove %s from tools in roles/%s.md (ADR 0020)",
				s.Role, strings.Join(bad, " and "), s.Role))
		}
	}
	v.seenRoles = append(v.seenRoles, s.ID)
}

// forbiddenInReview lists the tools of the role that a read-only role cannot have.
func forbiddenInReview(ro *Role) []string {
	var bad []string
	for _, t := range ro.Tools {
		if slices.Contains(contracts.WriteTools(), t) && !slices.Contains(bad, t) {
			bad = append(bad, strconv.Quote(t))
		}
	}
	return bad
}

func (v *flowValidator) gateStep(p string, s Step) {
	if len(s.Gate) == 0 {
		v.r.add(join(p, "gate"), "list at least one gate")
	}
	known, section := v.ctx.gates, "gates"
	switch s.Scope {
	case "", "node":
	case "milestone":
		known, section = v.ctx.milestone, "milestone_gates"
	default:
		v.r.add(join(p, "scope"), fmt.Sprintf("unknown scope %q (want node or milestone)", s.Scope))
	}
	for i, g := range s.Gate {
		if !known[g] {
			v.r.add(idx(join(p, "gate"), i), fmt.Sprintf("gate %q is not defined in %s of kluzo.yaml", g, section))
		}
	}
	if s.MaxRetries < 0 {
		v.r.add(join(p, "max_retries"), "must not be negative")
	}
	if v.r.src.has(join(p, "on_fail")) && !slices.Contains(v.seenRoles, s.OnFail) {
		v.r.add(join(p, "on_fail"), fmt.Sprintf("%q is not an earlier role step of this flow", s.OnFail))
	}
}

func (v *flowValidator) loopStep(p string, s Step, inLoop bool) {
	if inLoop {
		v.r.add(join(p, "loop"), "a loop cannot be nested")
		return
	}
	if s.Loop == nil {
		v.r.add(join(p, "loop"), "loop needs max_rounds and steps")
		return
	}
	lp := join(p, "loop")
	if s.Loop.MaxRounds < 1 {
		v.r.add(join(lp, "max_rounds"), "required, at least 1")
	}
	if len(s.Loop.Steps) == 0 {
		v.r.add(join(lp, "steps"), "a loop needs at least one step")
	}
	v.steps(join(lp, "steps"), s.Loop.Steps, true)
}
