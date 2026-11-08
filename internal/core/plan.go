package core

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Plan errors, in addition to the graph errors. Each is the Rule of a Problem.
var (
	ErrNotInPlan          = errors.New("milestone issue missing from the plan")
	ErrNotInMilestone     = errors.New("plan node is not an issue of the milestone")
	ErrExcludedDependency = errors.New("dependency on an issue excluded from the plan")
	ErrDivergentEdge      = errors.New("plan and tracker disagree on a dependency")
	ErrNoAcceptance       = errors.New("node has no acceptance criterion")
	ErrClosedNode         = errors.New("plan node is a closed issue")
)

// Problem is one reason a plan cannot start. Rule is the sentinel to match
// with errors.Is; Detail explains it to the person who has to fix it.
type Problem struct {
	Rule   error
	Node   string // issue the problem is about; empty when it is about none
	Detail string
}

func (p Problem) Error() string { return p.Rule.Error() + ": " + p.Detail }
func (p Problem) Unwrap() error { return p.Rule }

// Criterion is one acceptance criterion with its stable id (ADR 0023).
type Criterion struct {
	ID   string // "AC1", "AC2"… in the order of the plan
	Text string
}

// PlanNode is a node as the approved plan declares it (docs/contracts/plan.md),
// already validated for shape by internal/config.
type PlanNode struct {
	Issue      string // tracker reference; unique in the plan
	Title      string
	Areas      []string
	BlocksOn   []string
	ContractOn []string
	Acceptance []string
	// Excluded marks a milestone issue Kluzo must leave alone.
	Excluded bool
}

// edgeKey identifies a dependency edge.
type edgeKey struct {
	from, to string
	kind     contracts.EdgeKind
}

// GraphFromPlan builds the milestone graph from the plan, the issues of the
// milestone and the dependencies the tracker holds (incoming edges of each
// issue). Every node starts planned. The edges are the union of the plan and
// the tracker. It returns every problem found, and the graph is fit to start
// only when there are none; a divergence leaves the union in the graph so the
// approval can sync the tracker with the plan.
//
// Rules (docs/contracts/plan.md): every open issue of the milestone is in the
// plan or excluded (a closed one stays out without being listed, and a plan
// node that points to it is an error); every plan node is an issue of the
// milestone; dependencies stay inside the milestone and on non-excluded open
// issues, while a dependency on a closed issue is satisfied and its edge is
// ignored; plan and tracker agree;
// no cycles; every node has an area and at least one acceptance criterion.
// Conflicts are not stored: Graph.Conflicts derives them from the areas, and
// the shared area never conflicts.
func GraphFromPlan(plan []PlanNode, issues []ports.Issue, deps []ports.Dependency) (Graph, []Problem) {
	var problems []Problem
	inMilestone, closed := map[string]bool{}, map[string]bool{}
	for _, is := range issues {
		inMilestone[is.Ref] = true
		closed[is.Ref] = is.State == ports.IssueClosed
	}
	planned := make(map[string]PlanNode, len(plan))
	for _, n := range plan {
		planned[n.Issue] = n
	}

	for _, is := range issues {
		if _, ok := planned[is.Ref]; !ok && !closed[is.Ref] {
			problems = append(problems, Problem{ErrNotInPlan, is.Ref, fmt.Sprintf("#%s (%s) is in the milestone but not in the plan: add it as a node, or list it with excluded: true", is.Ref, is.Title)})
		}
	}

	g := Graph{Nodes: map[string]GraphNode{}}
	for _, n := range plan {
		if !inMilestone[n.Issue] {
			problems = append(problems, Problem{ErrNotInMilestone, n.Issue, fmt.Sprintf("#%s is in the plan but not in the milestone: move the issue to the milestone or remove the node", n.Issue)})
			continue
		}
		if closed[n.Issue] {
			problems = append(problems, Problem{ErrClosedNode, n.Issue, fmt.Sprintf("#%s is closed: remove it from the plan, or reopen the issue", n.Issue)})
			continue
		}
		if n.Excluded {
			continue
		}
		if len(n.Acceptance) == 0 {
			problems = append(problems, Problem{ErrNoAcceptance, n.Issue, fmt.Sprintf("#%s has no acceptance criterion: list at least one under acceptance (copy them from the issue)", n.Issue)})
		}
		g.Nodes[n.Issue] = GraphNode{ID: n.Issue, Title: n.Title, State: NodePlanned, Areas: n.Areas, Criteria: criteria(n.Acceptance)}
	}

	fromPlan, fromTracker := map[edgeKey]bool{}, map[edgeKey]bool{}
	// reject records the problem of an edge whose origin cannot block a node
	// of the graph, and reports whether it did.
	reject := func(e edgeKey, where string) bool {
		switch {
		case !inMilestone[e.from]:
			problems = append(problems, Problem{ErrUnknownNode, e.to, fmt.Sprintf("#%s depends on #%s (%s), which is not in the milestone: only issues of the milestone can block a node; move #%[2]s into the milestone or remove the dependency", e.to, e.from, where)})
		case planned[e.from].Excluded || !g.has(e.from):
			problems = append(problems, Problem{ErrExcludedDependency, e.to, fmt.Sprintf("#%s depends on #%s (%s), which is not a node of the plan (excluded or missing): remove the dependency, or include #%[2]s", e.to, e.from, where)})
		default:
			return false
		}
		return true
	}
	for _, n := range plan {
		if !g.has(n.Issue) {
			continue
		}
		for _, e := range planEdges(n) {
			if closed[e.from] {
				continue // already integrated elsewhere: the dependency is met
			}
			if !reject(e, "plan") {
				fromPlan[e] = true
			}
		}
	}
	for _, d := range deps {
		if !g.has(d.ToRef) {
			continue // the edge is about an excluded issue or one outside the plan
		}
		if closed[d.FromRef] {
			continue
		}
		if e := (edgeKey{d.FromRef, d.ToRef, d.Kind}); !reject(e, "tracker") {
			fromTracker[e] = true
		}
	}

	all := map[edgeKey]bool{}
	for e := range fromPlan {
		all[e] = true
	}
	for e := range fromTracker {
		all[e] = true
	}
	for _, e := range sortedEdges(all) {
		g.Edges = append(g.Edges, Edge{From: e.from, To: e.to, Kind: e.kind})
		switch {
		case e.from == e.to: // reported as a self edge by the graph
		case !fromTracker[e]:
			problems = append(problems, Problem{ErrDivergentEdge, e.to, fmt.Sprintf("#%s → #%s (%s) is in the plan but not in the tracker: approving the plan syncs the tracker", e.from, e.to, e.kind)})
		case !fromPlan[e]:
			problems = append(problems, Problem{ErrDivergentEdge, e.to, fmt.Sprintf("#%s → #%s (%s) is in the tracker but not in the plan: add it to the plan, or remove the link in the tracker", e.from, e.to, e.kind)})
		}
	}
	return g, append(problems, g.problems()...)
}

// planEdges lists the incoming edges a plan node declares, blocks first.
func planEdges(n PlanNode) []edgeKey {
	var out []edgeKey
	for _, from := range n.BlocksOn {
		out = append(out, edgeKey{from, n.Issue, contracts.EdgeBlocks})
	}
	for _, from := range n.ContractOn {
		out = append(out, edgeKey{from, n.Issue, contracts.EdgeContract})
	}
	return out
}

// criteria numbers the acceptance texts AC1..n in order.
func criteria(texts []string) []Criterion {
	out := make([]Criterion, len(texts))
	for i, t := range texts {
		out[i] = Criterion{ID: "AC" + strconv.Itoa(i+1), Text: t}
	}
	return out
}

func sortedEdges(set map[edgeKey]bool) []edgeKey {
	out := make([]edgeKey, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.to < b.to || a.to == b.to && (a.from < b.from || a.from == b.from && a.kind < b.kind)
	})
	return out
}
