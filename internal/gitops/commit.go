package gitops

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// identity is the author and committer of the commits Kluzo makes for a node.
var identity = []string{"-c", "user.name=Kluzo", "-c", "user.email=kluzo@localhost"}

// Commit records everything the session left in the node worktree as one
// commit on the node branch, and returns the commit (HEAD when there was
// nothing to record). Kluzo commits, never the agent (security.md). It runs
// only after Check, and with the git dir of the worktree as the main
// repository registered it, never through the .git file of the worktree,
// which the agent controls.
func (g *Git) Commit(ctx context.Context, repo, worktree, message string) (string, error) {
	dir, err := g.worktreeGitDir(ctx, repo, worktree)
	if err != nil {
		return "", err
	}
	at := []string{"--git-dir=" + dir, "--work-tree=" + worktree}
	cmd := func(args ...string) (string, error) {
		return g.run(ctx, worktree, append(append(append([]string(nil), identity...), at...), args...)...)
	}
	if _, err := cmd("add", "--all"); err != nil {
		return "", err
	}
	status, err := cmd("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) != "" {
		if _, err := cmd("commit", "--quiet", "--no-verify", "-m", message); err != nil {
			return "", err
		}
	}
	head, err := cmd("rev-parse", "--verify", "HEAD^{commit}")
	return strings.TrimSpace(head), err
}

// worktreeGitDir finds the git dir of the worktree at path among those the
// main repository registered: the one whose gitdir file points at path.
func (g *Git) worktreeGitDir(ctx context.Context, repo, path string) (string, error) {
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return "", err
	}
	want := filepath.Join(canonical(path), ".git")
	dirs, err := filepath.Glob(filepath.Join(common, "worktrees", "*"))
	if err != nil {
		return "", err
	}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "gitdir"))
		if err == nil && canonical(filepath.Dir(strings.TrimSpace(string(b))))+"/.git" == want {
			return d, nil
		}
	}
	return "", fmt.Errorf("%s is not a worktree of %s", path, repo)
}

// DiffFile is what a diff changed in one file.
type DiffFile struct {
	Path           string
	Added, Removed int // lines; zero for a binary file
	Binary         bool
	// Lines are the ranges of the new side the diff added or changed, both
	// ends included; what a finding of the round may point at.
	Lines [][2]int
}

// Diff returns what changed between two commits of the repository, file by
// file. It reads objects only: no external diff and no text conversion run.
func (g *Git) Diff(ctx context.Context, repo, from, to string) ([]DiffFile, error) {
	base := []string{"-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames"}
	stat, err := g.run(ctx, repo, append(base, "--numstat", "-z", "--end-of-options", from, to)...)
	if err != nil {
		return nil, err
	}
	var files []DiffFile
	index := map[string]int{}
	for _, rec := range strings.Split(strings.TrimSuffix(stat, "\x00"), "\x00") {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		f := DiffFile{Path: parts[2], Binary: parts[0] == "-"}
		f.Added, _ = strconv.Atoi(parts[0])
		f.Removed, _ = strconv.Atoi(parts[1])
		index[f.Path] = len(files)
		files = append(files, f)
	}
	patch, err := g.run(ctx, repo, append(base, "--unified=0", "--end-of-options", from, to)...)
	if err != nil {
		return nil, err
	}
	cur := -1
	sc := bufio.NewScanner(strings.NewReader(patch))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = -1
		case strings.HasPrefix(line, "+++ b/"):
			// git ends the name with a tab when it has a space
			if i, ok := index[strings.TrimSuffix(strings.TrimPrefix(line, "+++ b/"), "\t")]; ok {
				cur = i
			}
		case strings.HasPrefix(line, "@@ ") && cur >= 0:
			if r, ok := newRange(line); ok {
				files[cur].Lines = append(files[cur].Lines, r)
			}
		}
	}
	return files, sc.Err()
}

// newRange reads the new side of a hunk header, "@@ -a,b +c,d @@": lines c to
// c+d-1. A hunk that only removes lines (d = 0) has no new lines.
func newRange(header string) ([2]int, bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return [2]int{}, false
	}
	start, count, found := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
	from, err := strconv.Atoi(start)
	if err != nil {
		return [2]int{}, false
	}
	n := 1
	if found {
		if n, err = strconv.Atoi(count); err != nil || n == 0 {
			return [2]int{}, false
		}
	}
	return [2]int{from, from + n - 1}, true
}
