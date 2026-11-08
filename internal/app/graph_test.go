package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// A three-issue milestone: #1 → #2 → #3, and #4 left out of Kluzo.
var graphPlan = []string{
	"version: 1",
	"plan_version: 1",
	"milestone: v0.1",
	"nodes:",
	"  - issue: 1",
	"    areas: [store]",
	"    acceptance: [persists]",
	"  - issue: 2",
	"    areas: [core]",
	"    blocks_on: [1]",
	"    acceptance: [schedules, is deterministic]",
	"  - issue: 3",
	"    areas: [shared]",
	"    blocks_on: [2]",
	"    acceptance: [ships]",
	"  - issue: 4",
	"    excluded: true",
}

type graphEnv struct {
	tracker   *fakes.Tracker
	milestone string
	dir       string
}

func newGraphEnv(t *testing.T, planLines ...string) graphEnv {
	t.Helper()
	ctx := context.Background()
	tr := fakes.NewTracker(clock.NewManual(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)))
	ms := tr.AddMilestone("v0.1", "")
	for i := 1; i <= 4; i++ {
		if _, err := tr.CreateIssue(ctx, ports.NewIssue{Title: "issue " + strconv.Itoa(i), MilestoneRef: ms}, "k"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	link(t, tr, "2", "1")
	link(t, tr, "3", "2")
	dir := filepath.Join(t.TempDir(), DefaultConfigDir)
	if planLines != nil {
		if err := os.MkdirAll(filepath.Join(dir, "plans"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plans", "v0.1.yaml"), []byte(strings.Join(planLines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return graphEnv{tracker: tr, milestone: ms, dir: dir}
}

// link makes from block to in the tracker.
func link(t *testing.T, tr *fakes.Tracker, to, from string) {
	t.Helper()
	ctx := context.Background()
	deps, err := tr.Dependencies(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	deps = append(deps, ports.Dependency{FromRef: from, ToRef: to, Kind: contracts.EdgeBlocks})
	if err := tr.SetDependencies(ctx, to, deps); err != nil {
		t.Fatal(err)
	}
}

func (e graphEnv) run(t *testing.T) LoadGraphOutput {
	t.Helper()
	out, err := LoadGraph{Tracker: e.tracker}.Execute(context.Background(), LoadGraphInput{ConfigDir: e.dir, MilestoneRef: e.milestone})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLoadGraphBuildsTheGraph(t *testing.T) {
	out := newGraphEnv(t, graphPlan...).run(t)
	if len(out.Problems) != 0 {
		t.Fatalf("problems: %v", out.Problems)
	}
	if got, want := out.Graph.Edges, []core.Edge{{From: "1", To: "2", Kind: contracts.EdgeBlocks}, {From: "2", To: "3", Kind: contracts.EdgeBlocks}}; !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %v, want %v", got, want)
	}
	if len(out.Graph.Nodes) != 3 {
		t.Errorf("nodes = %v, want 1, 2 and 3 (4 is excluded)", out.Graph.Nodes)
	}
	if got := out.Graph.Nodes["2"].Criteria; len(got) != 2 || got[1] != (core.Criterion{ID: "AC2", Text: "is deterministic"}) {
		t.Errorf("criteria = %v", got)
	}
	// The scheduler picks the head of the critical path, one node at a time.
	if got := out.Graph.NextToDispatch(1); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("next = %v, want [1]", got)
	}
}

func TestLoadGraphReportsProblemsWithFileAndLine(t *testing.T) {
	tests := []struct {
		name  string
		plan  func(lines []string) []string
		tweak func(t *testing.T, e graphEnv)
		line  int
		rule  error
	}{
		{"tracker link missing from the plan", nil, func(t *testing.T, e graphEnv) { link(t, e.tracker, "3", "1") }, 12, core.ErrDivergentEdge},
		{"plan link missing from the tracker", func(l []string) []string { l[13] = "    blocks_on: [2, 1]"; return l }, nil, 12, core.ErrDivergentEdge},
		{"issue forgotten by the plan", func(l []string) []string { return l[:len(l)-2] }, nil, 1, core.ErrNotInPlan},
		{"cycle through the tracker", nil, func(t *testing.T, e graphEnv) { link(t, e.tracker, "1", "3") }, 5, core.ErrCycle},
		{"no acceptance", func(l []string) []string { l[14] = "    acceptance: []"; return l }, nil, 12, core.ErrNoAcceptance},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := append([]string(nil), graphPlan...)
			if tt.plan != nil {
				lines = tt.plan(lines)
			}
			env := newGraphEnv(t, lines...)
			if tt.tweak != nil {
				tt.tweak(t, env)
			}
			out := env.run(t)
			if len(out.Problems) == 0 {
				t.Fatal("want a problem")
			}
			var found bool
			for _, p := range out.Problems {
				if p.File != filepath.Join(env.dir, "plans", "v0.1.yaml") {
					t.Errorf("problem names file %q", p.File)
				}
				if p.Line == tt.line && strings.Contains(p.Msg, tt.rule.Error()) {
					found = true
				}
			}
			if !found {
				t.Errorf("want %q at line %d, got:\n%v", tt.rule, tt.line, out.Problems)
			}
		})
	}
}

func TestLoadGraphPlanFileProblems(t *testing.T) {
	out := newGraphEnv(t).run(t) // no plan file
	if len(out.Problems) != 1 || !strings.Contains(out.Problems[0].Msg, "file not found") || out.Plan != nil {
		t.Errorf("missing plan: %+v", out)
	}
	out = newGraphEnv(t, "version: 1", "plan_version: 1", "milestone: v0.1", "nodes:", "  - issue: one").run(t)
	if len(out.Problems) != 1 || out.Problems[0].Line != 5 {
		t.Errorf("malformed plan: %+v", out.Problems)
	}
}

func TestLoadGraphFailsWhenTheTrackerDoes(t *testing.T) {
	env := newGraphEnv(t, graphPlan...)
	_, err := LoadGraph{Tracker: env.tracker}.Execute(context.Background(), LoadGraphInput{ConfigDir: env.dir, MilestoneRef: "99"})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
