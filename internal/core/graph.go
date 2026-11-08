package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// SharedArea is the implicit area of files outside every declared area (ADR 0011).
const SharedArea = contracts.SharedArea

// Graph errors. Each is the Rule of a Problem; callers use errors.Is.
var (
	ErrCycle       = errors.New("dependency cycle")
	ErrUnknownNode = errors.New("edge references a node outside the milestone")
	ErrSelfEdge    = errors.New("node depends on itself")
	ErrNoArea      = errors.New("node has no area")
	ErrEdgeKind    = errors.New("unsupported plan edge kind")
)

// Edge is a directed relation From → To.
type Edge struct {
	From, To string
	Kind     contracts.EdgeKind
}

// GraphNode is what the scheduler needs to know about a node.
type GraphNode struct {
	ID    string // issue reference
	Title string
	State NodeState
	Areas []string
	// Criteria are the acceptance criteria with their ids (AC1..n, ADR 0023).
	Criteria []Criterion
	// EstimateHours weighs the critical path; zero counts as one unit.
	EstimateHours float64
	// ContractPublished is set by the supervisor when the node's
	// publishes_contract path exists on the milestone branch.
	ContractPublished bool
}

// Graph is a milestone's plan: nodes plus blocks/contract edges.
// Conflict edges are derived from areas and never stored here.
type Graph struct {
	Nodes map[string]GraphNode
	Edges []Edge
}

// Validate reports every structural problem of the plan, joined into one error.
func (g Graph) Validate() error {
	problems := g.problems()
	errs := make([]error, len(problems))
	for i, p := range problems {
		errs[i] = p
	}
	return errors.Join(errs...)
}

// problems lists the structural problems of the graph: nodes without area,
// edges of an unsupported kind, to the node itself or to a node that is not in
// the graph, and, when there is nothing else wrong, one dependency cycle.
func (g Graph) problems() []Problem {
	var out []Problem
	for _, id := range g.ids() {
		if len(g.Nodes[id].Areas) == 0 {
			out = append(out, Problem{ErrNoArea, id, fmt.Sprintf("#%s declares no area; give it at least one (use %q if it touches no declared area)", id, SharedArea)})
		}
	}
	for _, e := range g.Edges {
		switch {
		case e.Kind != contracts.EdgeBlocks && e.Kind != contracts.EdgeContract:
			out = append(out, Problem{ErrEdgeKind, e.To, fmt.Sprintf("%q (#%s → #%s) is not a blocks or contract edge", e.Kind, e.From, e.To)})
		case e.From == e.To:
			out = append(out, Problem{ErrSelfEdge, e.From, fmt.Sprintf("#%s cannot depend on itself", e.From)})
		case !g.has(e.From) || !g.has(e.To):
			out = append(out, Problem{ErrUnknownNode, e.To, fmt.Sprintf("#%s → #%s: one end is not a node of the graph", e.From, e.To)})
		}
	}
	if len(out) == 0 {
		if cycle := g.findCycle(); cycle != nil {
			out = append(out, Problem{ErrCycle, cycle[0], "#" + strings.Join(cycle, " → #") + ": none of them can start before the others are integrated; remove one of these links"})
		}
	}
	return out
}

// Conflicts returns the conflict edges derived from shared areas, each pair
// once with From < To.
func (g Graph) Conflicts() []Edge {
	ids := g.ids()
	var out []Edge
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if shareArea(g.Nodes[a].Areas, g.Nodes[b].Areas) {
				out = append(out, Edge{From: a, To: b, Kind: contracts.EdgeConflict})
			}
		}
	}
	return out
}

// Ready returns the planned nodes whose dependencies are satisfied: every
// blocks predecessor is integrated and every contract predecessor has
// published its contract (or is integrated). Sorted by ID.
func (g Graph) Ready() []string {
	var out []string
	for _, id := range g.ids() {
		if g.Nodes[id].State == NodePlanned && g.depsSatisfied(id) {
			out = append(out, id)
		}
	}
	return out
}

func (g Graph) depsSatisfied(id string) bool {
	for _, e := range g.Edges {
		if e.To != id {
			continue
		}
		from := g.Nodes[e.From]
		switch e.Kind {
		case contracts.EdgeBlocks:
			if from.State != NodeIntegrated {
				return false
			}
		case contracts.EdgeContract:
			if from.State != NodeIntegrated && !from.ContractPublished {
				return false
			}
		}
	}
	return true
}

// CriticalWeights returns, for every node, the weight of the longest chain of
// unfinished work that starts at it, following blocks and contract edges.
// Integrated and cancelled nodes weigh zero. The graph must be acyclic.
func (g Graph) CriticalWeights() map[string]float64 {
	memo := make(map[string]float64, len(g.Nodes))
	var weight func(id string) float64
	weight = func(id string) float64 {
		if w, ok := memo[id]; ok {
			return w
		}
		n := g.Nodes[id]
		own := 0.0
		if n.State != NodeIntegrated && n.State != NodeCancelled {
			own = n.EstimateHours
			if own <= 0 {
				own = 1
			}
		}
		best := 0.0
		for _, e := range g.Edges {
			if e.From == id && e.Kind != contracts.EdgeConflict {
				best = max(best, weight(e.To))
			}
		}
		memo[id] = own + best
		return memo[id]
	}
	for _, id := range g.ids() {
		weight(id)
	}
	return memo
}

// active reports whether a node holds a scheduler slot and its areas.
func active(s NodeState) bool {
	switch s {
	case NodeRunning, NodeInReview, NodeGating, NodeQueued, NodeIntegrating:
		return true
	case NodeBlocked:
		return true // a blocked node keeps its worktree and areas
	}
	return false
}

// NextToDispatch picks the ready nodes to start now. It fills the free slots
// (maxParallel minus active nodes), never picks two nodes that share an area
// or a node that shares an area with an active one, and prefers the heaviest
// critical weight, then the smallest ID. The result is deterministic.
func (g Graph) NextToDispatch(maxParallel int) []string {
	taken := []string{}
	slots := maxParallel
	for _, id := range g.ids() {
		if n := g.Nodes[id]; active(n.State) {
			slots--
			taken = append(taken, n.Areas...)
		}
	}
	if slots <= 0 {
		return nil
	}

	ready := g.Ready()
	weights := g.CriticalWeights()
	sort.SliceStable(ready, func(i, j int) bool {
		if weights[ready[i]] != weights[ready[j]] {
			return weights[ready[i]] > weights[ready[j]]
		}
		return compareRefs(ready[i], ready[j]) < 0
	})

	var picked []string
	for _, id := range ready {
		if len(picked) == slots {
			break
		}
		areas := g.Nodes[id].Areas
		if shareArea(areas, taken) {
			continue
		}
		picked = append(picked, id)
		taken = append(taken, areas...)
	}
	return picked
}

func (g Graph) has(id string) bool { _, ok := g.Nodes[id]; return ok }

func (g Graph) ids() []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, compareRefs)
	return ids
}

// compareRefs orders issue references for display and for the scheduler's
// tie-break: numeric references first, by number (creation order in the
// tracker, so 9 comes before 10), then the others as text. Equal numbers
// (such as "7" and "07") fall back to text so the order stays total.
func compareRefs(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)
	switch {
	case errA == nil && errB == nil:
		return cmp.Or(cmp.Compare(na, nb), strings.Compare(a, b))
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// findCycle returns one cycle as a path (first node repeated at the end), or nil.
func (g Graph) findCycle() []string {
	const (
		white = iota
		grey
		black
	)
	color := make(map[string]int, len(g.Nodes))
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		color[id] = grey
		stack = append(stack, id)
		for _, e := range g.Edges {
			if e.From != id || e.Kind == contracts.EdgeConflict {
				continue
			}
			switch color[e.To] {
			case grey:
				start := slices.Index(stack, e.To)
				return append(slices.Clone(stack[start:]), e.To)
			case white:
				if c := visit(e.To); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}
	for _, id := range g.ids() {
		if color[id] == white {
			if c := visit(id); c != nil {
				return c
			}
		}
	}
	return nil
}

func shareArea(a, b []string) bool {
	for _, x := range a {
		if x == SharedArea {
			continue
		}
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
