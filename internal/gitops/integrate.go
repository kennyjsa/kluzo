package gitops

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/text"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Remote is the repository Kluzo fetches from and pushes to, with the
// credentials of the bot: never the agent's, never the host's (security.md).
type Remote struct {
	URL   string // https URL of the repository; a local path in tests
	Token string // the bot's token for https; empty for none
}

// remoteRun runs git against the remote. The token goes in an HTTP header set
// through the environment (GIT_CONFIG_*), never on the command line, and the
// credential helpers of the repository are switched off.
func (g *Git) remoteRun(ctx context.Context, dir string, r Remote, args ...string) (string, error) {
	env := append([]string(nil), g.env...)
	if r.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.Token))
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic)
	}
	full := append(append(append([]string(nil), hardening...), "-c", "credential.helper=", "-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always", "-c", "protocol.file.allow=always"), args...)
	o, err := exec.Run(ctx, exec.Spec{Bin: g.bin, Args: full, Dir: dir, Env: env})
	if err != nil || o.Status.Code != 0 {
		return o.Stdout, &Error{Args: args, Code: o.Status.Code, Stderr: o.Stderr, Err: err}
	}
	return o.Stdout, nil
}

// Push sends the refspecs to the remote. A refspec with "+" forces (the node
// branches, which Kluzo rewrites); the milestone branch is only fast-forwarded.
func (g *Git) Push(ctx context.Context, repo string, r Remote, refspecs ...string) error {
	_, err := g.remoteRun(ctx, repo, r, append([]string{"push", "--porcelain", "--no-verify", "--end-of-options", r.URL}, refspecs...)...)
	return err
}

// ErrDiverged means the local and the remote branch both have commits the
// other lacks: a person must reconcile them.
var ErrDiverged = errors.New("the local and the remote branch diverged")

// SyncBranch reconciles the local branch with the remote one and returns the
// local tip: a branch only on the remote is created locally, a local branch
// behind the remote is fast-forwarded, one ahead is kept (the next push sends
// it). A branch on neither side is ErrNotFound; diverged branches ErrDiverged.
func (g *Git) SyncBranch(ctx context.Context, repo string, r Remote, branch string) (string, error) {
	ref := "refs/heads/" + branch
	ls, err := g.remoteRun(ctx, repo, r, "ls-remote", "--end-of-options", r.URL, ref)
	if err != nil {
		return "", err
	}
	local, hasLocal, err := g.resolve(ctx, repo, ref+"^{commit}")
	if err != nil {
		return "", err
	}
	remote, _, _ := strings.Cut(strings.TrimSpace(ls), "\t")
	if remote == "" {
		if !hasLocal {
			return "", fmt.Errorf("branch %s: %w", branch, ports.ErrNotFound)
		}
		return local, nil
	}
	if remote == local {
		return local, nil
	}
	fetched := "refs/kluzo/fetched/" + branch
	if _, err := g.remoteRun(ctx, repo, r, "fetch", "--no-tags", "--no-write-fetch-head", "--end-of-options", r.URL, "+"+ref+":"+fetched); err != nil {
		return "", err
	}
	switch {
	case !hasLocal:
		_, err = g.run(ctx, repo, "update-ref", "--end-of-options", ref, remote, strings.Repeat("0", len(remote)))
		return remote, err
	case g.ancestor(ctx, repo, remote, local):
		return local, nil // ahead: the next push sends it
	case g.ancestor(ctx, repo, local, remote):
		_, err = g.run(ctx, repo, "update-ref", "--end-of-options", ref, remote, local)
		return remote, err
	}
	return "", fmt.Errorf("branch %s (local %s, remote %s): %w: %w", branch, local, remote, ErrDiverged, ports.ErrPermanent)
}

// HasBranch reports whether the local branch exists.
func (g *Git) HasBranch(ctx context.Context, repo, branch string) (bool, error) {
	_, ok, err := g.resolve(ctx, repo, "refs/heads/"+branch+"^{commit}")
	return ok, err
}

// Contains reports whether the commit tip contains commit c.
func (g *Git) Contains(ctx context.Context, repo, tip, c string) bool {
	return g.ancestor(ctx, repo, c, tip)
}

// ancestor reports whether a is an ancestor of b (or equal to it).
func (g *Git) ancestor(ctx context.Context, repo, a, b string) bool {
	_, err := g.run(ctx, repo, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	return err == nil
}

// conflictKeep is how much of the conflict markers a rebase conflict keeps.
const conflictKeep = 12_000

// Conflict is a rebase that stopped: the unmerged files and their markers.
type Conflict struct {
	Files   []string
	Markers string
}

// Rebase rebases the node branch checked out in the worktree on onto and
// returns its new head. A conflict is aborted and returned as a Conflict,
// with an empty head. It runs with the git dir of the worktree as the main
// repository registered it, like Commit, and only while no session runs.
func (g *Git) Rebase(ctx context.Context, repo, worktree, onto string) (string, *Conflict, error) {
	dir, err := g.worktreeGitDir(ctx, repo, worktree)
	if err != nil {
		return "", nil, err
	}
	cmd := func(args ...string) (string, error) {
		at := []string{"--git-dir=" + dir, "--work-tree=" + worktree}
		return g.run(ctx, worktree, append(append(append([]string(nil), identity...), at...), args...)...)
	}
	if _, rerr := cmd("rebase", "--no-autostash", "--no-autosquash", "--no-update-refs", "--end-of-options", onto); rerr != nil {
		unmerged, err := cmd("diff", "--name-only", "--diff-filter=U", "-z")
		if err != nil {
			return "", nil, errors.Join(rerr, err)
		}
		files := strings.Split(strings.TrimSuffix(unmerged, "\x00"), "\x00")
		if unmerged == "" {
			files = nil
		}
		markers, _ := cmd("diff", "--no-ext-diff", "--no-textconv", "--no-color") // best effort: the files are the cause
		// A rebase refused before it started (a dirty worktree) has nothing to abort.
		if _, err := cmd("rebase", "--abort"); err != nil && len(files) > 0 {
			return "", nil, errors.Join(rerr, err)
		}
		if len(files) == 0 {
			// Not a conflict (a dirty worktree, say): running it again does
			// not help.
			return "", nil, fmt.Errorf("rebase on %s: %w: %w", onto, rerr, ports.ErrPermanent)
		}
		return "", &Conflict{Files: files, Markers: text.Clip(markers, conflictKeep, "…(truncated)")}, nil
	}
	head, err := cmd("rev-parse", "--verify", "HEAD^{commit}")
	return strings.TrimSpace(head), nil, err
}

// MergeSpec is a node merged into the milestone branch (ADR 0009).
type MergeSpec struct {
	Repo    string
	Branch  string // the milestone branch
	Tip     string // its commit the node was rebased on; the merge moves it only from there
	Head    string // the node commit
	Message string // "Merge #<issue>: <title>" and the Kluzo trailers
}

// Merge records the merge commit of the node on the milestone branch, as
// git merge --no-ff would, without a checkout: the tree comes from
// merge-tree and the branch moves from Tip by compare-and-swap. A node
// already in the branch returns its merge commit, so a run again does not
// merge twice. It refuses a milestone branch checked out in a worktree, whose
// files the move would leave behind.
func (g *Git) Merge(ctx context.Context, s MergeSpec) (string, error) {
	ref := "refs/heads/" + s.Branch
	cur, ok, err := g.resolve(ctx, s.Repo, ref+"^{commit}")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("branch %s: %w", s.Branch, ports.ErrNotFound)
	}
	if g.ancestor(ctx, s.Repo, s.Head, cur) {
		out, err := g.run(ctx, s.Repo, "rev-list", "--ancestry-path", "--merges", "--reverse", "--end-of-options", s.Head+".."+cur)
		if err != nil {
			return "", err
		}
		if first, _, _ := strings.Cut(out, "\n"); first != "" {
			return first, nil
		}
		return cur, nil
	}
	if cur != s.Tip {
		return "", fmt.Errorf("branch %s is at %s, not at %s the node was rebased on: %w", s.Branch, cur, s.Tip, ports.ErrPermanent)
	}
	wts, err := g.worktrees(ctx, s.Repo)
	if err != nil {
		return "", err
	}
	for path, b := range wts {
		if b == ref {
			return "", fmt.Errorf("%s is checked out in %s; check out another branch there: %w", s.Branch, path, ports.ErrPermanent)
		}
	}
	tree, err := g.run(ctx, s.Repo, "merge-tree", "--write-tree", "--no-messages", "--end-of-options", s.Tip, s.Head)
	if err != nil {
		return "", fmt.Errorf("merge %s into %s: %w: %w", s.Head, s.Branch, err, ports.ErrPermanent)
	}
	tree, _, _ = strings.Cut(tree, "\n")
	sha, err := g.run(ctx, s.Repo, append(append([]string(nil), identity...), "commit-tree", "-p", s.Tip, "-p", s.Head, "-m", s.Message, "--end-of-options", tree)...)
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(sha)
	if _, err := g.run(ctx, s.Repo, "update-ref", "-m", "kluzo: merge "+s.Head, "--end-of-options", ref, sha, s.Tip); err != nil {
		return "", err
	}
	return sha, nil
}

// ResetBranch moves the local branch from cur back to to, by
// compare-and-swap: a merge the remote refused is undone, so the next sync
// does not find the branches diverged.
func (g *Git) ResetBranch(ctx context.Context, repo, branch, to, cur string) error {
	_, err := g.run(ctx, repo, "update-ref", "-m", "kluzo: undo a merge the remote refused", "--end-of-options", "refs/heads/"+branch, to, cur)
	return err
}

// MergeMessage is the message of a node's merge commit (ADR 0009).
func MergeMessage(issue, title, node, workspace string, attempt int) string {
	return fmt.Sprintf("Merge #%s: %s\n\nKluzo-Node: %s\nKluzo-Workspace: %s\nKluzo-Attempt: %s\n", issue, title, node, workspace, strconv.Itoa(attempt))
}

// RemoveCheckout removes a checkout of the repository at path, registered or
// left behind, and its directory.
func (g *Git) RemoveCheckout(ctx context.Context, repo, path string) error {
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
	return os.RemoveAll(path)
}
