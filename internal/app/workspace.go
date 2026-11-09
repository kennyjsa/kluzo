package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
	"github.com/kennyjsa/kluzo/internal/tracker/github"
)

// The workspace of a milestone on this machine (docs/contracts/data-model.md):
// ~/.local/share/kluzo/workspaces/<project>-<milestone>/ with its state.db,
// worktrees, HOMEs, caches and logs; the download cache of the project at
// ~/.local/share/kluzo/cache/<project>/.

// ErrInvalid wraps the errors of a use case caused by the configuration or
// the plan: the CLI exits 2 on them.
var ErrInvalid = errors.New("invalid configuration")

// ErrNoWorkspace means the milestone has no workspace on this machine.
var ErrNoWorkspace = errors.New("no workspace")

// Host is what the use cases read from the machine: the environment, the
// clock, the ids, the log and the way to the tracker. The zero value is the
// real machine.
type Host struct {
	Env   []string    // os.Environ when nil
	Clock clock.Clock // the system clock when nil
	IDs   *ids.Generator
	Log   *slog.Logger
	// Connect opens the tracker of the project (Connect when nil).
	Connect func(ctx context.Context, p *config.Project, env []string) (Integrations, error)
}

// Integrations are the adapters of the tracker of a project.
type Integrations struct {
	Tracker ports.Tracker
	SCM     ports.SCM
	CI      ports.CI
	// GitToken is the credential of the bot for git fetch and push.
	GitToken func(context.Context) (string, error)
	// Remote is the URL git fetches from and pushes to.
	Remote string
}

// fill returns h with the real machine where it is empty.
func (h Host) fill() Host {
	if h.Env == nil {
		h.Env = os.Environ()
	}
	if h.Clock == nil {
		h.Clock = clock.System{}
	}
	if h.IDs == nil {
		h.IDs = ids.NewGenerator(rand.Reader)
	}
	if h.Log == nil {
		h.Log = slog.New(slog.DiscardHandler)
	}
	if h.Connect == nil {
		h.Connect = Connect
	}
	return h
}

// getenv reads a variable of env.
func getenv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Connect opens the tracker of the project with the credentials of the bot,
// read from the variables config.md names (security.md: never from .kluzo/).
func Connect(ctx context.Context, p *config.Project, env []string) (Integrations, error) {
	t := p.Config.Tracker
	if t.Kind != "github" {
		return Integrations{}, fmt.Errorf("tracker %q: only github is supported in v0.1", t.Kind)
	}
	auth := github.Auth{Kind: github.AuthToken}
	switch t.Auth.Kind {
	case "github_app":
		id, key := getenv(env, t.Auth.AppIDEnv), getenv(env, t.Auth.KeyFileEnv)
		if id == "" || key == "" {
			return Integrations{}, fmt.Errorf("the GitHub App needs %s and %s", t.Auth.AppIDEnv, t.Auth.KeyFileEnv)
		}
		pem, err := os.ReadFile(key)
		if err != nil {
			return Integrations{}, fmt.Errorf("read the key of the GitHub App: %w", err)
		}
		auth.Kind, auth.PrivateKey = github.AuthGitHubApp, pem
		if _, err := fmt.Sscan(id, &auth.AppID); err != nil {
			return Integrations{}, fmt.Errorf("%s is not a number: %w", t.Auth.AppIDEnv, err)
		}
	default:
		name := t.Auth.TokenEnvName(t.Kind)
		if auth.Token = getenv(env, name); auth.Token == "" {
			return Integrations{}, fmt.Errorf("%s is not set: the token of the bot", name)
		}
	}
	a, err := github.New(ctx, github.Options{Repo: t.Repo, Auth: auth})
	if err != nil {
		return Integrations{}, err
	}
	return Integrations{Tracker: a, SCM: a, CI: a, GitToken: a.GitToken, Remote: "https://github.com/" + t.Repo + ".git"}, nil
}

// project loads the configuration; its problems wrap ErrInvalid.
func project(dir string) (*config.Project, error) {
	p, err := config.Load(dir)
	var invalid config.Errors
	if errors.As(err, &invalid) {
		return nil, fmt.Errorf("%w:\n%w", ErrInvalid, err)
	}
	return p, err
}

// unsafeName are the characters a directory name does not keep.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Place is where a workspace and its project live on this machine.
type Place struct {
	ConfigDir string // the .kluzo directory, absolute
	Repo      string // the main checkout: the parent of ConfigDir
	Dir       string // the workspace directory
	Downloads string // the download cache of the project
	Milestone string // the title of the milestone
}

// DB is the journal of the workspace.
func (p Place) DB() string { return filepath.Join(p.Dir, "state.db") }

// place locates the workspace of a milestone of the project in configDir.
func place(env []string, p *config.Project, configDir, milestone string) (Place, error) {
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return Place{}, err
	}
	data := getenv(env, "XDG_DATA_HOME")
	if data == "" {
		home := getenv(env, "HOME")
		if home == "" {
			return Place{}, errors.New("HOME is not set: the workspaces live under it")
		}
		data = filepath.Join(home, ".local", "share")
	}
	root := filepath.Join(data, "kluzo")
	name := p.Config.Project.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(abs))
	}
	name = unsafeName.ReplaceAllString(name, "-")
	return Place{
		ConfigDir: abs, Repo: filepath.Dir(abs), Milestone: milestone,
		Dir:       filepath.Join(root, "workspaces", name+"-"+unsafeName.ReplaceAllString(milestone, "-")),
		Downloads: filepath.Join(root, "cache", name),
	}, nil
}

// opened is a workspace opened by a use case: its project, its place and its
// journal, which the caller closes.
type opened struct {
	project *config.Project
	place   Place
	db      *store.DB
	snap    store.Snapshot
}

// open opens the workspace of a milestone; ErrNoWorkspace when there is none.
func open(ctx context.Context, h Host, configDir, milestone string) (*opened, error) {
	p, err := project(configDir)
	if err != nil {
		return nil, err
	}
	pl, err := place(h.Env, p, configDir, milestone)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(pl.DB()); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w for milestone %q (run kluzo plan first)", ErrNoWorkspace, milestone)
	}
	db, err := store.Open(ctx, pl.DB())
	if err != nil {
		return nil, err
	}
	snap, err := db.Snapshot(ctx)
	if err == nil && snap.Workspace == nil {
		err = fmt.Errorf("%w for milestone %q", ErrNoWorkspace, milestone)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &opened{project: p, place: pl, db: db, snap: snap}, nil
}

// created reads the workspace.created of the workspace.
func (o *opened) created(ctx context.Context) (contracts.WorkspaceCreated, error) {
	var c contracts.WorkspaceCreated
	evs, err := o.db.Events(ctx, 0, 1)
	if err != nil {
		return c, err
	}
	if len(evs) == 0 || evs[0].Kind != contracts.EventWorkspaceCreated {
		return c, fmt.Errorf("%w: the journal does not start with workspace.created", ErrNoWorkspace)
	}
	return c, json.Unmarshal(evs[0].Payload, &c)
}

// MoveWorkspace pauses or resumes the workspace of a milestone (kluzo pause,
// kluzo resume).
type MoveWorkspace struct {
	Host Host
	// Doctor runs before a resume (retomar exige kluzo doctor verde); nil
	// means the Doctor of Host.
	Doctor func(ctx context.Context, configDir string) (DoctorOutput, error)
}

// ErrRefused means the workspace refuses the move in its state.
var ErrRefused = core.ErrInvalidTransition

// Move is what a person may do to a running workspace.
type Move string

// The moves of a person.
const (
	Pause  Move = Move(core.TriggerPause)
	Resume Move = Move(core.TriggerResume)
)

// MoveWorkspaceInput names the workspace and what to do with it.
type MoveWorkspaceInput struct {
	ConfigDir string
	Milestone string
	Move      Move
}

// Execute applies the trigger. A resume first needs a green doctor.
func (u MoveWorkspace) Execute(ctx context.Context, in MoveWorkspaceInput) (core.WorkspaceState, error) {
	h := u.Host.fill()
	if in.Move != Pause && in.Move != Resume {
		return "", fmt.Errorf("a person may only pause or resume, not %q", in.Move)
	}
	trigger := core.WorkspaceTrigger(in.Move)
	o, err := open(ctx, h, in.ConfigDir, in.Milestone)
	if err != nil {
		return "", err
	}
	defer func() { _ = o.db.Close() }()
	if _, err := core.NextWorkspace(o.snap.Workspace.State, trigger); err != nil {
		return o.snap.Workspace.State, fmt.Errorf("the workspace is %s: %w", o.snap.Workspace.State, err)
	}
	if in.Move == Resume {
		doctor := u.Doctor
		if doctor == nil {
			doctor = func(ctx context.Context, dir string) (DoctorOutput, error) {
				return Doctor{Host: h}.Execute(ctx, DoctorInput{Dir: dir})
			}
		}
		out, err := doctor(ctx, in.ConfigDir)
		if err != nil {
			return o.snap.Workspace.State, err
		}
		if !out.OK() {
			return o.snap.Workspace.State, fmt.Errorf("%w: run kluzo doctor and fix what it reports before resuming", ErrDoctor)
		}
	}
	if err := supervisor.MoveWorkspace(ctx, o.db, h.IDs, h.Clock, trigger); err != nil {
		return o.snap.Workspace.State, err
	}
	snap, err := o.db.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	return snap.Workspace.State, nil
}
