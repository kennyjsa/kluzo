package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gates"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/mcp"
	notifylog "github.com/kennyjsa/kluzo/internal/notify/log"
	"github.com/kennyjsa/kluzo/internal/platform/hostcred"
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
	"github.com/kennyjsa/kluzo/internal/runtime/claudecode"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// assembled is the supervisor of a workspace with what must be stopped when
// it ends.
type assembled struct {
	sup  *supervisor.Supervisor
	stop func()
}

// assemble is the production assembly of the supervisor, the only one: every
// port is the real adapter. supervisor.New refuses it when a required
// dependency is left out.
func assemble(ctx context.Context, h Host, o *opened, version string) (*assembled, error) {
	integ, err := h.Connect(ctx, o.project, h.Env)
	if err != nil {
		return nil, err
	}
	created, err := o.created(ctx)
	if err != nil {
		return nil, err
	}
	criteria, err := planCriteria(o.place)
	if err != nil {
		return nil, err
	}
	logs := filepath.Join(o.place.Dir, "logs")
	cred := modelCredential(h.Env)
	rt := o.project.Config.Runtimes[config.ClaudeCode]
	git := gitops.New(gitops.Config{ParentEnv: h.Env})
	runtime := claudecode.New(claudecode.Config{Bin: rt.Bin, MinVersion: rt.MinVersion, Home: filepath.Join(o.place.Dir, "home"),
		ConfigDir: filepath.Join(o.place.Dir, "sessions"), LogDir: filepath.Join(logs, "sessions"), ParentEnv: h.Env, EnvAllow: rt.EnvAllow,
		Credential: cred, Secrets: []string{cred.Value}, Clock: h.Clock})
	srv := mcp.New(&supervisor.Recorder{DB: o.db, Clock: h.Clock, IDs: h.IDs, Log: h.Log}, rand.Reader, version)
	if err := srv.Start(); err != nil {
		return nil, err
	}
	stop := func() { _ = srv.Shutdown(context.Background()) }
	sup, err := supervisor.New(supervisor.Deps{
		DB: o.db, Clock: h.Clock, IDs: h.IDs, Log: h.Log,
		Tracker: integ.Tracker, SCM: integ.SCM, GitToken: integ.GitToken,
		Runtime:  runtime,
		MCP:      srv,
		Git:      git,
		Gates:    gates.New(gates.Config{LogDir: filepath.Join(logs, "gates"), CI: integ.CI, Secrets: []string{cred.Value}}),
		Notifier: notifylog.New(h.Log),
		Docker:   h.Docker,
		Nonce:    nonce,
		HostCredentials: func(ctx context.Context) ([]string, error) {
			return sessionHostCredentials(ctx, h.Env, git.HostCredentialHelpers, runtime.HostLogin)
		},
	}, supervisor.Config{
		Project: o.project, Workspace: created.ID, Milestone: o.place.Milestone, Repo: o.place.Repo, Remote: integ.Remote,
		Branch: created.Branch, Dir: o.place.Dir, Downloads: o.place.Downloads, ParentEnv: h.Env, Criteria: criteria, Version: version,
	})
	if err != nil {
		stop()
		return nil, err
	}
	return &assembled{sup: sup, stop: stop}, nil
}

// sessionHostCredentials are the credentials of the host checked before each
// session (ADR 0020, Q67): those hostcred finds, and a CLI logged in without
// the credential variables of Kluzo.
func sessionHostCredentials(ctx context.Context, env []string, helpers func(context.Context, string, string) ([]string, error),
	hostLogin func(context.Context) (bool, error)) ([]string, error) {
	found, err := hostcred.Check(ctx, env, helpers)
	if err != nil {
		return nil, err
	}
	login, err := hostLogin(ctx)
	if err != nil {
		return nil, err
	}
	if login {
		found = append(found, hostLoginFinding(config.ClaudeCode))
	}
	return found, nil
}

// hostLoginFinding names a harness logged in without the credential
// variables of Kluzo.
func hostLoginFinding(h config.Harness) string {
	return string(h) + " is logged in without CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY (a login of the host)"
}

// nonce is the envelope nonce of a prompt: 8 random hex digits.
func nonce() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// planCriteria are the acceptance criteria of each node, AC1..n in the order
// of the plan (ADR 0023).
func planCriteria(pl Place) (map[string][]core.Criterion, error) {
	plan, err := config.LoadPlan(pl.ConfigDir, pl.Milestone)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	out := map[string][]core.Criterion{}
	for _, n := range plan.Nodes {
		for i, text := range n.Acceptance {
			ref := strconv.Itoa(n.Issue)
			out[ref] = append(out[ref], core.Criterion{ID: "AC" + strconv.Itoa(i+1), Text: text})
		}
	}
	return out, nil
}

// RunWorkspace runs the workspace of a milestone until it is over or ctx
// ends (kluzo run): what was in flight stays in the outbox for the next run.
type RunWorkspace struct {
	Host    Host
	Version string
}

// RunWorkspaceInput names the workspace.
type RunWorkspaceInput struct{ ConfigDir, Milestone string }

// Execute runs the supervisor of the workspace and returns its state when it
// stops.
func (u RunWorkspace) Execute(ctx context.Context, in RunWorkspaceInput) (core.WorkspaceState, error) {
	h := u.Host.fill()
	o, err := open(ctx, h, in.ConfigDir, in.Milestone)
	if err != nil {
		return "", err
	}
	defer func() { _ = o.db.Close() }()
	if s := o.snap.Workspace.State; s == core.WorkspaceAwaitingApproval || s == core.WorkspaceRefining {
		return s, fmt.Errorf("the workspace is %s: approve the plan first (kluzo approve %s)", s, in.Milestone)
	}
	a, err := assemble(ctx, h, o, u.Version)
	if err != nil {
		return o.snap.Workspace.State, err
	}
	defer a.stop()
	if err := a.sup.Run(ctx); err != nil {
		return "", err
	}
	snap, err := o.db.Snapshot(context.WithoutCancel(ctx))
	if err != nil {
		return "", err
	}
	return snap.Workspace.State, nil
}

// Status reads the state of the workspace of a milestone (kluzo status).
type Status struct{ Host Host }

// StatusInput names the workspace.
type StatusInput struct{ ConfigDir, Milestone string }

// StatusNode is a node in the status.
type StatusNode struct {
	Issue    string  `json:"issue"`
	Title    string  `json:"title"`
	State    string  `json:"state"`
	Blocked  string  `json:"blocked_reason,omitempty"`
	Attempt  int     `json:"attempt"`
	Round    int     `json:"round"`
	CostUSD  float64 `json:"cost_usd"`
	MergeSHA string  `json:"merge_sha,omitempty"`
}

// StatusOutput is the state of a workspace, also the JSON of status --json.
type StatusOutput struct {
	Milestone string       `json:"milestone"`
	Workspace string       `json:"workspace"`
	State     string       `json:"state"`
	Branch    string       `json:"branch"`
	BranchOK  string       `json:"milestone_branch"` // pending | done | failed
	PR        string       `json:"pr,omitempty"`
	CostUSD   float64      `json:"cost_usd"`
	BudgetUSD float64      `json:"budget_usd,omitempty"`
	Nodes     []StatusNode `json:"nodes"`
	// Failed are the effects that gave up, for kluzo effect retry.
	Failed []StatusEffect `json:"failed_effects"`
	Disk   StatusDisk     `json:"disk"`
}

// StatusEffect is a failed effect of the outbox.
type StatusEffect struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	LastError string `json:"last_error"`
}

// StatusDisk is the disk the workspace uses (ADR 0025), in bytes: its
// directory, the download caches of the project, the images its gates built
// and the Docker build cache. Docker says why the last two are missing.
type StatusDisk struct {
	Workspace  int64  `json:"workspace"`
	Caches     int64  `json:"caches"`
	Images     int64  `json:"images"`
	BuildCache int64  `json:"build_cache"`
	Docker     string `json:"docker,omitempty"`
}

// Execute reads the projections of the workspace.
func (u Status) Execute(ctx context.Context, in StatusInput) (StatusOutput, error) {
	h := u.Host.fill()
	o, err := open(ctx, h, in.ConfigDir, in.Milestone)
	if err != nil {
		return StatusOutput{}, err
	}
	defer func() { _ = o.db.Close() }()
	out := status(in.Milestone, o.snap)
	failed, err := o.db.FailedEffects(ctx)
	if err != nil {
		return out, err
	}
	out.Failed = []StatusEffect{}
	for _, f := range failed {
		out.Failed = append(out.Failed, StatusEffect{ID: f.ID, Kind: f.Kind, LastError: f.LastError})
	}
	out.Disk, err = disk(ctx, h, o)
	return out, err
}

// disk measures what the workspace takes on this machine.
func disk(ctx context.Context, h Host, o *opened) (StatusDisk, error) {
	var d StatusDisk
	var err error
	if d.Workspace, err = dirSize(o.place.Dir); err != nil {
		return d, err
	}
	if d.Caches, err = dirSize(o.place.Downloads); err != nil {
		return d, err
	}
	err = h.Docker.Probe(ctx)
	if err == nil {
		err = dockerDisk(ctx, h, o.snap.Workspace.ID, &d)
	}
	if err != nil {
		d.Docker = err.Error()
	}
	return d, nil
}

// dockerDisk adds the images of the compose projects of the workspace and
// the build cache.
func dockerDisk(ctx context.Context, h Host, workspace string, d *StatusDisk) error {
	images, err := h.Docker.Images(ctx)
	if err != nil {
		return err
	}
	for _, im := range images {
		if ws, _, ok := sessionenv.ParseComposeProject(im.Project); ok && ws == strings.ToLower(workspace) {
			d.Images += im.Size
		}
	}
	d.BuildCache, err = h.Docker.BuildCache(ctx)
	return err
}

func status(milestone string, s store.Snapshot) StatusOutput {
	w := s.Workspace
	out := StatusOutput{Milestone: milestone, Workspace: w.ID, State: string(w.State), Branch: w.Branch, BranchOK: string(w.Milestone),
		PR: w.PR.URL, CostUSD: config.USD(w.CostUSDMicros), BudgetUSD: config.USD(w.BudgetUSDMicros), Nodes: []StatusNode{}}
	for _, n := range s.Nodes {
		out.Nodes = append(out.Nodes, StatusNode{Issue: n.IssueRef, Title: n.Title, State: string(n.State), Blocked: string(n.BlockedReason),
			Attempt: n.Attempt, Round: n.Round, CostUSD: config.USD(n.CostUSDMicros), MergeSHA: n.MergedSHA})
	}
	return out
}
