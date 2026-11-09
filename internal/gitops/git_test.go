package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
)

func TestRunnerArgsAndEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	bin := filepath.Join(dir, "fakegit")
	write(t, bin, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+out+".args\nenv > "+out+".env\n", 0o755)

	g := New(Config{Bin: bin, ParentEnv: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/home/me", "SSH_AUTH_SOCK=/tmp/agent", "GIT_DIR=/elsewhere"}})
	if _, err := g.run(context.Background(), dir, "status", "--short"); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(out + ".args")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "status", "--short"}
	if diff := cmp.Diff(wantArgs, strings.Fields(string(args))); diff != "" {
		t.Errorf("args (-want +got):\n%s", diff)
	}

	envOut, err := os.ReadFile(out + ".env")
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range strings.Fields(string(envOut)) {
		if k, _, _ := strings.Cut(kv, "="); k != "PWD" && k != "SHLVL" && k != "_" && k != "OLDPWD" {
			env = append(env, kv) // the shell adds the others
		}
	}
	slices.Sort(env)
	wantEnv := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"LANG=C.UTF-8", "PATH=/usr/bin:/bin",
	}
	if diff := cmp.Diff(wantEnv, env); diff != "" {
		t.Errorf("env (-want +got):\n%s", diff)
	}
}

// TestHooksDoNotRun plants hooks in .git/hooks and in a core.hooksPath set in
// the repository configuration; none runs when gitops commits or checks out.
func TestHooksDoNotRun(t *testing.T) {
	g := newGit()
	repo := newRepo(t, g)
	markers := t.TempDir()
	plant := func(dir string) {
		for _, hook := range []string{"pre-commit", "post-commit", "post-checkout", "reference-transaction"} {
			write(t, filepath.Join(dir, hook), "#!/bin/sh\ntouch "+filepath.Join(markers, hook)+"\n", 0o755)
		}
	}
	plant(filepath.Join(repo, ".git", "hooks"))

	// Control: the same commit by a git that only ignores the global and
	// system configuration does run the hook, so the planted hooks work.
	write(t, filepath.Join(repo, "control.txt"), "x\n", 0o644)
	plain := func(args ...string) {
		t.Helper()
		o, err := exec.Run(context.Background(), exec.Spec{
			Bin: "git", Args: args, Dir: repo,
			Env: []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
		})
		if err != nil || o.Status.Code != 0 {
			t.Fatalf("git %v: %v, %+v", args, err, o)
		}
	}
	plain("add", "control.txt")
	plain("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "control")
	if !exists(filepath.Join(markers, "pre-commit")) {
		t.Fatal("control: the planted pre-commit hook did not run")
	}
	if err := os.RemoveAll(markers); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(markers, 0o755); err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	plant(other)
	git(t, g, repo, "config", "core.hooksPath", other)

	commit(t, g, repo, map[string]string{"a.txt": "a\n"})
	if _, err := g.AddWorktree(context.Background(), WorktreeSpec{Repo: repo, Path: filepath.Join(t.TempDir(), "wt"), Branch: "node", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(markers)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("hook %s ran", e.Name())
	}
}

// TestGlobalConfigIgnored gives the runner a HOME, an XDG directory and a
// GIT_CONFIG_GLOBAL whose configuration defines an alias: none is read.
func TestGlobalConfigIgnored(t *testing.T) {
	home := t.TempDir()
	cfg := "[alias]\n\tst = status\n[core]\n\thooksPath = " + home + "\n"
	write(t, filepath.Join(home, ".gitconfig"), cfg, 0o644)
	write(t, filepath.Join(home, ".config", "git", "config"), cfg, 0o644)
	write(t, filepath.Join(home, "global"), cfg, 0o644)

	parent := []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "GIT_CONFIG_GLOBAL=" + filepath.Join(home, "global")}
	g := New(Config{ParentEnv: parent})
	repo := newRepo(t, g)

	_, err := g.run(context.Background(), repo, "st")
	var ge *Error
	if !errors.As(err, &ge) || !strings.Contains(ge.Stderr, "not a git command") {
		t.Errorf("alias from the global configuration ran: err = %v", err)
	}
	if out, err := g.run(context.Background(), repo, "config", "--get", "alias.st"); err == nil {
		t.Errorf("alias.st = %q, want it unset", out)
	}
}

func TestErrorNamesCommandAndStderr(t *testing.T) {
	g := newGit()
	_, err := g.run(context.Background(), t.TempDir(), "log", "-1")
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if ge.Code == 0 || !strings.Contains(err.Error(), "git log -1") || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("err = %q", err)
	}

	_, err = New(Config{Bin: filepath.Join(t.TempDir(), "missing")}).run(context.Background(), t.TempDir(), "status")
	if !errors.As(err, &ge) || ge.Err == nil || ge.Code != -1 {
		t.Errorf("missing binary: err = %v", err)
	}
}

func TestVersion(t *testing.T) {
	if v, err := newGit().Version(context.Background()); err != nil || v == "" {
		t.Errorf("Version = %q, %v", v, err)
	}
	old := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho git version 2.30.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v, err := New(Config{Bin: old}).Version(context.Background()); err == nil || v != "2.30.1" {
		t.Errorf("old git: %q, %v", v, err)
	}
}
