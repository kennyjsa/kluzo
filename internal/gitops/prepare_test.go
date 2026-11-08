package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// worktreeBlock is the worktree section of the valid configuration of
// internal/config, replaced by each test.
const worktreeBlock = "worktree:\n  setup: \"bash tools/prepare-worktree.sh\"\n  include: [\".env\"]\n  timeout: 15m\n"

// kluzoYAML returns a valid kluzo.yaml with the given worktree section.
func kluzoYAML(t *testing.T, worktree string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "config", "testdata", "valid", "kluzo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), worktreeBlock) {
		t.Fatal("the worktree section of the config fixture changed")
	}
	return strings.Replace(string(data), worktreeBlock, worktree, 1)
}

func TestPrepare(t *testing.T) {
	tests := []struct {
		name     string
		worktree string                              // the worktree section at the milestone tip
		files    func(t *testing.T, repo, wt string) // untracked files of the main checkout
		check    func(t *testing.T, wt string, err error)
	}{
		{
			name:     "copies includes and runs setup with the given environment",
			worktree: "worktree:\n  setup: 'echo \"$COMPOSE_PROJECT_NAME $(pwd)\" > setup.out; cat .env >> setup.out'\n  include: [.env, config/local.yaml, missing.env]\n  timeout: 1m\n",
			files: func(t *testing.T, repo, _ string) {
				write(t, filepath.Join(repo, ".env"), "SECRET=1\n", 0o600)
				write(t, filepath.Join(repo, "config", "local.yaml"), "a: 1\n", 0o640)
			},
			check: func(t *testing.T, wt string, err error) {
				if err != nil {
					t.Fatal(err)
				}
				out, rerr := os.ReadFile(filepath.Join(wt, "setup.out"))
				if want := "kluzo-ws-n1 " + canonical(wt) + "\nSECRET=1\n"; rerr != nil || string(out) != want {
					t.Errorf("setup.out = %q, %v; want %q", out, rerr, want)
				}
				for path, mode := range map[string]os.FileMode{".env": 0o600, "config/local.yaml": 0o640} {
					fi, serr := os.Stat(filepath.Join(wt, path))
					if serr != nil || fi.Mode().Perm() != mode {
						t.Errorf("%s: %v, mode %v; want %v", path, serr, fi.Mode(), mode)
					}
				}
				if exists(filepath.Join(wt, "missing.env")) {
					t.Error("missing include created")
				}
			},
		},
		{
			name:     "empty setup: only the copy",
			worktree: "worktree:\n  include: [.env]\n",
			files:    func(t *testing.T, repo, _ string) { write(t, filepath.Join(repo, ".env"), "X=1\n", 0o600) },
			check: func(t *testing.T, wt string, err error) {
				if err != nil || !exists(filepath.Join(wt, ".env")) {
					t.Errorf("err = %v, .env copied: %v", err, exists(filepath.Join(wt, ".env")))
				}
			},
		},
		{
			name:     "setup fails",
			worktree: "worktree:\n  setup: 'echo working; echo boom >&2; exit 3'\n",
			check: func(t *testing.T, _ string, err error) {
				se := setupError(t, err)
				if se.Code != 3 || se.TimedOut || !strings.Contains(se.Output, "working") || !strings.Contains(se.Output, "boom") {
					t.Errorf("SetupError = %+v", se)
				}
			},
		},
		{
			name:     "setup times out with the default timeout",
			worktree: "worktree:\n  setup: 'sleep 30'\n",
			check: func(t *testing.T, _ string, err error) {
				if se := setupError(t, err); !se.TimedOut {
					t.Errorf("SetupError = %+v, want timed out", se)
				}
			},
		},
		{
			name:     "include that is a symlink",
			worktree: "worktree:\n  include: [.env]\n",
			files: func(t *testing.T, repo, _ string) {
				if err := os.Symlink("/etc/passwd", filepath.Join(repo, ".env")); err != nil {
					t.Fatal(err)
				}
			},
			check: wantRefused("is a symlink"),
		},
		{
			name:     "include through a directory symlink out of the checkout",
			worktree: "worktree:\n  include: [outside/secret]\n",
			files: func(t *testing.T, repo, _ string) {
				dir := t.TempDir()
				write(t, filepath.Join(dir, "secret"), "s\n", 0o600)
				if err := os.Symlink(dir, filepath.Join(repo, "outside")); err != nil {
					t.Fatal(err)
				}
			},
			check: wantRefused("escapes"),
		},
		{
			name:     "include that is a directory",
			worktree: "worktree:\n  include: [config]\n",
			files:    func(t *testing.T, repo, _ string) { write(t, filepath.Join(repo, "config", "a"), "a\n", 0o600) },
			check:    wantRefused("not a regular file"),
		},
		{
			name:     "include over a symlink in the worktree",
			worktree: "worktree:\n  include: [.env]\n",
			files: func(t *testing.T, repo, wt string) {
				write(t, filepath.Join(repo, ".env"), "X=1\n", 0o600)
				if err := os.Symlink("README.md", filepath.Join(wt, ".env")); err != nil {
					t.Fatal(err)
				}
			},
			check: wantRefused("not a regular file in the worktree"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGit()
			repo := newRepo(t, g)
			commit(t, g, repo, map[string]string{".kluzo/kluzo.yaml": kluzoYAML(t, tt.worktree)})
			git(t, g, repo, "branch", "milestone/m")
			// Neither the working copy nor the worktree is read: both say to fail.
			write(t, filepath.Join(repo, ".kluzo", "kluzo.yaml"), kluzoYAML(t, "worktree:\n  setup: 'exit 7'\n"), 0o644)
			wt := filepath.Join(t.TempDir(), "wt")
			mustAdd(t, g, WorktreeSpec{Repo: repo, Path: wt, Branch: "node/1", Base: "milestone/m"})
			write(t, filepath.Join(wt, ".kluzo", "kluzo.yaml"), kluzoYAML(t, "worktree:\n  setup: 'exit 9'\n"), 0o644)
			if tt.files != nil {
				tt.files(t, repo, wt)
			}

			err := g.Prepare(context.Background(), PrepareSpec{
				Repo: repo, Worktree: wt, Base: "milestone/m",
				Env:            []string{"PATH=" + os.Getenv("PATH"), "COMPOSE_PROJECT_NAME=kluzo-ws-n1"},
				DefaultTimeout: 300 * time.Millisecond,
			})
			tt.check(t, wt, err)
		})
	}
}

func setupError(t *testing.T, err error) *SetupError {
	t.Helper()
	var se *SetupError
	if !errors.As(err, &se) || !errors.Is(err, ErrSetup) {
		t.Fatalf("err = %v, want *SetupError", err)
	}
	if ports.IsPermanent(err) || !se.Cause().Retryable() {
		t.Errorf("%v is permanent or not retryable", err)
	}
	return se
}

func wantRefused(text string) func(*testing.T, string, error) {
	return func(t *testing.T, _ string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), text) || errors.Is(err, ErrSetup) {
			t.Errorf("err = %v, want a refused include (%q)", err, text)
		}
	}
}

func TestPrepareSetupCannotStart(t *testing.T) {
	g := newGit()
	repo := newRepo(t, g)
	commit(t, g, repo, map[string]string{".kluzo/kluzo.yaml": kluzoYAML(t, "worktree:\n  setup: 'true'\n")})
	err := g.Prepare(context.Background(), PrepareSpec{Repo: repo, Worktree: filepath.Join(t.TempDir(), "gone"), Base: "main"})
	if se := setupError(t, err); se.Err == nil || se.Code != -1 {
		t.Errorf("SetupError = %+v, want a start failure", se)
	}
}

func TestPrepareConfigFromTip(t *testing.T) {
	g := newGit()
	repo := newRepo(t, g)
	err := g.Prepare(context.Background(), PrepareSpec{Repo: repo, Worktree: repo, Base: "main"})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("no kluzo.yaml at the tip: err = %v, want ErrNotFound", err)
	}
	commit(t, g, repo, map[string]string{".kluzo/kluzo.yaml": "version: 1\nbogus: true\n"})
	if err := g.Prepare(context.Background(), PrepareSpec{Repo: repo, Worktree: repo, Base: "main"}); err == nil || !strings.Contains(err.Error(), "main:.kluzo/kluzo.yaml") {
		t.Errorf("invalid kluzo.yaml at the tip: err = %v", err)
	}
}

func TestCopyIncludesRefusesEscape(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x"} {
		if err := copyIncludes(from, to, []string{p}); err == nil || !strings.Contains(err.Error(), "leaves the repository") {
			t.Errorf("copyIncludes(%q) = %v, want refused", p, err)
		}
	}
}
