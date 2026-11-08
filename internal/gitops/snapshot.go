package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrTampered means a session changed the refs, the git configuration or the
// hooks of the repository (ADR 0024, item 4). The change was undone; the node
// fails.
var ErrTampered = errors.New("repository tampered with")

// Snapshot is the state of the repository a session must not change. Its
// fields are plain data so the caller can keep it across a restart.
type Snapshot struct {
	// CommonDir is the absolute git dir of the main checkout. Check works on
	// it and never resolves it again: the .git file of a node worktree is the
	// agent's to rewrite, and could point a new lookup at a decoy.
	CommonDir string
	// Refs maps every ref to its object id, or to "ref: <target>" when it is
	// symbolic.
	Refs map[string]string
	// Files maps paths relative to the common git dir to their content: HEAD
	// of the main checkout, the config files (nil when absent) and every
	// entry under hooks/.
	Files map[string]*File
}

// File is a file of the common git dir as it was.
type File struct {
	Mode fs.FileMode // type and permissions
	Data []byte      // content, or the target of a symlink
}

func (f *File) equal(o *File) bool {
	return f == nil && o == nil || f != nil && o != nil && f.Mode == o.Mode && bytes.Equal(f.Data, o.Data)
}

// Snapshot captures the refs, HEAD, the config files and the hooks of the
// repository before a session. repo must be the main checkout: a linked
// worktree is refused, because its .git file is under the agent's control.
func (g *Git) Snapshot(ctx context.Context, repo string) (Snapshot, error) {
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return Snapshot{}, err
	}
	out, err := g.run(ctx, repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return Snapshot{}, err
	}
	if dir := strings.TrimSpace(out); dir != common {
		return Snapshot{}, fmt.Errorf("snapshot %s: a linked worktree (git dir %s); pass the main checkout", repo, dir)
	}
	refs, err := g.refs(ctx, common)
	if err != nil {
		return Snapshot{}, err
	}
	files, err := readGitFiles(common)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{CommonDir: common, Refs: refs, Files: files}, nil
}

// Check compares the repository of before with before, after a session.
// Every difference is restored and reported in an error wrapping ErrTampered;
// a failure to restore is joined to it. The files go first and without git,
// so a configuration the agent broke or made hostile is gone before git reads
// it; then the refs. Changes to allowedRefs (full names, such as the node
// branch) are expected and left alone.
//
// Kluzo's own writes to the repository while the session ran must be among
// allowedRefs, or not happen; and no git command of Kluzo may run in the
// repository between the end of a session and Check, since until then its
// configuration is the agent's (Q46).
func (g *Git) Check(ctx context.Context, before Snapshot, allowedRefs ...string) error {
	common := before.CommonDir
	if common == "" {
		return errors.New("check: the snapshot has no git dir")
	}
	files, err := readGitFiles(common)
	if err != nil {
		return fmt.Errorf("check: read %s: %w", common, err)
	}
	var changes []string
	var errs []error
	// Removals go first and deepest first, so a path an agent turned into a
	// symlink or a directory is gone before the original is written back.
	names := union(before.Files, files)
	slices.Reverse(names)
	for _, name := range names {
		if before.Files[name] == nil && files[name] != nil {
			changes = append(changes, describe(common, name, "added"))
			if err := os.RemoveAll(filepath.Join(common, name)); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", name, err))
			}
		}
	}
	slices.Reverse(names)
	for _, name := range names {
		was, is := before.Files[name], files[name]
		if was == nil || was.equal(is) {
			continue
		}
		verb := "changed"
		if is == nil {
			verb = "removed"
		}
		changes = append(changes, describe(common, name, verb))
		if err := writeGitFile(filepath.Join(common, name), was); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", name, err))
		}
	}

	refs, err := g.refs(ctx, common)
	if err != nil {
		changes = append(changes, "refs unreadable after restoring the files")
		errs = append(errs, err)
	}
	// Refs the session created go first, so one that took the place of a
	// deleted ref's directory (refs/heads/a/b for refs/heads/a) is gone
	// before the deleted one is written back.
	var restores [][]string
	for _, name := range union(before.Refs, refs) {
		was, wasOK := before.Refs[name]
		is, isOK := refs[name]
		if err != nil || was == is && wasOK == isOK || slices.Contains(allowedRefs, name) {
			continue
		}
		switch {
		case !wasOK:
			changes = append(changes, name+" created")
			if _, err := g.run(ctx, common, gitDir(common, "update-ref", "--no-deref", "-d", name)...); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", name, err))
			}
			continue
		case !isOK:
			changes = append(changes, name+" deleted")
		default:
			changes = append(changes, name+" moved")
		}
		if target, sym := strings.CutPrefix(was, "ref: "); sym {
			restores = append(restores, []string{name, "symbolic-ref", name, target})
		} else {
			restores = append(restores, []string{name, "update-ref", "--no-deref", name, was})
		}
	}
	for _, r := range restores {
		if _, err := g.run(ctx, common, gitDir(common, r[1:]...)...); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", r[0], err))
		}
	}
	if len(changes) == 0 {
		return errors.Join(errs...)
	}
	slices.Sort(changes)
	return errors.Join(append([]error{fmt.Errorf("%w: %s", ErrTampered, strings.Join(changes, "; "))}, errs...)...)
}

// gitDir prefixes a command with the git dir to work on, so no lookup from
// the working directory takes place.
func gitDir(common string, args ...string) []string {
	return append([]string{"--git-dir=" + common}, args...)
}

func (g *Git) commonDir(ctx context.Context, repo string) (string, error) {
	out, err := g.run(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return strings.TrimSpace(out), err
}

// refs lists every ref of the git dir common with its object id, or its
// target when symbolic.
func (g *Git) refs(ctx context.Context, common string) (map[string]string, error) {
	out, err := g.run(ctx, common, gitDir(common, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(symref)")...)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimSuffix(line, "\n"), "\x00")
		if len(f) != 3 {
			return nil, fmt.Errorf("for-each-ref: unexpected line %q", line)
		}
		refs[f[0]] = f[1]
		if f[2] != "" {
			refs[f[0]] = "ref: " + f[2]
		}
	}
	return refs, nil
}

// readGitFiles reads HEAD and the config files of the common dir, recording
// the ones that are absent, and every entry under hooks/ except directories.
// HEAD is the branch of the main checkout, which for-each-ref does not list.
func readGitFiles(common string) (map[string]*File, error) {
	names := []string{"HEAD", "config", "config.worktree"}
	// Each worktree may have its own; recording the absent ones catches an
	// agent that adds one.
	dirs, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, d := range dirs {
		if d.IsDir() {
			names = append(names, filepath.Join("worktrees", d.Name(), "config.worktree"))
		}
	}
	files := map[string]*File{}
	for _, name := range names {
		f, err := readGitFile(filepath.Join(common, name))
		if err != nil {
			return nil, err
		}
		files[name] = f
	}
	hooks := filepath.Join(common, "hooks")
	err = filepath.WalkDir(hooks, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == hooks && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := readGitFile(path)
		if err == nil {
			files[strings.TrimPrefix(path, common+string(filepath.Separator))] = f
		}
		return err
	})
	return files, err
}

// readGitFile reads a file without following a symlink; nil when absent.
func readGitFile(path string) (*File, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &File{Mode: fi.Mode()}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		f.Data = []byte(target)
		return f, err
	case fi.Mode().IsRegular():
		f.Data, err = os.ReadFile(path)
		return f, err
	}
	return f, nil // a directory or a special file where a file was: its mode tells it apart
}

// writeGitFile puts f back at path, replacing whatever is there.
func writeGitFile(path string, f *File) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if f.Mode&fs.ModeSymlink != 0 {
		return os.Symlink(string(f.Data), path)
	}
	if err := os.WriteFile(path, f.Data, f.Mode.Perm()); err != nil {
		return err
	}
	return os.Chmod(path, f.Mode.Perm()) // WriteFile applies the umask
}

// describe names a changed file the way a person reads it: ".git/config
// changed", "hook pre-commit added".
func describe(common, name, verb string) string {
	if hook, ok := strings.CutPrefix(name, "hooks"+string(filepath.Separator)); ok {
		return "hook " + hook + " " + verb
	}
	if name == "hooks" {
		return "hooks directory " + verb
	}
	return filepath.Join(filepath.Base(common), name) + " " + verb
}

// union returns the keys of both maps, sorted.
func union[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
