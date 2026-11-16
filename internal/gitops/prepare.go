package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// configPath is where kluzo.yaml lives in the repository.
const configPath = ".kluzo/kluzo.yaml"

// setupTailBytes is how much of the end of the setup output a SetupError keeps.
const setupTailBytes = 4 << 10

// ErrSetup means the worktree.setup command failed. It is a runtime error:
// retrying may help.
var ErrSetup = errors.New("worktree setup failed")

// SetupError is a worktree.setup that exited non-zero, timed out or could not
// start. It unwraps to ErrSetup, is not permanent and its Cause is retryable.
type SetupError struct {
	Code     int    // exit code; -1 when it did not start or a signal ended it
	TimedOut bool   // worktree.timeout ran out
	Output   string // the end of its standard output followed by its standard error
	Err      error  // why it did not start, if so
}

func (e *SetupError) Error() string {
	var msg string
	switch {
	case e.Err != nil:
		msg = fmt.Sprintf("%v: %v", ErrSetup, e.Err)
	case e.TimedOut:
		msg = fmt.Sprintf("%v: timed out", ErrSetup)
	default:
		msg = fmt.Sprintf("%v: exit %d", ErrSetup, e.Code)
	}
	if s := strings.TrimSpace(e.Output); s != "" {
		msg += "\n" + s
	}
	return msg
}

func (e *SetupError) Unwrap() []error {
	if e.Err != nil {
		return []error{ErrSetup, e.Err}
	}
	return []error{ErrSetup}
}

// Cause classifies the failure for the retry policy (docs/contracts/data-model.md).
func (e *SetupError) Cause() ports.Cause { return ports.CauseRuntime }

// PrepareSpec describes the preparation of a node worktree.
type PrepareSpec struct {
	Repo     string // the main checkout, where worktree.include is copied from
	Worktree string // the node worktree
	Base     string // the milestone branch: kluzo.yaml is read from its tip
	// Env is the complete environment of worktree.setup, the one the gates
	// get: built by the caller with sessionenv, it must hold env_allow, the
	// cache variables of caches: and COMPOSE_PROJECT_NAME (ADR 0025).
	Env []string
	// DefaultTimeout bounds worktree.setup when kluzo.yaml sets no timeout.
	DefaultTimeout time.Duration
}

// Prepare readies a node worktree before its first session: it copies the
// worktree.include files from the main checkout and runs worktree.setup in
// the worktree. Both come from kluzo.yaml at the tip of Base, never from the
// worktree or the working copy, which an agent or a person may have changed.
// A failed setup returns a *SetupError.
func (g *Git) Prepare(ctx context.Context, s PrepareSpec) error {
	data, err := g.ReadAt(ctx, s.Repo, "refs/heads/"+s.Base, configPath)
	if err != nil {
		return err
	}
	cfg, err := config.ParseConfig(s.Base+":"+configPath, data)
	if err != nil {
		return err
	}
	if err := copyIncludes(s.Repo, s.Worktree, cfg.Worktree.Include); err != nil {
		return err
	}
	if cfg.Worktree.Setup == "" {
		return nil
	}
	timeout := cfg.Worktree.Timeout
	if timeout <= 0 {
		timeout = s.DefaultTimeout
	}
	return runSetup(ctx, cfg.Worktree.Setup, s.Worktree, s.Env, timeout)
}

// copyIncludes copies each path from the main checkout to the worktree,
// keeping its permissions. A path that does not exist is skipped; one that
// leaves either tree, is a symlink or is not a regular file is refused.
func copyIncludes(from, to string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	src, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenRoot(to)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	for _, p := range paths {
		if err := copyInclude(src, dst, p); err != nil {
			return fmt.Errorf("worktree.include %q: %w", p, err)
		}
	}
	return nil
}

func copyInclude(src, dst *os.Root, p string) error {
	if !filepath.IsLocal(p) {
		return errors.New("leaves the repository")
	}
	fi, err := src.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return errors.New("is a symlink")
	case !fi.Mode().IsRegular():
		return errors.New("is not a regular file")
	}
	in, err := src.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if dir := filepath.Dir(p); dir != "." {
		if err := dst.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// A symlink at the destination is not followed: it could point at a file
	// of the worktree an agent wants overwritten.
	if fi, err := dst.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return errors.New("is not a regular file in the worktree")
	}
	out, err := dst.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Chmod(fi.Mode().Perm()), out.Close())
}

// runSetup runs the setup command with sh in dir.
func runSetup(ctx context.Context, cmd, dir string, env []string, timeout time.Duration) error {
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	o, runErr := exec.Run(runCtx, exec.Spec{Bin: "sh", Args: []string{"-c", cmd}, Dir: dir, Env: env})
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("worktree setup: %w", err) // Kluzo is stopping, not a failure of the setup
	}
	if runErr == nil && o.Status.Code == 0 {
		return nil
	}
	e := &SetupError{Code: o.Status.Code, Output: tail(o.Stdout+o.Stderr, setupTailBytes)}
	switch {
	case errors.Is(runErr, context.DeadlineExceeded):
		e.TimedOut = true
	case runErr != nil:
		e.Err = runErr
	}
	return e
}
