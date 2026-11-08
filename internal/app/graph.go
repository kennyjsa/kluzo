package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// LoadGraph reads the plan of a milestone, checks it against the project
// configuration (areas, role overrides), asks the tracker for the issues and
// their dependencies, and builds the graph the scheduler works on (F10). It
// reads only: persistence and events come with the workspace.
type LoadGraph struct{ Tracker ports.Tracker }

// LoadGraphInput names the configuration directory and the tracker milestone.
type LoadGraphInput struct {
	ConfigDir    string // the .kluzo directory
	MilestoneRef string
}

// LoadGraphOutput is the graph, or the reasons it cannot start. A non-empty
// Problems means the plan is invalid; it is not an error of the use case. Each
// problem names the plan file and the line of the node it is about.
type LoadGraphOutput struct {
	Plan     *config.Plan
	Graph    core.Graph // fit to start only when Problems is empty
	Problems []config.Error
}

// Execute loads the plan and builds the graph. The error is reserved for
// failures to reach the tracker.
func (u LoadGraph) Execute(ctx context.Context, in LoadGraphInput) (LoadGraphOutput, error) {
	var out LoadGraphOutput
	m, err := u.Tracker.Milestone(ctx, in.MilestoneRef)
	if err != nil {
		return out, fmt.Errorf("read milestone %s: %w", in.MilestoneRef, err)
	}
	plan, err := config.LoadPlan(in.ConfigDir, m.Title)
	var invalid config.Errors
	if errors.As(err, &invalid) {
		out.Problems = invalid
		return out, nil
	} else if err != nil {
		return out, err
	}
	out.Plan = plan

	proj, err := config.Load(in.ConfigDir)
	if errors.As(err, &invalid) {
		out.Problems = invalid // the plan cannot be judged against a broken configuration
		return out, nil
	} else if err != nil {
		return out, err
	}
	out.Problems = plan.CheckProject(proj)

	issues, err := u.Tracker.MilestoneIssues(ctx, in.MilestoneRef)
	if err != nil {
		return out, fmt.Errorf("list issues of milestone %s: %w", in.MilestoneRef, err)
	}
	nodes := planNodes(plan)
	excluded := map[string]bool{}
	for _, n := range nodes {
		excluded[n.Issue] = n.Excluded
	}
	var deps []ports.Dependency
	for _, is := range issues {
		if excluded[is.Ref] {
			continue
		}
		d, err := u.Tracker.Dependencies(ctx, is.Ref)
		if err != nil {
			return out, fmt.Errorf("read dependencies of issue %s: %w", is.Ref, err)
		}
		deps = append(deps, d...)
	}

	var problems []core.Problem
	out.Graph, problems = core.GraphFromPlan(nodes, issues, deps)
	for _, p := range problems {
		line := 1
		if n, err := strconv.Atoi(p.Node); err == nil {
			line = plan.NodeLine(n)
		}
		out.Problems = append(out.Problems, config.Error{File: plan.File(), Line: line, Key: "nodes", Msg: p.Error()})
	}
	return out, nil
}

// planNodes converts the plan file into what the core judges.
func planNodes(plan *config.Plan) []core.PlanNode {
	refs := func(issues []int) []string {
		out := make([]string, len(issues))
		for i, n := range issues {
			out[i] = strconv.Itoa(n)
		}
		return out
	}
	nodes := make([]core.PlanNode, len(plan.Nodes))
	for i, n := range plan.Nodes {
		nodes[i] = core.PlanNode{
			Issue: strconv.Itoa(n.Issue), Title: n.Title, Areas: n.Areas, Acceptance: n.Acceptance,
			BlocksOn: refs(n.BlocksOn), ContractOn: refs(n.ContractOn), Excluded: n.Excluded,
		}
	}
	return nodes
}
