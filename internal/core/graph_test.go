package core

import (
	"errors"
	"slices"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// agenda builds the example milestone used across the docs and the prototype:
// #41 → #42 → #45 → #47 is the critical path; #43 publishes a contract for #44.
func agenda() Graph {
	n := func(id string, s NodeState, areas ...string) GraphNode {
		return GraphNode{ID: id, State: s, Areas: areas}
	}
	return Graph{
		Nodes: map[string]GraphNode{
			"41": n("41", NodePlanned, "agenda"),
			"42": n("42", NodePlanned, "agenda"),
			"43": n("43", NodePlanned, "api-contract"),
			"44": n("44", NodePlanned, "web-agenda"),
			"45": n("45", NodePlanned, "notify"),
			"46": n("46", NodePlanned, "agenda-acl"),
			"47": n("47", NodePlanned, "e2e"),
		},
		Edges: []Edge{
			{"41", "42", contracts.EdgeBlocks},
			{"43", "44", contracts.EdgeContract},
			{"44", "46", contracts.EdgeContract},
			{"42", "45", contracts.EdgeBlocks},
			{"45", "47", contracts.EdgeBlocks},
			{"46", "47", contracts.EdgeBlocks},
		},
	}
}

func set(g Graph, id string, s NodeState) {
	n := g.Nodes[id]
	n.State = s
	g.Nodes[id] = n
}

func TestValidate(t *testing.T) {
	if err := agenda().Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	cyclic := agenda()
	cyclic.Edges = append(cyclic.Edges, Edge{"47", "41", contracts.EdgeBlocks})
	if err := cyclic.Validate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle not detected: %v", err)
	}

	broken := agenda()
	broken.Edges = append(broken.Edges,
		Edge{"99", "41", contracts.EdgeBlocks},
		Edge{"42", "42", contracts.EdgeBlocks},
		Edge{"41", "43", contracts.EdgeConflict},
	)
	n := broken.Nodes["43"]
	n.Areas = nil
	broken.Nodes["43"] = n
	err := broken.Validate()
	for _, want := range []error{ErrUnknownNode, ErrSelfEdge, ErrEdgeKind, ErrNoArea} {
		if !errors.Is(err, want) {
			t.Errorf("Validate() = %v, want it to include %v", err, want)
		}
	}
}

func TestReady(t *testing.T) {
	g := agenda()
	if got, want := g.Ready(), []string{"41", "43"}; !slices.Equal(got, want) {
		t.Fatalf("Ready() = %v, want %v", got, want)
	}

	// #42 waits until #41 is integrated, not merely reviewed.
	set(g, "41", NodeGating)
	if slices.Contains(g.Ready(), "42") {
		t.Fatal("#42 ready before #41 is integrated")
	}
	set(g, "41", NodeIntegrated)
	if !slices.Contains(g.Ready(), "42") {
		t.Fatal("#42 not ready after #41 integrated")
	}

	// #44 only needs #43's contract published.
	n := g.Nodes["43"]
	n.State, n.ContractPublished = NodeRunning, true
	g.Nodes["43"] = n
	if !slices.Contains(g.Ready(), "44") {
		t.Fatal("#44 not ready after #43 published its contract")
	}
}

func TestCriticalWeights(t *testing.T) {
	g := agenda()
	w := g.CriticalWeights()
	// 41 → 42 → 45 → 47 = 4 units; 43 → 44 → 46 → 47 = 4 units.
	if w["41"] != 4 || w["43"] != 4 || w["47"] != 1 {
		t.Fatalf("weights = %v", w)
	}
	n := g.Nodes["42"]
	n.EstimateHours = 5
	g.Nodes["42"] = n
	set(g, "41", NodeIntegrated)
	if w := g.CriticalWeights(); w["41"] != 7 || w["42"] != 7 {
		t.Fatalf("weights after estimate = %v, want 41 = 7 (integrated counts 0), 42 = 7", w)
	}
}

func TestNextToDispatch(t *testing.T) {
	g := agenda()

	// v0.1: one at a time. Ties on weight go to the smallest ID.
	if got := g.NextToDispatch(1); !slices.Equal(got, []string{"41"}) {
		t.Fatalf("NextToDispatch(1) = %v, want [41]", got)
	}
	// With room for three, both ready nodes start.
	if got := g.NextToDispatch(3); !slices.Equal(got, []string{"41", "43"}) {
		t.Fatalf("NextToDispatch(3) = %v, want [41 43]", got)
	}
	// An active node uses a slot.
	set(g, "41", NodeRunning)
	if got := g.NextToDispatch(1); got != nil {
		t.Fatalf("NextToDispatch(1) with one active = %v, want none", got)
	}
}

func TestDispatchRespectsAreas(t *testing.T) {
	g := Graph{Nodes: map[string]GraphNode{
		"1": {ID: "1", State: NodeRunning, Areas: []string{"agenda"}},
		"2": {ID: "2", State: NodePlanned, Areas: []string{"agenda"}},
		"3": {ID: "3", State: NodePlanned, Areas: []string{"auth", SharedArea}},
		"4": {ID: "4", State: NodePlanned, Areas: []string{"auth"}},
		"5": {ID: "5", State: NodePlanned, Areas: []string{SharedArea}},
	}}
	got := g.NextToDispatch(10)
	// 2 conflicts with the active 1; 4 conflicts with 3 (picked first by ID);
	// shared never conflicts.
	if want := []string{"3", "5"}; !slices.Equal(got, want) {
		t.Fatalf("NextToDispatch = %v, want %v", got, want)
	}
	if c := g.Conflicts(); len(c) != 2 {
		t.Fatalf("Conflicts() = %v, want 1–2 (agenda) and 3–4 (auth)", c)
	}
}

func TestBlockedNodeKeepsItsArea(t *testing.T) {
	g := Graph{Nodes: map[string]GraphNode{
		"1": {ID: "1", State: NodeBlocked, Areas: []string{"agenda"}},
		"2": {ID: "2", State: NodePlanned, Areas: []string{"agenda"}},
	}}
	if got := g.NextToDispatch(5); got != nil {
		t.Fatalf("dispatched %v into the area of a blocked node", got)
	}
}

// References are compared as numbers when both are numeric: 9 before 10.
func TestNextToDispatchTieBreaksNumerically(t *testing.T) {
	g := Graph{Nodes: map[string]GraphNode{}}
	for _, id := range []string{"10", "9", "100", "x"} {
		g.Nodes[id] = GraphNode{ID: id, State: NodePlanned, Areas: []string{SharedArea}}
	}
	g.Nodes["11"] = GraphNode{ID: "11", State: NodeReady, Areas: []string{SharedArea}} // already moved to ready
	if got, want := g.NextToDispatch(10), []string{"9", "10", "11", "100", "x"}; !slices.Equal(got, want) {
		t.Errorf("NextToDispatch() = %v, want %v", got, want)
	}
	if got, want := g.Ready(), []string{"9", "10", "100", "x"}; !slices.Equal(got, want) {
		t.Errorf("Ready() = %v, want %v", got, want)
	}
}
