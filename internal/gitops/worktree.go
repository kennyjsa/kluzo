package gitops

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// WorktreeSpec describes the worktree of a node.
type WorktreeSpec struct {
	Repo   string // the main checkout
	Path   string // where the worktree goes
	Branch string // the node branch, created by AddWorktree
	Base   string // the milestone branch the node starts from
}

// AddWorktree creates the worktree at Path on a new branch Branch that starts
// at the tip of the local branch Base, and returns the commit it started from.
// It runs as an effect of the outbox, so it may run again: a worktree already
// at Path on Branch is accepted, and an existing Branch without a worktree is
// checked out as it is. A missing Base wraps ports.ErrNotFound.
func (g *Git) AddWorktree(ctx context.Context, s WorktreeSpec) (string, error) {
	base := "refs/heads/" + s.Base
	tip, ok, err := g.resolve(ctx, s.Repo, base+"^{commit}")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("base branch %q: %w", s.Base, ports.ErrNotFound)
	}
	branch := "refs/heads/" + s.Branch
	// A worktree whose directory was deleted is still registered; pruning
	// first lets it be created again.
	if _, err := g.run(ctx, s.Repo, "worktree", "prune"); err != nil {
		return "", err
	}
	wts, err := g.worktrees(ctx, s.Repo)
	if err != nil {
		return "", err
	}
	if b, found := wts[canonical(s.Path)]; found {
		if b != branch {
			return "", fmt.Errorf("worktree %s is on %q, not on %q", s.Path, b, branch)
		}
	} else {
		_, exists, err := g.resolve(ctx, s.Repo, branch)
		if err != nil {
			return "", err
		}
		args := []string{"worktree", "add", "--end-of-options", s.Path, s.Branch}
		if !exists {
			args = []string{"worktree", "add", "-b", s.Branch, "--end-of-options", s.Path, tip}
		}
		if _, err := g.run(ctx, s.Repo, args...); err != nil {
			return "", err
		}
	}
	// Equal to tip on the first run; on a repeat after the milestone moved, it
	// is still the commit the branch was born from.
	out, err := g.run(ctx, s.Repo, "merge-base", "--end-of-options", branch, tip)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoveWorktree removes the worktree at path and deletes the local branch.
// Either may already be gone. A locked worktree is removed too: the lock may
// have been set by the agent.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path, branch string) error {
	if _, err := g.run(ctx, repo, "worktree", "prune"); err != nil {
		return err
	}
	wts, err := g.worktrees(ctx, repo)
	if err != nil {
		return err
	}
	if _, found := wts[canonical(path)]; found {
		if _, err := g.run(ctx, repo, "worktree", "remove", "--force", "--force", "--end-of-options", path); err != nil {
			return err
		}
	}
	_, exists, err := g.resolve(ctx, repo, "refs/heads/"+branch)
	if err != nil || !exists {
		return err
	}
	_, err = g.run(ctx, repo, "branch", "-D", "--end-of-options", branch)
	return err
}

// worktrees returns the registered worktrees: canonical path -> full name of
// the branch checked out there ("" when detached).
func (g *Git) worktrees(ctx context.Context, repo string) (map[string]string, error) {
	out, err := g.run(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	wts := map[string]string{}
	var cur string
	for _, field := range strings.Split(out, "\x00") {
		k, v, _ := strings.Cut(field, " ")
		switch k {
		case "worktree":
			cur = canonical(v)
			wts[cur] = ""
		case "branch":
			wts[cur] = v
		}
	}
	return wts, nil
}

// canonical resolves symlinks in path so it compares with the paths git
// prints (on macOS the temporary directory is behind /var -> /private/var).
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	// A path that does not exist yet: resolve its parent.
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}

// ReadAt returns the content of the file at path in revision rev. A path that
// does not exist there, or is not a file, wraps ports.ErrNotFound.
func (g *Git) ReadAt(ctx context.Context, repo, rev, path string) ([]byte, error) {
	obj, ok, err := g.resolve(ctx, repo, rev+":"+path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s at %s: %w", path, rev, ports.ErrNotFound)
	}
	kind, err := g.run(ctx, repo, "cat-file", "-t", obj)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(kind) != "blob" {
		return nil, fmt.Errorf("%s at %s is not a file: %w", path, rev, ports.ErrNotFound)
	}
	out, err := g.run(ctx, repo, "cat-file", "blob", obj)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}
