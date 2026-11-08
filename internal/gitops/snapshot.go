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
	// Refs maps every ref to its object id, or to "ref: <target>" when it is
	// symbolic.
	Refs map[string]string
	// Files maps paths relative to the common git dir to their content: the
	// config files (nil when absent) and every entry under hooks/.
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

// Snapshot captures the refs, the config files and the hooks of the
// repository at repo, before a session.
func (g *Git) Snapshot(ctx context.Context, repo string) (Snapshot, error) {
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return Snapshot{}, err
	}
	refs, err := g.refs(ctx, repo)
	if err != nil {
		return Snapshot{}, err
	}
	files, err := readGitFiles(common)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Refs: refs, Files: files}, nil
}

// Check compares the repository with before, after a session. Every
// difference is restored and reported in an error wrapping ErrTampered; a
// failure to restore is joined to it. Changes to allowedRefs (full names, such
// as the node branch) are expected and left alone. Kluzo's own changes to the
// repository while the session ran must be among them, or not happen.
func (g *Git) Check(ctx context.Context, repo string, before Snapshot, allowedRefs ...string) error {
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return err
	}
	refs, err := g.refs(ctx, repo)
	if err != nil {
		return err
	}
	files, err := readGitFiles(common)
	if err != nil {
		return err
	}
	var changes []string
	var errs []error
	for _, name := range union(before.Refs, refs) {
		was, wasOK := before.Refs[name]
		is, isOK := refs[name]
		if was == is && wasOK == isOK || slices.Contains(allowedRefs, name) {
			continue
		}
		var args []string
		switch {
		case !wasOK:
			changes = append(changes, name+" created")
			args = []string{"update-ref", "--no-deref", "-d", name}
		case !isOK:
			changes = append(changes, name+" deleted")
		default:
			changes = append(changes, name+" moved")
		}
		if wasOK {
			if target, sym := strings.CutPrefix(was, "ref: "); sym {
				args = []string{"symbolic-ref", name, target}
			} else {
				args = []string{"update-ref", "--no-deref", name, was}
			}
		}
		if _, err := g.run(ctx, repo, args...); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", name, err))
		}
	}
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
	if len(changes) == 0 {
		return errors.Join(errs...)
	}
	slices.Sort(changes)
	return errors.Join(append([]error{fmt.Errorf("%w: %s", ErrTampered, strings.Join(changes, "; "))}, errs...)...)
}

func (g *Git) commonDir(ctx context.Context, repo string) (string, error) {
	out, err := g.run(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return strings.TrimSpace(out), err
}

// refs lists every ref with its object id, or its target when symbolic.
func (g *Git) refs(ctx context.Context, repo string) (map[string]string, error) {
	out, err := g.run(ctx, repo, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(symref)")
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

// readGitFiles reads the config files of the common dir, recording the ones
// that are absent, and every entry under hooks/ except directories.
func readGitFiles(common string) (map[string]*File, error) {
	names := []string{"config", "config.worktree"}
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
