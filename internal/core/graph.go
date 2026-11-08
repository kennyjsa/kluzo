package core

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// SharedArea is the implicit area of files outside every declared area; it
// never creates conflicts (ADR 0011).
const SharedArea = "shared"

// Graph errors.
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
	State NodeState
	Areas []string
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
	var errs []error
	for _, id := range g.ids() {
		if len(g.Nodes[id].Areas) == 0 {
			errs = append(errs, fmt.Errorf("%w: %s", ErrNoArea, id))
		}
	}
	for _, e := range g.Edges {
		switch {
		case e.Kind != contracts.EdgeBlocks && e.Kind != contracts.EdgeContract:
			errs = append(errs, fmt.Errorf("%w: %q (%s → %s)", ErrEdgeKind, e.Kind, e.From, e.To))
		case e.From == e.To:
			errs = append(errs, fmt.Errorf("%w: %s", ErrSelfEdge, e.From))
		case !g.has(e.From) || !g.has(e.To):
			errs = append(errs, fmt.Errorf("%w: %s → %s", ErrUnknownNode, e.From, e.To))
		}
	}
	if len(errs) == 0 {
		if cycle := g.findCycle(); cycle != nil {
			errs = append(errs, fmt.Errorf("%w: %s", ErrCycle, strings.Join(cycle, " → ")))
		}
	}
	return errors.Join(errs...)
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
		return ready[i] < ready[j]
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
	sort.Strings(ids)
	return ids
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
