package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// planDir writes the lines as .kluzo/plans/v2.0.yaml and returns the .kluzo dir.
func planDir(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, PlansDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PlanPath(dir, "v2.0"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadPlanReadsTheExample(t *testing.T) {
	p, err := LoadPlan(filepath.Join("testdata", "valid"), "v2.0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Milestone != "v2.0" || p.PlanVersion != 3 || p.ApprovedBy != "kennyjsa" || p.Budget.USD != 38 || len(p.Nodes) != 5 {
		t.Errorf("plan = %+v", p)
	}
	if want := time.Date(2026, 11, 5, 14, 2, 11, 0, time.UTC); !p.ApprovedAt.Equal(want) {
		t.Errorf("approved_at = %v, want %v", p.ApprovedAt, want)
	}
	n := p.Nodes[2]
	if n.Issue != 42 || len(n.BlocksOn) != 1 || n.BlocksOn[0] != 41 || len(n.Acceptance) != 2 || n.Budget.USD != 6 || n.Overrides["implementer"]["advisor"] != "opus" {
		t.Errorf("node 42 = %+v", n)
	}
	if !p.Nodes[4].Excluded || p.Nodes[1].PublishesContract != "openapi/agenda.yaml" || p.Nodes[3].ContractOn[0] != 43 {
		t.Errorf("nodes = %+v", p.Nodes)
	}
	if got := p.NodeLine(42); got != 23 {
		t.Errorf("NodeLine(42) = %d, want 23", got)
	}
	if got := p.NodeLine(999); got != 1 {
		t.Errorf("NodeLine of an unknown issue = %d, want 1", got)
	}
	if p.MilestoneLine() != 3 || !strings.HasSuffix(p.File(), "v2.0.yaml") {
		t.Errorf("milestone line %d, file %s", p.MilestoneLine(), p.File())
	}
}

func TestLoadPlanErrors(t *testing.T) {
	ok := []string{"version: 1", "plan_version: 1", "milestone: v2.0"} // lines 1-3
	node := func(extra ...string) []string {
		return append([]string{"nodes:", "  - issue: 41", "    areas: [a]", "    acceptance: [x]"}, extra...)
	}
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	tests := []struct {
		name  string
		lines []string
		want  []want
	}{
		{"unknown field", cat(ok, node("    areaz: [b]")), []want{{"plans/v2.0.yaml", 8, "", `unknown field "areaz"`}}},
		{"bad version and milestone", []string{"version: 2", "plan_version: 0", "milestone: v3.0", "nodes:", "  - issue: 41"}, []want{
			{"plans/v2.0.yaml", 1, "version", "unsupported plan version 2"},
			{"plans/v2.0.yaml", 2, "plan_version", "at least 1"},
			{"plans/v2.0.yaml", 3, "milestone", `is "v3.0" but the file is the plan of "v2.0"`},
		}},
		{"no nodes", append(ok[:3:3], "nodes: []"), []want{{"plans/v2.0.yaml", 4, "nodes", "at least one node"}}},
		{"duplicate issue", cat(ok, node("  - issue: 41")), []want{{"plans/v2.0.yaml", 8, "nodes[1].issue", "duplicate issue #41 (first at nodes[0])"}}},
		{"node without issue number", cat(ok, []string{"nodes:", "  - title: x"}), []want{{"plans/v2.0.yaml", 5, "nodes[0].issue", "required"}}},
		{"blank entries", cat(ok, node("    subtasks: ['']", "    blocks_on: [0]")), []want{
			{"plans/v2.0.yaml", 8, "nodes[0].subtasks[0]", "must not be empty"},
			{"plans/v2.0.yaml", 9, "nodes[0].blocks_on[0]", "issue number"},
		}},
		{"blank acceptance criterion", cat(ok, []string{"nodes:", "  - issue: 41", "    acceptance: ['  ']"}), []want{{"plans/v2.0.yaml", 6, "nodes[0].acceptance[0]", "must not be empty"}}},
		{"contract path outside the repository", cat(ok, node("    publishes_contract: ../x.yaml")), []want{{"plans/v2.0.yaml", 8, "nodes[0].publishes_contract", "clean path relative"}}},
		{"negative budgets", cat(ok, []string{"budget: { usd: -1 }"}, node("    budget: { usd: -2 }")), []want{
			{"plans/v2.0.yaml", 4, "budget.usd", "negative"},
			{"plans/v2.0.yaml", 9, "nodes[0].budget", "negative"},
		}},
		{"excluded node is only a marker", cat(ok, []string{"nodes:", "  - issue: 41", "    excluded: true", "    acceptance: ['']"}), nil},
		{"wrong type", cat(ok, []string{"nodes:", "  - issue: forty"}), []want{{"plans/v2.0.yaml", 5, "", "cannot unmarshal"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := planDir(t, tt.lines...)
			_, err := LoadPlan(dir, "v2.0")
			assertErrors(t, dir, err, tt.want)
		})
	}
}

func TestLoadPlanFileProblems(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadPlan(dir, "v2.0")
	assertErrors(t, dir, err, []want{{"plans/v2.0.yaml", 0, "", "file not found"}})
	for _, name := range []string{"", "../kluzo", "a/b", ".."} {
		if _, err := LoadPlan(dir, name); err == nil || !strings.Contains(err.Error(), "cannot name a plan file") {
			t.Errorf("LoadPlan(%q) err = %v, want a refusal", name, err)
		}
	}
}
