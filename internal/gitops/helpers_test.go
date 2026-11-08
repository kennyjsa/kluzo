package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newGit returns the hardened runner with the PATH of the test and messages
// in English.
func newGit() *Git { return New(Config{ParentEnv: []string{"PATH=" + os.Getenv("PATH"), "LANG=C"}}) }

// git runs a command through the hardened runner and fails the test on error.
func git(t *testing.T, g *Git, dir string, args ...string) string {
	t.Helper()
	out, err := g.run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// newRepo creates a repository on main with one commit.
func newRepo(t *testing.T, g *Git) string {
	t.Helper()
	repo := t.TempDir()
	git(t, g, repo, "init", "-q", "-b", "main")
	commit(t, g, repo, map[string]string{"README.md": "hello\n"})
	return repo
}

// commit writes files in dir and commits them; it returns the new commit.
func commit(t *testing.T, g *Git, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		write(t, filepath.Join(dir, name), content, 0o644)
	}
	git(t, g, dir, "add", "-A")
	git(t, g, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "test")
	return git(t, g, dir, "rev-parse", "HEAD")
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
