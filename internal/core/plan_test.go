package core

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// planFixture is the agenda milestone of docs/contracts/plan.md without the
// contract edges: #41 → #42 → #45 → #47 and #46 → #47.
func planFixture() (plan []PlanNode, issues []ports.Issue, deps []ports.Dependency) {
	ac := []string{"works", "is tested"}
	plan = []PlanNode{
		{Issue: "41", Title: "Schema", Areas: []string{"agenda"}, Acceptance: ac},
		{Issue: "42", Title: "API", Areas: []string{"agenda"}, BlocksOn: []string{"41"}, Acceptance: ac},
		{Issue: "45", Title: "Reminder", Areas: []string{"notify"}, BlocksOn: []string{"42"}, Acceptance: ac},
		{Issue: "46", Title: "ACL", Areas: []string{SharedArea}, Acceptance: ac},
		{Issue: "47", Title: "E2E", Areas: []string{SharedArea}, BlocksOn: []string{"45", "46"}, Acceptance: ac},
	}
	for _, n := range plan {
		issues = append(issues, ports.Issue{Ref: n.Issue, Title: n.Title})
		for _, from := range n.BlocksOn {
			deps = append(deps, ports.Dependency{FromRef: from, ToRef: n.Issue, Kind: contracts.EdgeBlocks, Source: ports.SourceNative})
		}
	}
	return plan, issues, deps
}

func TestGraphFromPlanAcceptsAValidPlan(t *testing.T) {
	plan, issues, deps := planFixture()
	g, problems := GraphFromPlan(plan, issues, deps)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	wantEdges := []Edge{
		{"41", "42", contracts.EdgeBlocks}, {"42", "45", contracts.EdgeBlocks},
		{"45", "47", contracts.EdgeBlocks}, {"46", "47", contracts.EdgeBlocks},
	}
	if !reflect.DeepEqual(g.Edges, wantEdges) {
		t.Errorf("edges = %v, want %v", g.Edges, wantEdges)
	}
	for id, n := range g.Nodes {
		if n.State != NodePlanned {
			t.Errorf("node %s starts %s, want planned", id, n.State)
		}
	}
	if got, want := g.Nodes["42"].Criteria, []Criterion{{"AC1", "works"}, {"AC2", "is tested"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("criteria = %v, want %v", got, want)
	}
	if got := g.Nodes["42"].Title; got != "API" {
		t.Errorf("title = %q", got)
	}
}

// Conflicts come from the areas; the shared area never conflicts.
func TestGraphFromPlanDerivesConflictsFromAreas(t *testing.T) {
	plan, issues, deps := planFixture()
	g, problems := GraphFromPlan(plan, issues, deps)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := []Edge{{"41", "42", contracts.EdgeConflict}} // 46 and 47 share only "shared"
	if got := g.Conflicts(); !reflect.DeepEqual(got, want) {
		t.Errorf("conflicts = %v, want %v", got, want)
	}
}

func TestGraphFromPlanRules(t *testing.T) {
	blocks := func(from, to string) ports.Dependency {
		return ports.Dependency{FromRef: from, ToRef: to, Kind: contracts.EdgeBlocks, Source: ports.SourceNative}
	}
	tests := []struct {
		name   string
		mutate func(plan *[]PlanNode, issues *[]ports.Issue, deps *[]ports.Dependency)
		want   []error // every rule that must be reported, in order; nil means none
	}{
		{"issue missing from the plan", func(_ *[]PlanNode, is *[]ports.Issue, _ *[]ports.Dependency) {
			*is = append(*is, ports.Issue{Ref: "48", Title: "forgotten"})
		}, []error{ErrNotInPlan}},
		{"issue excluded from the plan", func(p *[]PlanNode, is *[]ports.Issue, _ *[]ports.Dependency) {
			*p = append(*p, PlanNode{Issue: "48", Excluded: true})
			*is = append(*is, ports.Issue{Ref: "48"})
		}, nil},
		{"closed issue outside the plan is not an error", func(_ *[]PlanNode, is *[]ports.Issue, _ *[]ports.Dependency) {
			*is = append(*is, ports.Issue{Ref: "48", State: ports.IssueClosed})
		}, nil},
		{"plan node pointing to a closed issue", func(_ *[]PlanNode, is *[]ports.Issue, _ *[]ports.Dependency) {
			(*is)[4].State = ports.IssueClosed
		}, []error{ErrClosedNode}},
		{"dependency on a closed issue is met and its edge ignored", func(p *[]PlanNode, is *[]ports.Issue, d *[]ports.Dependency) {
			*is = append(*is, ports.Issue{Ref: "40", State: ports.IssueClosed})
			(*p)[0].BlocksOn = []string{"40"}
			*d = append(*d, blocks("40", "41"))
		}, nil},
		{"tracker dependency on a closed issue absent from the plan is ignored", func(_ *[]PlanNode, is *[]ports.Issue, d *[]ports.Dependency) {
			*is = append(*is, ports.Issue{Ref: "40", State: ports.IssueClosed})
			*d = append(*d, blocks("40", "41"))
		}, nil},
		{"excluded entry pointing to a closed issue is harmless", func(p *[]PlanNode, is *[]ports.Issue, _ *[]ports.Dependency) {
			*p = append(*p, PlanNode{Issue: "48", Excluded: true})
			*is = append(*is, ports.Issue{Ref: "48", State: ports.IssueClosed})
		}, nil},
		{"plan node outside the milestone", func(p *[]PlanNode, _ *[]ports.Issue, _ *[]ports.Dependency) {
			*p = append(*p, PlanNode{Issue: "99", Areas: []string{"a"}, Acceptance: []string{"x"}})
		}, []error{ErrNotInMilestone}},
		{"node without acceptance", func(p *[]PlanNode, _ *[]ports.Issue, _ *[]ports.Dependency) {
			(*p)[0].Acceptance = nil
		}, []error{ErrNoAcceptance}},
		{"excluded node needs no acceptance or area", func(p *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			(*p)[3] = PlanNode{Issue: "46", Excluded: true}
			*d = slices.DeleteFunc(*d, func(x ports.Dependency) bool { return x.FromRef == "46" })
			(*p)[4].BlocksOn = []string{"45"}
		}, nil},
		{"node without area", func(p *[]PlanNode, _ *[]ports.Issue, _ *[]ports.Dependency) {
			(*p)[0].Areas = nil
		}, []error{ErrNoArea}},
		{"plan depends on an issue outside the milestone", func(p *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			(*p)[0].BlocksOn = []string{"7"}
			*d = append(*d, blocks("7", "41"))
		}, []error{ErrUnknownNode, ErrUnknownNode}},
		{"tracker depends on an issue outside the milestone", func(_ *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			*d = append(*d, blocks("7", "41"))
		}, []error{ErrUnknownNode}},
		{"dependency on an excluded issue", func(p *[]PlanNode, is *[]ports.Issue, d *[]ports.Dependency) {
			*p = append(*p, PlanNode{Issue: "48", Excluded: true})
			*is = append(*is, ports.Issue{Ref: "48"})
			(*p)[0].BlocksOn = []string{"48"}
			*d = append(*d, blocks("48", "41"))
		}, []error{ErrExcludedDependency, ErrExcludedDependency}},
		{"tracker link of an excluded issue to a node is ignored when it is the target", func(p *[]PlanNode, is *[]ports.Issue, d *[]ports.Dependency) {
			*p = append(*p, PlanNode{Issue: "48", Excluded: true})
			*is = append(*is, ports.Issue{Ref: "48"})
			*d = append(*d, blocks("41", "48"))
		}, nil},
		{"edge in the plan but not in the tracker", func(_ *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			*d = slices.DeleteFunc(*d, func(x ports.Dependency) bool { return x.FromRef == "41" })
		}, []error{ErrDivergentEdge}},
		{"edge in the tracker but not in the plan", func(_ *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			*d = append(*d, blocks("41", "46"))
		}, []error{ErrDivergentEdge}},
		{"a contract link is compared like a blocks link", func(p *[]PlanNode, _ *[]ports.Issue, _ *[]ports.Dependency) {
			(*p)[1].ContractOn = []string{"46"}
		}, []error{ErrDivergentEdge}},
		{"cycle inside the plan", func(p *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			(*p)[0].BlocksOn = []string{"47"}
			*d = append(*d, blocks("47", "41"))
		}, []error{ErrCycle}},
		{"cycle only in the union of plan and tracker", func(_ *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			*d = append(*d, blocks("42", "41"))
		}, []error{ErrDivergentEdge, ErrCycle}},
		{"node that depends on itself", func(p *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			(*p)[0].BlocksOn = []string{"41"}
			*d = append(*d, blocks("41", "41"))
		}, []error{ErrSelfEdge}},
		{"the same link in the plan and in the tracker counts once", func(_ *[]PlanNode, _ *[]ports.Issue, d *[]ports.Dependency) {
			*d = append(*d, (*d)[0])
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, issues, deps := planFixture()
			tt.mutate(&plan, &issues, &deps)
			_, problems := GraphFromPlan(plan, issues, deps)
			var got []error
			for _, p := range problems {
				got = append(got, p.Rule)
			}
			if !slices.EqualFunc(got, tt.want, errors.Is) {
				t.Errorf("rules = %v, want %v\nproblems:\n%s", got, tt.want, describe(problems))
			}
			for _, p := range problems {
				if p.Detail == "" || !errors.Is(p, p.Rule) {
					t.Errorf("problem %+v needs a detail and must match its rule", p)
				}
			}
		})
	}
}

// A divergence keeps the union in the graph, so the approval can sync the tracker.
func TestGraphFromPlanKeepsTheUnionOnDivergence(t *testing.T) {
	plan, issues, deps := planFixture()
	deps = slices.DeleteFunc(deps, func(d ports.Dependency) bool { return d.FromRef == "41" })
	deps = append(deps, ports.Dependency{FromRef: "41", ToRef: "46", Kind: contracts.EdgeBlocks})
	g, problems := GraphFromPlan(plan, issues, deps)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want the two divergences", problems)
	}
	for _, e := range []Edge{{"41", "42", contracts.EdgeBlocks}, {"41", "46", contracts.EdgeBlocks}} {
		if !slices.Contains(g.Edges, e) {
			t.Errorf("union lacks %v: %v", e, g.Edges)
		}
	}
}

func TestGraphFromPlanExplainsTheCycle(t *testing.T) {
	plan, issues, deps := planFixture()
	plan[0].BlocksOn = []string{"47"}
	deps = append(deps, ports.Dependency{FromRef: "47", ToRef: "41", Kind: contracts.EdgeBlocks})
	_, problems := GraphFromPlan(plan, issues, deps)
	if len(problems) != 1 || !errors.Is(problems[0], ErrCycle) {
		t.Fatalf("problems = %v", problems)
	}
	if got, want := problems[0].Detail, "#41 → #42 → #45 → #47 → #41: none of them can start before the others are integrated; remove one of these links"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
}

// The same input always yields the same problems in the same order.
func TestGraphFromPlanIsDeterministic(t *testing.T) {
	plan, issues, deps := planFixture()
	plan[0].BlocksOn = []string{"7", "8"}
	plan[0].Acceptance = nil
	issues = append(issues, ports.Issue{Ref: "48"})
	want := describeAll(plan, issues, deps)
	for range 20 {
		if got := describeAll(plan, issues, deps); got != want {
			t.Fatalf("problems changed between runs:\n%s\n---\n%s", want, got)
		}
	}
}

func describeAll(plan []PlanNode, issues []ports.Issue, deps []ports.Dependency) string {
	_, problems := GraphFromPlan(plan, issues, deps)
	return describe(problems)
}

func describe(problems []Problem) string {
	var s string
	for _, p := range problems {
		s += fmt.Sprintf("  - %v\n", p)
	}
	return s
}
