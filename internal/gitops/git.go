// Package gitops runs every git command of Kluzo on the local repository: the
// worktree of each node, the files the core cites, and the check that a
// session left the refs, the git configuration and the hooks as they were
// (ADR 0024, item 4). It is the only package that runs git, through
// platform/exec.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/version"
)

// Config configures the git runner.
type Config struct {
	Bin       string   // default "git"
	ParentEnv []string // the environment of Kluzo; only PATH and LANG are read from it
}

// Git runs git hardened: no hooks, no fsmonitor and no global or system
// configuration. The configuration of the repository is still read, and keys
// such as filter.*.smudge, diff.external or core.sshCommand run commands, so
// the protection rests on an order: Check restores the configuration after
// every session, before any other git command of Kluzo, and Kluzo never runs
// git with a node worktree as the working directory while a session may
// still write there (its .git file is the agent's).
type Git struct {
	bin string
	env []string
}

// hardening goes before every subcommand. These keys win over the
// configuration of the repository: a hook or an fsmonitor command written
// there never runs.
var hardening = []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}

// New returns the runner.
func New(cfg Config) *Git {
	if cfg.Bin == "" {
		cfg.Bin = "git"
	}
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", // neither ~/.gitconfig nor the XDG file
		"GIT_TERMINAL_PROMPT=0",
	}
	for _, kv := range cfg.ParentEnv {
		if k, _, _ := strings.Cut(kv, "="); k == "PATH" || k == "LANG" {
			env = append(env, kv)
		}
	}
	return &Git{bin: cfg.Bin, env: env}
}

// Error is a git command that failed.
type Error struct {
	Args   []string // the command line after "git" and the hardening
	Code   int      // exit code; -1 when it did not start or a signal ended it
	Stderr string   // the end of its standard error
	Err    error    // why it did not start or was stopped, if so
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("git %s: exit %d", strings.Join(e.Args, " "), e.Code)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// run runs git in dir and returns its standard output.
func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	o, err := exec.Run(ctx, exec.Spec{Bin: g.bin, Args: append(append([]string(nil), hardening...), args...), Dir: dir, Env: g.env})
	if err != nil || o.Status.Code != 0 {
		return o.Stdout, &Error{Args: args, Code: o.Status.Code, Stderr: o.Stderr, Err: err}
	}
	return o.Stdout, nil
}

// resolve returns the object id rev names, or ok false when it names nothing.
// rev-parse --verify --quiet exits with 1 and says nothing in that case.
func (g *Git) resolve(ctx context.Context, dir, rev string) (string, bool, error) {
	out, err := g.run(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	if ge := (*Error)(nil); errors.As(err, &ge) && ge.Err == nil && ge.Code == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(out), true, nil
}

// tail returns at most the last n bytes of s, starting on a whole character.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// MinVersion is the oldest git Kluzo runs on (worktrees and merge-tree
// --write-tree, docs/development.md).
const MinVersion = "2.38"

// Version returns the version of git, and an error when it is older than
// MinVersion or does not run.
func (g *Git) Version(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	v := version.Find(out)
	if v == "" || version.Compare(v, MinVersion) < 0 {
		return v, fmt.Errorf("git %s is installed, kluzo needs %s or newer", v, MinVersion)
	}
	return v, nil
}
