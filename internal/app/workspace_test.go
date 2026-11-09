package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// workspaceEnv is the graph env in a git repository on main, with a host
// whose tracker is the fake one.
type workspaceEnv struct {
	graphEnv
	host Host
	data string // XDG_DATA_HOME
}

func newWorkspaceEnv(t *testing.T) workspaceEnv {
	t.Helper()
	e := workspaceEnv{graphEnv: newGraphEnv(t, graphPlan...), data: t.TempDir()}
	repo := filepath.Dir(e.dir)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repo, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + repo, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	e.host = Host{
		Env:   []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "XDG_DATA_HOME=" + e.data, "USER=kenny"},
		Clock: clock.NewManual(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)),
		Connect: func(context.Context, *config.Project, []string) (Integrations, error) {
			return Integrations{Tracker: e.tracker, SCM: fakes.NewSCM("main"), CI: fakes.NewCI(), Remote: filepath.Join(e.data, "origin.git")}, nil
		},
	}
	return e
}

// kluzo plan, approve, status, pause and resume on one workspace.
func TestWorkspaceCommands(t *testing.T) {
	ctx := context.Background()
	e := newWorkspaceEnv(t)
	out, err := PlanWorkspace{Host: e.host}.Execute(ctx, PlanWorkspaceInput{ConfigDir: e.dir, MilestoneRef: e.milestone})
	if err != nil {
		t.Fatal(err)
	}
	if out.Milestone != "v0.1" || out.Nodes != 3 || !strings.HasPrefix(out.Dir, filepath.Join(e.data, "kluzo", "workspaces")) {
		t.Errorf("plan = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(out.Dir, "state.db")); err != nil {
		t.Error(err)
	}
	if _, err := (PlanWorkspace{Host: e.host}).Execute(ctx, PlanWorkspaceInput{ConfigDir: e.dir, MilestoneRef: e.milestone}); !errors.Is(err, ErrWorkspaceExists) {
		t.Errorf("second plan: %v", err)
	}
	st, err := Status{Host: e.host}.Execute(ctx, StatusInput{ConfigDir: e.dir, Milestone: "v0.1"})
	if err != nil || st.State != string(core.WorkspaceAwaitingApproval) || st.Branch != "milestone/v0.1" || st.BranchOK != "pending" || len(st.Nodes) != 3 || st.Nodes[0].State != "planned" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := (Status{Host: e.host}).Execute(ctx, StatusInput{ConfigDir: e.dir, Milestone: "v9"}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("status of no workspace: %v", err)
	}
	if _, err := (RunWorkspace{Host: e.host}).Execute(ctx, RunWorkspaceInput{ConfigDir: e.dir, Milestone: "v0.1"}); err == nil || !strings.Contains(err.Error(), "approve") {
		t.Errorf("run before the approval: %v", err)
	}
	move := func(m Move, green bool) (core.WorkspaceState, error) {
		return MoveWorkspace{Host: e.host, Doctor: func(context.Context, string) (DoctorOutput, error) {
			if green {
				return DoctorOutput{}, nil
			}
			return DoctorOutput{Problems: []string{"git: too old"}}, nil
		}}.Execute(ctx, MoveWorkspaceInput{ConfigDir: e.dir, Milestone: "v0.1", Move: m})
	}
	if _, err := move(Pause, true); !errors.Is(err, core.ErrInvalidTransition) {
		t.Errorf("pause awaiting approval: %v", err)
	}
	if err := (ApprovePlan{Host: e.host}).Execute(ctx, ApprovePlanInput{ConfigDir: e.dir, Milestone: "v0.1"}); err != nil {
		t.Fatal(err)
	}
	o, err := open(ctx, e.host.fill(), e.dir, "v0.1")
	if err != nil {
		t.Fatal(err)
	}
	evs, _ := o.db.Events(ctx, 0, 100)
	var approved, commit bool
	for _, ev := range evs {
		approved = approved || ev.Kind == contracts.EventPlanApproved && strings.Contains(string(ev.Payload), `"by":"kenny"`)
	}
	pending, _ := o.db.PendingEffects(ctx, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 10)
	for _, f := range pending {
		commit = commit || f.Kind == string(core.EffectCommitPlan) && strings.Contains(string(f.Payload), `".kluzo/plans/v0.1.yaml"`)
	}
	if !approved || !commit || o.snap.Workspace.State != core.WorkspaceApproved {
		t.Errorf("approval: event %v, git.plan %v, state %s", approved, commit, o.snap.Workspace.State)
	}
	if err := (ApprovePlan{Host: e.host}).Execute(ctx, ApprovePlanInput{ConfigDir: e.dir, Milestone: "v0.1"}); err == nil {
		t.Error("approved twice")
	}
	// Started by the loop once the milestone branch is ready; here by hand.
	if err := supervisor.MoveWorkspace(ctx, o.db, e.host.fill().IDs, e.host.Clock, core.TriggerStart); err != nil {
		t.Fatal(err)
	}
	_ = o.db.Close()
	if s, err := move(Pause, true); err != nil || s != core.WorkspacePaused {
		t.Fatalf("pause: %s %v", s, err)
	}
	if s, err := move(Resume, false); !errors.Is(err, ErrDoctor) || s != core.WorkspacePaused {
		t.Errorf("resume with a red doctor: %s %v", s, err)
	}
	if s, err := move(Resume, true); err != nil || s != core.WorkspaceRunning {
		t.Errorf("resume: %s %v", s, err)
	}
	if _, err := move(Move(core.TriggerCancelAll), true); err == nil {
		t.Error("a person cancelled through pause/resume")
	}
}

// A plan the tracker contradicts is invalid configuration, and creates
// nothing.
func TestPlanInvalid(t *testing.T) {
	e := newWorkspaceEnv(t)
	if err := os.WriteFile(config.PlanPath(e.dir, "v0.1"), []byte("version: 1\nplan_version: 1\nmilestone: v0.1\nnodes:\n  - issue: 1\n    areas: [nope]\n    acceptance: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PlanWorkspace{Host: e.host}.Execute(context.Background(), PlanWorkspaceInput{ConfigDir: e.dir, MilestoneRef: e.milestone})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("plan = %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "kluzo", "workspaces")); !os.IsNotExist(err) {
		t.Error("a workspace was created")
	}
}

// The production assembly gives the supervisor every real port; with a
// required port left out, supervisor.New refuses it by name.
func TestProductionAssembly(t *testing.T) {
	ctx := context.Background()
	e := newWorkspaceEnv(t)
	if _, err := (PlanWorkspace{Host: e.host}).Execute(ctx, PlanWorkspaceInput{ConfigDir: e.dir, MilestoneRef: e.milestone}); err != nil {
		t.Fatal(err)
	}
	h := e.host.fill()
	o, err := open(ctx, h, e.dir, "v0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.db.Close() }()
	a, err := assemble(ctx, h, o, "test")
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	a.stop()
	h.Connect = func(context.Context, *config.Project, []string) (Integrations, error) {
		return Integrations{Tracker: e.tracker, CI: fakes.NewCI(), Remote: "x"}, nil // no SCM
	}
	if _, err := assemble(ctx, h, o, "test"); err == nil || !strings.Contains(err.Error(), "SCM") {
		t.Errorf("without the SCM: %v", err)
	}
}
