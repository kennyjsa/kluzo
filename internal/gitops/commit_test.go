package gitops

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCommit(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo := newRepo(t, g)
	git(t, g, repo, "branch", "milestone/v1")
	s := WorktreeSpec{Repo: repo, Path: filepath.Join(t.TempDir(), "wt"), Branch: "issue/1-x", Base: "milestone/v1"}
	mustAdd(t, g, s)
	tip := git(t, g, repo, "rev-parse", "milestone/v1")

	// Nothing to record: HEAD as it is.
	if sha, err := g.Commit(ctx, repo, s.Path, "kluzo: node 1"); err != nil || sha != tip {
		t.Fatalf("empty commit = %s, %v; want %s", sha, err, tip)
	}
	write(t, filepath.Join(s.Path, "a.go"), "package a\n", 0o644)
	if err := os.Remove(filepath.Join(s.Path, "README.md")); err != nil {
		t.Fatal(err)
	}
	// The agent points the .git file of the worktree elsewhere: the commit
	// still lands on the node branch of the real repository.
	decoy := newRepo(t, g)
	write(t, filepath.Join(s.Path, ".git"), "gitdir: "+filepath.Join(decoy, ".git")+"\n", 0o644)
	sha, err := g.Commit(ctx, repo, s.Path, "kluzo: node 1")
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, g, repo, "rev-parse", "refs/heads/issue/1-x"); got != sha || sha == tip {
		t.Errorf("branch at %s, commit %s, tip %s", got, sha, tip)
	}
	if files := git(t, g, repo, "show", "--name-status", "--format=%an <%ae>", sha); files != "Kluzo <kluzo@localhost>\n\nD\tREADME.md\nA\ta.go" {
		t.Errorf("commit = %q", files)
	}
	if _, err := g.Commit(ctx, repo, t.TempDir(), "x"); err == nil {
		t.Error("a directory that is not a worktree was committed")
	}
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo := newRepo(t, g)
	from := commit(t, g, repo, map[string]string{"a.go": "1\n2\n3\n4\n5\n", "bin.dat": "x\x00y", "gone.txt": "bye\n", "q\"x_test.go": "func TestQ\n"})
	write(t, filepath.Join(repo, "bin.dat"), "z\x00w", 0o644)
	for _, f := range []string{"gone.txt", "q\"x_test.go"} {
		if err := os.Remove(filepath.Join(repo, f)); err != nil {
			t.Fatal(err)
		}
	}
	to := commit(t, g, repo, map[string]string{"a.go": "1\nTWO\n3\n5\n6\n7\n", "dir/new file.go": "x\n", "tricky.txt": "++ b/a.go\n-- a/gone.txt\n", "n\"y.go": "x //nolint\n"})
	files, err := g.Diff(ctx, repo, from, to)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]DiffFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	a := byPath["a.go"]
	if a.Added != 3 || a.Removed != 2 || !slices.Equal(a.Lines, [][2]int{{2, 2}, {5, 6}}) || a.Deleted || a.Created ||
		!slices.Equal(a.AddedText, []AddedLine{{2, "TWO"}, {5, "6"}, {6, "7"}}) || !slices.Equal(a.RemovedText, []AddedLine{{2, "2"}, {4, "4"}}) {
		t.Errorf("a.go = %+v", a)
	}
	if f := byPath["dir/new file.go"]; f.Added != 1 || !slices.Equal(f.Lines, [][2]int{{1, 1}}) || !f.Created {
		t.Errorf("new file = %+v", f)
	}
	// Added lines that look like headers are content.
	if f := byPath["tricky.txt"]; !slices.Equal(f.AddedText, []AddedLine{{1, "++ b/a.go"}, {2, "-- a/gone.txt"}}) || f.Deleted {
		t.Errorf("tricky file = %+v", f)
	}
	if f := byPath["gone.txt"]; f.Removed != 1 || len(f.Lines) != 0 || !f.Deleted || f.Created {
		t.Errorf("deleted file = %+v", f)
	}
	if f := byPath["bin.dat"]; !f.Binary || len(f.Lines) != 0 {
		t.Errorf("binary file = %+v", f)
	}
	// Names git quotes are read too.
	if f := byPath["q\"x_test.go"]; !f.Deleted || !slices.Equal(f.RemovedText, []AddedLine{{1, "func TestQ"}}) {
		t.Errorf("quoted deleted file = %+v", f)
	}
	if f := byPath["n\"y.go"]; !slices.Equal(f.AddedText, []AddedLine{{1, "x //nolint"}}) || !f.Created {
		t.Errorf("quoted new file = %+v", f)
	}
	if len(files) != 7 {
		t.Errorf("files = %+v", files)
	}
}
