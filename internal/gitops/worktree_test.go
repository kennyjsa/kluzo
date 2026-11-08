package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

func TestAddWorktree(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		base    string // instead of the milestone branch
		before  func(t *testing.T, g *Git, s WorktreeSpec, tip string)
		wantErr error  // matched with errors.Is
		errText string // contained in the error
	}{
		{
			name:   "new branch from the milestone tip",
			before: func(*testing.T, *Git, WorktreeSpec, string) {},
		},
		{
			name: "repeat: worktree already there",
			before: func(t *testing.T, g *Git, s WorktreeSpec, _ string) {
				mustAdd(t, g, s)
			},
		},
		{
			name: "repeat after the milestone moved: still the birth commit",
			before: func(t *testing.T, g *Git, s WorktreeSpec, _ string) {
				mustAdd(t, g, s)
				commit(t, g, s.Path, map[string]string{"node.txt": "work\n"})
				commit(t, g, s.Repo, map[string]string{"later.txt": "later\n"})
			},
		},
		{
			name: "branch exists without worktree: reused",
			before: func(t *testing.T, g *Git, s WorktreeSpec, tip string) {
				git(t, g, s.Repo, "branch", s.Branch, tip)
			},
		},
		{
			name: "worktree directory deleted: pruned and created again",
			before: func(t *testing.T, g *Git, s WorktreeSpec, _ string) {
				mustAdd(t, g, s)
				if err := os.RemoveAll(s.Path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "missing base",
			base:    "nope",
			before:  func(*testing.T, *Git, WorktreeSpec, string) {},
			wantErr: ports.ErrNotFound,
		},
		{
			name: "path holds another branch",
			before: func(t *testing.T, g *Git, s WorktreeSpec, _ string) {
				s.Branch = "other"
				mustAdd(t, g, s)
			},
			errText: `not on "refs/heads/node/1"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGit()
			repo := newRepo(t, g)
			git(t, g, repo, "branch", "milestone/m")
			tip := git(t, g, repo, "rev-parse", "milestone/m")
			commit(t, g, repo, map[string]string{"main-only.txt": "x\n"}) // main is not the base
			s := WorktreeSpec{Repo: repo, Path: filepath.Join(t.TempDir(), "wt"), Branch: "node/1", Base: "milestone/m"}
			if tt.base != "" {
				s.Base = tt.base
			}
			tt.before(t, g, s, tip)

			got, err := g.AddWorktree(ctx, s)
			switch {
			case tt.wantErr != nil || tt.errText != "":
				if err == nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), tt.errText) {
					t.Fatalf("err = %v, want %v %q", err, tt.wantErr, tt.errText)
				}
				return
			case err != nil:
				t.Fatal(err)
			}
			if got != tip {
				t.Errorf("AddWorktree = %s, want the milestone tip %s", got, tip)
			}
			if b := git(t, g, s.Path, "symbolic-ref", "HEAD"); b != "refs/heads/node/1" {
				t.Errorf("worktree on %s", b)
			}
			if exists(filepath.Join(s.Path, "main-only.txt")) {
				t.Error("the worktree started from main, not from the milestone")
			}
		})
	}
}

func mustAdd(t *testing.T, g *Git, s WorktreeSpec) {
	t.Helper()
	if _, err := g.AddWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveWorktree(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo := newRepo(t, g)
	s := WorktreeSpec{Repo: repo, Path: filepath.Join(t.TempDir(), "wt"), Branch: "node/1", Base: "main"}
	mustAdd(t, g, s)
	write(t, filepath.Join(s.Path, "dirty.txt"), "uncommitted\n", 0o644)
	git(t, g, repo, "worktree", "lock", s.Path)

	for i := range 2 { // the second run finds nothing to do
		if err := g.RemoveWorktree(ctx, repo, s.Path, s.Branch); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if exists(s.Path) {
		t.Error("worktree directory still there")
	}
	if _, ok, err := g.resolve(ctx, repo, "refs/heads/node/1"); ok || err != nil {
		t.Errorf("branch still there (err %v)", err)
	}
	if out := git(t, g, repo, "worktree", "list", "--porcelain"); strings.Contains(out, "node/1") {
		t.Errorf("worktree still registered:\n%s", out)
	}
	mustAdd(t, g, s) // the node can start again
}

func TestReadAt(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo := newRepo(t, g)
	commit(t, g, repo, map[string]string{".kluzo/kluzo.yaml": "tip\n"})
	write(t, filepath.Join(repo, ".kluzo", "kluzo.yaml"), "working copy\n", 0o644)

	got, err := g.ReadAt(ctx, repo, "refs/heads/main", ".kluzo/kluzo.yaml")
	if err != nil || string(got) != "tip\n" {
		t.Errorf("ReadAt = %q, %v; want the committed content", got, err)
	}
	for _, tt := range []struct{ rev, path string }{
		{"refs/heads/main", "missing.yaml"},
		{"refs/heads/main", ".kluzo"}, // a directory
		{"refs/heads/nope", ".kluzo/kluzo.yaml"},
	} {
		if _, err := g.ReadAt(ctx, repo, tt.rev, tt.path); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("ReadAt(%s, %s) err = %v, want ErrNotFound", tt.rev, tt.path, err)
		}
	}
	if _, err := g.ReadAt(ctx, t.TempDir(), "HEAD", "x"); err == nil || errors.Is(err, ports.ErrNotFound) {
		t.Errorf("outside a repository: err = %v, want a git error", err)
	}
}
