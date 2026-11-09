package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// ErrWorkspaceExists means the milestone already has a workspace here.
var ErrWorkspaceExists = errors.New("the milestone already has a workspace")

// micros converts dollars to micro-dollars.
func micros(usd float64) int64 { return int64(math.Round(usd * 1e6)) }

// PlanWorkspace creates the workspace of a milestone from its hand-written
// plan (kluzo plan, v0.1): the plan is checked against the project and the
// tracker, and the workspace waits for approval while the milestone branch
// is created or adopted.
type PlanWorkspace struct{ Host Host }

// PlanWorkspaceInput names the configuration and the milestone in the
// tracker (its number on GitHub).
type PlanWorkspaceInput struct {
	ConfigDir    string
	MilestoneRef string
}

// PlanWorkspaceOutput is the workspace created.
type PlanWorkspaceOutput struct {
	Milestone string // the title, which names the workspace
	Dir       string
	Nodes     int
}

// Execute checks the plan and creates the workspace. Problems of the plan
// wrap ErrInvalid.
func (u PlanWorkspace) Execute(ctx context.Context, in PlanWorkspaceInput) (PlanWorkspaceOutput, error) {
	h := u.Host.fill()
	p, err := project(in.ConfigDir)
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	integ, err := h.Connect(ctx, p, h.Env)
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	g, err := checkedGraph(ctx, integ, in.ConfigDir, in.MilestoneRef)
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	pl, err := place(h.Env, p, in.ConfigDir, g.Plan.Milestone)
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	if _, err := os.Stat(pl.DB()); err == nil {
		return PlanWorkspaceOutput{}, fmt.Errorf("%w: %s", ErrWorkspaceExists, pl.Dir)
	}
	git := gitops.New(gitops.Config{ParentEnv: h.Env})
	base, err := git.Head(ctx, pl.Repo, mainBranch(p))
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	now := h.Clock.Now()
	wsID, err := h.IDs.New(ids.Workspace, now)
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	budget := g.Plan.Budget.USD
	if budget == 0 {
		budget = p.Config.Budgets.Milestone.USD
	}
	w := supervisor.NewWorkspace{
		Created: contracts.WorkspaceCreated{ID: wsID, Branch: milestoneBranch(p, g.Plan.Milestone), BaseSHA: base, Profile: g.Plan.Profile, BudgetUSDMicros: micros(budget), MilestoneRef: in.MilestoneRef},
		Edges:   g.Graph.Edges,
	}
	for _, n := range g.Plan.Nodes {
		ref := strconv.Itoa(n.Issue)
		if _, ok := g.Graph.Nodes[ref]; !ok || n.Excluded {
			continue
		}
		id, err := h.IDs.New(ids.Node, now)
		if err != nil {
			return PlanWorkspaceOutput{}, err
		}
		b := p.Config.Budgets.Node
		if n.Budget.Attempts > 0 {
			b.Attempts = n.Budget.Attempts
		}
		if n.Budget.Minutes > 0 {
			b.Minutes = n.Budget.Minutes
		}
		if n.Budget.USD > 0 {
			b.USD = n.Budget.USD
		}
		w.Nodes = append(w.Nodes, supervisor.NewNode{ID: id, Added: contracts.NodeAdded{IssueRef: ref, Title: n.Title, Areas: n.Areas,
			Budget: contracts.NodeBudget{Attempts: b.Attempts, Minutes: b.Minutes, USDMicros: micros(b.USD)}}})
	}
	db, err := store.Open(ctx, pl.DB())
	if err != nil {
		return PlanWorkspaceOutput{}, err
	}
	defer func() { _ = db.Close() }()
	if err := supervisor.CreateWorkspace(ctx, db, h.IDs, h.Clock, w); err != nil {
		return PlanWorkspaceOutput{}, err
	}
	return PlanWorkspaceOutput{Milestone: g.Plan.Milestone, Dir: pl.Dir, Nodes: len(w.Nodes)}, nil
}

// checkedGraph loads the plan and its graph; problems wrap ErrInvalid.
func checkedGraph(ctx context.Context, integ Integrations, configDir, ref string) (LoadGraphOutput, error) {
	g, err := LoadGraph{Tracker: integ.Tracker}.Execute(ctx, LoadGraphInput{ConfigDir: configDir, MilestoneRef: ref})
	if err != nil {
		return g, err
	}
	if len(g.Problems) > 0 {
		return g, fmt.Errorf("%w:\n%w", ErrInvalid, config.Errors(g.Problems))
	}
	return g, nil
}

// mainBranch is branches.main, or main.
func mainBranch(p *config.Project) string {
	if b := p.Config.Branches.Main; b != "" {
		return b
	}
	return "main"
}

// milestoneBranch is the branch of the milestone: branches.milestone_prefix
// (milestone/ by default) and its title.
func milestoneBranch(p *config.Project, title string) string {
	prefix := p.Config.Branches.MilestonePrefix
	if prefix == "" {
		prefix = "milestone/"
	}
	return prefix + title
}

// ApprovePlan approves the plan of a workspace awaiting approval (kluzo
// approve): the plan is checked again, and its commit on the milestone
// branch goes to the outbox with the move to approved.
type ApprovePlan struct{ Host Host }

// ApprovePlanInput names the workspace (the milestone title) and who
// approves.
type ApprovePlanInput struct {
	ConfigDir string
	Milestone string
	By        string // empty: $USER
}

// Execute approves the plan.
func (u ApprovePlan) Execute(ctx context.Context, in ApprovePlanInput) error {
	h := u.Host.fill()
	o, err := open(ctx, h, in.ConfigDir, in.Milestone)
	if err != nil {
		return err
	}
	defer func() { _ = o.db.Close() }()
	if s := o.snap.Workspace.State; s != core.WorkspaceAwaitingApproval {
		return fmt.Errorf("the workspace is %s, not awaiting approval", s)
	}
	integ, err := h.Connect(ctx, o.project, h.Env)
	if err != nil {
		return err
	}
	created, err := o.created(ctx)
	if err != nil {
		return err
	}
	g, err := checkedGraph(ctx, integ, in.ConfigDir, created.MilestoneRef)
	if err != nil {
		return err
	}
	file := config.PlanPath(o.place.ConfigDir, in.Milestone)
	content, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(o.place.Repo, file)
	if err != nil {
		return err
	}
	by := in.By
	if by == "" {
		by = getenv(h.Env, "USER")
	}
	return supervisor.ApprovePlan(ctx, o.db, h.IDs, h.Clock, supervisor.PlanApproval{
		Approved: contracts.PlanApproved{PlanVersion: g.Plan.PlanVersion, CreatedIssues: []int{}, By: by},
		Path:     filepath.ToSlash(rel), Content: content,
	})
}
