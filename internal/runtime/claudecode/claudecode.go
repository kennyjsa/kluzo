// Package claudecode runs agent sessions on the Claude Code CLI in headless
// mode (ADR 0024). It builds the restricted invocation, a synthetic
// environment and the configuration files of the session, normalizes the
// stream into the events of docs/contracts/events.md, and classifies how the
// session ended. It decides nothing about what to do next.
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
	"github.com/kennyjsa/kluzo/internal/platform/version"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Name is the harness name in kluzo.yaml.
const Name = "claude-code"

// DefaultMinVersion is the oldest CLI the adapter supports (ADR 0024, item 10):
// hooks over HTTP with onFailure need 2.1.295.
const DefaultMinVersion = "2.1.295"

// DefaultKillGrace is how long a cancelled session has to stop after SIGTERM.
const DefaultKillGrace = 10 * time.Second

// requiredFlags are the flags the invocation uses that --help documents.
var requiredFlags = []string{
	"--input-format", "--output-format", "--verbose", "--restricted", "--strict-mcp-config",
	"--disable-slash-commands", "--session-id", "--resume", "--model", "--effort", "--max-budget-usd",
	"--exclude-dynamic-system-prompt-sections", "--tools", "--allowedTools",
	"--permission-mode", "--permission-prompts", "--mcp-config", "--settings", "--include-hook-events",
}

// hiddenFileFlags are flags the invocation uses that --help does not list
// (2.1.319). Check probes each with a file that does not exist: a CLI that
// knows the flag complains about the file, one that does not says "unknown
// option", and neither reaches the model.
var hiddenFileFlags = []string{"--append-system-prompt-file"}

// Credential is the model credential passed to the sessions by variable:
// CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY.
type Credential struct{ Name, Value string }

// Config configures the adapter.
type Config struct {
	Bin        string // default "claude"
	MinVersion string // default DefaultMinVersion
	// Home is the synthetic HOME of the workspace; it persists between
	// sessions because it holds the transcripts a resume reads.
	Home string
	// ConfigDir holds the 0600 files of each session, outside every worktree.
	ConfigDir string
	// LogDir receives the raw stream of each session, redacted.
	LogDir string
	// ParentEnv is the environment of Kluzo; only PATH, LANG and EnvAllow are read.
	ParentEnv  []string
	EnvAllow   []string // runtimes.claude-code.env_allow
	Credential Credential
	// Secrets are further values to mask in logs and events.
	Secrets   []string
	Clock     clock.Clock
	KillGrace time.Duration // default DefaultKillGrace
}

// Runtime implements ports.Runtime on the Claude Code CLI.
type Runtime struct{ cfg Config }

var _ ports.Runtime = (*Runtime)(nil)

// New returns the adapter with the defaults filled in.
func New(cfg Config) *Runtime {
	if cfg.Bin == "" {
		cfg.Bin = "claude"
	}
	if cfg.MinVersion == "" {
		cfg.MinVersion = DefaultMinVersion
	}
	if cfg.KillGrace == 0 {
		cfg.KillGrace = DefaultKillGrace
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System{}
	}
	return &Runtime{cfg: cfg}
}

// Name implements ports.Runtime.
func (*Runtime) Name() string { return Name }

// home creates the synthetic HOME if needed.
func (rt *Runtime) home() (string, error) {
	if err := os.MkdirAll(rt.cfg.Home, 0o700); err != nil {
		return "", fmt.Errorf("create the synthetic HOME: %w", err)
	}
	return rt.cfg.Home, nil
}

// env is the synthetic environment of a session (ADR 0024, item 3). The
// credential goes last, so no project variable can replace it.
func (rt *Runtime) env(spec ports.SessionSpec, home string) []string {
	extra := sessionenv.Merge(spec.Env)
	if rt.cfg.Credential.Name != "" {
		extra[rt.cfg.Credential.Name] = rt.cfg.Credential.Value
	}
	return sessionenv.Build(sessionenv.Spec{Parent: rt.cfg.ParentEnv, Home: home, Allow: rt.cfg.EnvAllow, Extra: extra})
}

// Check implements ports.Runtime: it reads the version, the flags and the
// credential, which must come from CLAUDE_CODE_OAUTH_TOKEN or
// ANTHROPIC_API_KEY (a login of the CLI does not count, see HostLogin). Every problem found is reported, joined.
func (rt *Runtime) Check(ctx context.Context) (ports.RuntimeInfo, error) {
	var info ports.RuntimeInfo
	home, err := rt.home()
	if err != nil {
		return info, err
	}
	run := func(args ...string) (string, exec.Status, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		o, err := exec.Run(ctx, exec.Spec{Bin: rt.cfg.Bin, Args: args, Env: rt.env(ports.SessionSpec{}, home)})
		return o.Stdout, o.Status, err
	}

	out, st, err := run("--version")
	if err != nil || st.Code != 0 {
		return info, fmt.Errorf("%w: %s --version: %w", ports.ErrRuntimeMissing, rt.cfg.Bin, errors.Join(err, exitErr(st)))
	}
	info.Version = version.Find(out)
	var problems []error
	switch {
	case info.Version == "":
		problems = append(problems, fmt.Errorf("%w: cannot read a version from %q", ports.ErrRuntimeVersion, strings.TrimSpace(out)))
	case version.Compare(info.Version, rt.cfg.MinVersion) < 0:
		problems = append(problems, fmt.Errorf("%w: claude %s is installed, kluzo needs %s or newer (update with `claude update`)", ports.ErrRuntimeVersion, info.Version, rt.cfg.MinVersion))
	}

	help, _, err := run("--help")
	if err != nil {
		problems = append(problems, fmt.Errorf("%w: claude --help: %w", ports.ErrRuntimeFlags, err))
	}
	var missing []string
	for _, f := range requiredFlags {
		if !strings.Contains(help, f) {
			missing = append(missing, f)
		}
	}
	for _, f := range hiddenFileFlags {
		if !rt.knowsFlag(ctx, home, f) {
			missing = append(missing, f)
		}
	}
	if err == nil && len(missing) > 0 {
		problems = append(problems, fmt.Errorf("%w: %s", ports.ErrRuntimeFlags, strings.Join(missing, ", ")))
	}

	if rt.cfg.Credential.Value == "" {
		problems = append(problems, fmt.Errorf("%w: set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY in the environment of Kluzo", ports.ErrRuntimeAuth))
	} else {
		info.Authenticated, info.AuthSource = true, "env:"+rt.cfg.Credential.Name
	}
	return info, errors.Join(problems...)
}

// knowsFlag probes a file flag hidden from --help with a file that does not
// exist (see hiddenFileFlags). Stdin is closed, so no task can be sent.
func (rt *Runtime) knowsFlag(ctx context.Context, home, flag string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	o, err := exec.Run(ctx, exec.Spec{Bin: rt.cfg.Bin, Args: []string{"-p", "--restricted", flag, filepath.Join(home, ".kluzo-flag-probe-missing")}, Env: rt.env(ports.SessionSpec{}, home)})
	return err == nil && !strings.Contains(o.Stderr, "unknown option")
}

// credentialVars are the variables that carry the credential of the model.
var credentialVars = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}

// HostLogin reports whether the CLI is logged in without the credential
// variables: with the synthetic environment of the sessions minus
// CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY, `claude auth status` still
// finds a login (the macOS Keychain, a login managed by the environment).
// That login is a credential of the host (security.md, Q67).
func (rt *Runtime) HostLogin(ctx context.Context) (bool, error) {
	home, err := rt.home()
	if err != nil {
		return false, err
	}
	var env []string
	for _, kv := range rt.env(ports.SessionSpec{}, home) {
		if k, _, _ := strings.Cut(kv, "="); !slices.Contains(credentialVars, k) {
			env = append(env, kv)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	o, err := exec.Run(ctx, exec.Spec{Bin: rt.cfg.Bin, Args: []string{"auth", "status"}, Env: env})
	if err != nil {
		return false, fmt.Errorf("claude auth status: %w", err)
	}
	compact := strings.ReplaceAll(o.Stdout, " ", "")
	return o.Status.Code == 0 && !strings.Contains(compact, `"loggedIn":false`), nil
}

func exitErr(st exec.Status) error {
	if st.Code == 0 {
		return nil
	}
	return fmt.Errorf("exit code %d", st.Code)
}

// refPrefix starts every harness ref this adapter hands out.
const refPrefix = "claude-code/"

// Start implements ports.Runtime.
func (rt *Runtime) Start(ctx context.Context, spec ports.SessionSpec) (ports.Session, error) {
	return rt.begin(ctx, spec, "", false)
}

// Resume implements ports.Runtime. harnessRef is the HarnessRef of the session
// being continued; it carries the fingerprint of the inputs that must not change.
func (rt *Runtime) Resume(ctx context.Context, harnessRef string, spec ports.SessionSpec) (ports.Session, error) {
	parts := strings.Split(strings.TrimPrefix(harnessRef, refPrefix), "/")
	if !strings.HasPrefix(harnessRef, refPrefix) || len(parts) != 2 {
		return nil, fmt.Errorf("resume %q: not a claude-code session ref: %w", harnessRef, ports.ErrInvalid)
	}
	return rt.begin(ctx, spec, parts[0]+"/"+parts[1], true)
}

// begin starts a new session or resumes one. For a resume, ref is "<uuid>/<fingerprint>".
func (rt *Runtime) begin(ctx context.Context, spec ports.SessionSpec, ref string, resume bool) (ports.Session, error) {
	if spec.HookEndpoint == "" || spec.Hooked == nil {
		return nil, ports.ErrNoGuard
	}
	ts, err := mapTools(spec.Role.Tools, spec.Verify, spec.MCPTools)
	if err != nil {
		return nil, err
	}
	id := harnessUUID(spec.SessionID, spec.Counter)
	var want string
	if resume {
		id, want, _ = strings.Cut(ref, "/")
	}
	f, cleanup, err := rt.sessionFiles(spec, id)
	if err != nil {
		return nil, err
	}
	args := argv(spec, ts, id, resume, f)
	home, err := rt.home()
	if err != nil {
		cleanup()
		return nil, err
	}
	fp := fingerprint(spec, args, home)
	if resume && fp != want {
		cleanup()
		return nil, fmt.Errorf("resume %s: %w", id, ports.ErrResumeMismatch)
	}
	s, err := rt.launch(ctx, spec, refPrefix+id+"/"+fp, ts, args, cleanup)
	if err != nil {
		return nil, err
	}
	return s, nil
}
