package gitops

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// withRemote returns a repository on main with a milestone branch, and a bare
// remote that has both.
func withRemote(t *testing.T, g *Git) (repo string, r Remote) {
	t.Helper()
	repo = newRepo(t, g)
	git(t, g, repo, "branch", "milestone/v1")
	r = Remote{URL: filepath.Join(t.TempDir(), "origin.git")}
	git(t, g, repo, "init", "-q", "--bare", r.URL)
	if err := g.Push(context.Background(), repo, r, "refs/heads/main:refs/heads/main", "refs/heads/milestone/v1:refs/heads/milestone/v1"); err != nil {
		t.Fatal(err)
	}
	return repo, r
}

func TestSyncBranch(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo, r := withRemote(t, g)
	tip := git(t, g, repo, "rev-parse", "milestone/v1")

	// Equal on both sides.
	if got, err := g.SyncBranch(ctx, repo, r, "milestone/v1"); err != nil || got != tip {
		t.Fatalf("equal: %s, %v", got, err)
	}
	// Ahead locally: kept, and the push sends it.
	git(t, g, repo, "checkout", "-q", "milestone/v1")
	ahead := commit(t, g, repo, map[string]string{"a.txt": "a\n"})
	git(t, g, repo, "checkout", "-q", "main")
	if got, err := g.SyncBranch(ctx, repo, r, "milestone/v1"); err != nil || got != ahead {
		t.Fatalf("ahead: %s, %v", got, err)
	}
	if err := g.Push(ctx, repo, r, "refs/heads/milestone/v1:refs/heads/milestone/v1"); err != nil {
		t.Fatal(err)
	}
	// Behind: fast-forwarded to the remote (someone pushed to the milestone).
	other := filepath.Join(t.TempDir(), "other")
	git(t, g, filepath.Dir(other), "clone", "-q", "-b", "milestone/v1", r.URL, other)
	remote := commit(t, g, other, map[string]string{"b.txt": "b\n"})
	git(t, g, other, "push", "-q", "origin", "milestone/v1")
	if got, err := g.SyncBranch(ctx, repo, r, "milestone/v1"); err != nil || got != remote || git(t, g, repo, "rev-parse", "milestone/v1") != remote {
		t.Fatalf("behind: %s, %v", got, err)
	}
	// Only on the remote: created locally.
	git(t, g, repo, "branch", "-D", "milestone/v1")
	if got, err := g.SyncBranch(ctx, repo, r, "milestone/v1"); err != nil || got != remote {
		t.Fatalf("remote only: %s, %v", got, err)
	}
	// Only locally: kept.
	git(t, g, repo, "branch", "milestone/v2", "main")
	if got, err := g.SyncBranch(ctx, repo, r, "milestone/v2"); err != nil || got != git(t, g, repo, "rev-parse", "main") {
		t.Fatalf("local only: %s, %v", got, err)
	}
	// Nowhere.
	if _, err := g.SyncBranch(ctx, repo, r, "milestone/v3"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("nowhere: %v", err)
	}
	// Diverged: refused for a person.
	git(t, g, repo, "checkout", "-q", "milestone/v1")
	commit(t, g, repo, map[string]string{"c.txt": "local\n"})
	git(t, g, repo, "checkout", "-q", "main")
	commit(t, g, other, map[string]string{"c.txt": "remote\n"})
	git(t, g, other, "push", "-q", "origin", "milestone/v1")
	if _, err := g.SyncBranch(ctx, repo, r, "milestone/v1"); !errors.Is(err, ErrDiverged) || !ports.IsPermanent(err) {
		t.Fatalf("diverged: %v", err)
	}
	if ok, err := g.HasBranch(ctx, repo, "milestone/v2"); !ok || err != nil {
		t.Errorf("HasBranch = %v, %v", ok, err)
	}
	if ok, err := g.HasBranch(ctx, repo, "milestone/v9"); ok || err != nil {
		t.Errorf("HasBranch of a missing branch = %v, %v", ok, err)
	}
}

// The token reaches git through the environment only, and the credential
// helpers are switched off.
func TestRemoteCredentials(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "git")
	write(t, bin, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+log+".args\nenv > "+log+".env\n", 0o755)
	g := New(Config{Bin: bin})
	if err := g.Push(context.Background(), dir, Remote{URL: "https://github.com/acme/demo.git", Token: "ghs_secret"}, "refs/heads/a:refs/heads/a"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(log + ".args")
	env, _ := os.ReadFile(log + ".env")
	if strings.Contains(string(args), "ghs_secret") || !strings.Contains(string(args), "credential.helper=\n") {
		t.Errorf("args = %s", args)
	}
	header := "GIT_CONFIG_VALUE_0=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_secret"))
	if !slices.Contains(strings.Split(string(env), "\n"), header) || !strings.Contains(string(env), "GIT_CONFIG_KEY_0=http.extraHeader") {
		t.Errorf("env = %s", env)
	}
}

// node checks out a node branch from the milestone in a worktree and commits
// files there; it returns the worktree and the commit.
func node(t *testing.T, g *Git, repo, branch string, files map[string]string) (string, string) {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := g.AddWorktree(context.Background(), WorktreeSpec{Repo: repo, Path: wt, Branch: branch, Base: "milestone/v1"}); err != nil {
		t.Fatal(err)
	}
	return wt, commit(t, g, wt, files)
}

func TestRebaseAndMerge(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo, _ := withRemote(t, g)
	wt, head := node(t, g, repo, "issue/1-a", map[string]string{"a.txt": "node\n"})

	// The milestone moves: the rebase puts the node on its tip.
	other, _ := node(t, g, repo, "issue/2-b", map[string]string{"b.txt": "b\n"})
	tip, err := g.Merge(ctx, MergeSpec{Repo: repo, Branch: "milestone/v1", Tip: git(t, g, repo, "rev-parse", "milestone/v1"),
		Head: git(t, g, other, "rev-parse", "HEAD"), Message: MergeMessage("2", "B", "nd_2", "ws_1", 1)})
	if err != nil {
		t.Fatal(err)
	}
	rebased, conflict, err := g.Rebase(ctx, repo, wt, tip)
	if err != nil || conflict != nil || rebased == head || git(t, g, repo, "rev-parse", rebased+"^") != tip {
		t.Fatalf("rebase: %s %+v %v", rebased, conflict, err)
	}

	merge, err := g.Merge(ctx, MergeSpec{Repo: repo, Branch: "milestone/v1", Tip: tip, Head: rebased, Message: MergeMessage("1", "Agenda API", "nd_1", "ws_1", 2)})
	if err != nil {
		t.Fatal(err)
	}
	parents := strings.Fields(git(t, g, repo, "rev-list", "--parents", "-n", "1", merge))
	msg := git(t, g, repo, "log", "-1", "--format=%B", merge)
	if git(t, g, repo, "rev-parse", "milestone/v1") != merge || !slices.Equal(parents, []string{merge, tip, rebased}) ||
		!strings.HasPrefix(msg, "Merge #1: Agenda API") || !strings.Contains(msg, "Kluzo-Node: nd_1\nKluzo-Workspace: ws_1\nKluzo-Attempt: 2") ||
		git(t, g, repo, "log", "-1", "--format=%an <%ae>", merge) != "Kluzo <kluzo@localhost>" {
		t.Errorf("merge %s: parents %v, message %q", merge, parents, msg)
	}
	if got := git(t, g, repo, "show", merge+":a.txt"); got != "node" {
		t.Errorf("a.txt at the merge = %q", got)
	}
	// Run again: the same merge, nothing new.
	if again, err := g.Merge(ctx, MergeSpec{Repo: repo, Branch: "milestone/v1", Tip: tip, Head: rebased, Message: "x"}); err != nil || again != merge {
		t.Errorf("merge again = %s, %v", again, err)
	}
	// A merge the remote refused is undone by compare-and-swap.
	if err := g.ResetBranch(ctx, repo, "milestone/v1", tip, merge); err != nil || git(t, g, repo, "rev-parse", "milestone/v1") != tip {
		t.Fatalf("reset: %v", err)
	}
	if err := g.ResetBranch(ctx, repo, "milestone/v1", merge, rebased); err == nil {
		t.Error("reset from a commit the branch is not at")
	}
	if !g.Contains(ctx, repo, merge, rebased) || g.Contains(ctx, repo, tip, rebased) {
		t.Error("Contains")
	}
	git(t, g, repo, "update-ref", "refs/heads/milestone/v1", merge)
	// A tip other than the one rebased on is refused.
	_, stale := node(t, g, repo, "issue/3-c", map[string]string{"c.txt": "c\n"})
	if _, err := g.Merge(ctx, MergeSpec{Repo: repo, Branch: "milestone/v1", Tip: tip, Head: stale, Message: "x"}); !ports.IsPermanent(err) {
		t.Errorf("stale tip: %v", err)
	}
	// A milestone branch checked out somewhere is refused.
	git(t, g, repo, "checkout", "-q", "milestone/v1")
	if _, err := g.Merge(ctx, MergeSpec{Repo: repo, Branch: "milestone/v1", Tip: merge, Head: stale, Message: "x"}); !ports.IsPermanent(err) || !strings.Contains(err.Error(), "checked out") {
		t.Errorf("checked out: %v", err)
	}
}

func TestRebaseConflict(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo, _ := withRemote(t, g)
	wt, head := node(t, g, repo, "issue/1-a", map[string]string{"README.md": "node\n"})
	git(t, g, repo, "checkout", "-q", "milestone/v1")
	tip := commit(t, g, repo, map[string]string{"README.md": "milestone\n"})
	git(t, g, repo, "checkout", "-q", "main")

	got, conflict, err := g.Rebase(ctx, repo, wt, tip)
	if err != nil || got != "" || conflict == nil || !slices.Equal(conflict.Files, []string{"README.md"}) || !strings.Contains(conflict.Markers, "<<<<<<<") {
		t.Fatalf("conflict: %q %+v %v", got, conflict, err)
	}
	// Aborted: the branch and the worktree are as before.
	if git(t, g, wt, "rev-parse", "HEAD") != head || git(t, g, wt, "status", "--porcelain") != "" {
		t.Errorf("not aborted: %s", git(t, g, wt, "status", "--porcelain"))
	}
	// A rebase that stops for another reason (a dirty worktree) is permanent.
	write(t, filepath.Join(wt, "README.md"), "dirty\n", 0o644)
	if _, conflict, err := g.Rebase(ctx, repo, wt, tip); conflict != nil || !ports.IsPermanent(err) {
		t.Errorf("dirty worktree: %+v %v", conflict, err)
	}
}

func TestRemoveCheckout(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo := newRepo(t, g)
	path := filepath.Join(t.TempDir(), "co")
	if err := g.Checkout(ctx, repo, path, "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveCheckout(ctx, repo, path); err != nil || exists(path) {
		t.Fatalf("registered: %v, exists %v", err, exists(path))
	}
	write(t, filepath.Join(path, "left.txt"), "x", 0o644)
	if err := g.RemoveCheckout(ctx, repo, path); err != nil || exists(path) {
		t.Fatalf("left behind: %v", err)
	}
	if strings.Contains(git(t, g, repo, "worktree", "list"), path) {
		t.Error("still registered")
	}
}

// RemoteBranches lists which branches the remote has; DeleteRemoteBranches
// deletes them, and a branch already absent counts as deleted.
func TestDeleteRemoteBranches(t *testing.T) {
	ctx := context.Background()
	g := newGit()
	repo, r := withRemote(t, g)
	git(t, g, repo, "branch", "issue/1-a", "main")
	if err := g.Push(ctx, repo, r, "+refs/heads/issue/1-a:refs/heads/issue/1-a"); err != nil {
		t.Fatal(err)
	}
	if got, err := g.RemoteBranches(ctx, repo, r, "issue/2-b", "issue/1-a", "milestone/v1"); err != nil || !slices.Equal(got, []string{"issue/1-a", "milestone/v1"}) {
		t.Fatalf("RemoteBranches = %v, %v", got, err)
	}
	if err := g.DeleteRemoteBranches(ctx, repo, r, "issue/1-a", "issue/2-b"); err != nil {
		t.Fatal(err)
	}
	if got, err := g.RemoteBranches(ctx, repo, r, "issue/1-a", "milestone/v1"); err != nil || !slices.Equal(got, []string{"milestone/v1"}) {
		t.Fatalf("after the delete: %v, %v", got, err)
	}
	if err := g.DeleteRemoteBranches(ctx, repo, r, "issue/1-a"); err != nil {
		t.Errorf("absent: %v", err)
	}
	if got, err := g.RemoteBranches(ctx, repo, r); err != nil || got != nil {
		t.Errorf("none asked: %v, %v", got, err)
	}
}
