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
	Deleted        bool // the file is gone on the new side
	Created        bool // the file is new: absent on the old side
	// Lines are the ranges of the new side the diff added or changed, both
	// ends included; what a finding of the round may point at.
	Lines [][2]int
	// AddedText are the lines the diff added, with their numbers on the new
	// side, and RemovedText the lines it removed, with their numbers on the
	// old side, for the check after the round.
	AddedText, RemovedText []AddedLine
}

// AddedLine is a line a diff added or removed.
type AddedLine struct {
	N    int
	Text string
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
	status, err := g.run(ctx, repo, append(base, "--name-status", "-z", "--end-of-options", from, to)...)
	if err != nil {
		return nil, err
	}
	recs := strings.Split(strings.TrimSuffix(status, "\x00"), "\x00")
	for k := 0; k+1 < len(recs); k += 2 {
		if i, ok := index[recs[k+1]]; ok {
			files[i].Deleted = recs[k] == "D"
			files[i].Created = recs[k] == "A"
		}
	}
	patch, err := g.run(ctx, repo, append(base, "--unified=0", "--end-of-options", from, to)...)
	if err != nil {
		return nil, err
	}
	// The lines of a hunk are read by the counts of its header, so a content
	// line that looks like a header ("--- a/x", "+++ b/x") stays content.
	cur := -1
	var next, oldLeft, newLeft, oldNext int
	sc := bufio.NewScanner(strings.NewReader(patch))
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case oldLeft+newLeft > 0 && strings.HasPrefix(line, "+"):
			if cur >= 0 {
				files[cur].AddedText = append(files[cur].AddedText, AddedLine{N: next, Text: line[1:]})
			}
			next++
			newLeft--
		case oldLeft+newLeft > 0 && strings.HasPrefix(line, "-"):
			if cur >= 0 {
				files[cur].RemovedText = append(files[cur].RemovedText, AddedLine{N: oldNext, Text: line[1:]})
			}
			oldNext++
			oldLeft--
		case strings.HasPrefix(line, "\\"): // "\ No newline at end of file"
		case strings.HasPrefix(line, "diff --git "):
			cur, oldLeft, newLeft = -1, 0, 0
		case strings.HasPrefix(line, "--- "):
			if name, ok := headerPath(line, "a/"); ok && cur < 0 {
				cur = indexOf(index, name) // the new side may be /dev/null
			}
		case strings.HasPrefix(line, "+++ "):
			if name, ok := headerPath(line, "b/"); ok {
				cur = indexOf(index, name)
			}
		case strings.HasPrefix(line, "@@ "):
			o, n, ok := hunkRanges(line)
			if !ok {
				continue
			}
			oldNext, oldLeft, next, newLeft = o[0], o[1], n[0], n[1]
			if cur >= 0 && n[1] > 0 {
				files[cur].Lines = append(files[cur].Lines, [2]int{n[0], n[0] + n[1] - 1})
			}
		}
	}
	return files, sc.Err()
}

func indexOf(index map[string]int, name string) int {
	if i, ok := index[name]; ok {
		return i
	}
	return -1
}

// headerPath reads the name of a "--- a/x" or "+++ b/x" header. git quotes a
// name with special characters C-style ("a/x\"y"), even with
// core.quotePath=false, and ends one with a space with a tab.
func headerPath(line, side string) (string, bool) {
	rest := strings.TrimSuffix(line[4:], "\t")
	if strings.HasPrefix(rest, `"`) {
		u, err := strconv.Unquote(rest)
		if err != nil {
			return "", false
		}
		rest = u
	}
	return strings.CutPrefix(rest, side)
}

// hunkRanges reads "@@ -a,b +c,d @@": the start and the count of each side
// (a count left out is 1).
func hunkRanges(header string) (old, new [2]int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return old, new, false
	}
	parse := func(s string) ([2]int, bool) {
		start, count, found := strings.Cut(s[1:], ",")
		from, err := strconv.Atoi(start)
		if err != nil {
			return [2]int{}, false
		}
		n := 1
		if found {
			if n, err = strconv.Atoi(count); err != nil {
				return [2]int{}, false
			}
		}
		return [2]int{from, n}, true
	}
	o, ok1 := parse(fields[1])
	n, ok2 := parse(fields[2])
	return o, n, ok1 && ok2
}
