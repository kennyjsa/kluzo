package gitops

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Files reads a node worktree for the evidence check of the core (ports.Files).
type Files struct{ root string }

var _ ports.Files = Files{}

// NewFiles returns a Files reading the worktree at root.
func NewFiles(root string) Files { return Files{root: root} }

// Lines implements ports.Files. Paths are opened through os.Root, so neither
// ".." nor a symlink can lead out of the worktree. A path that is not clean,
// or that cannot be stat-ed or opened (missing, outside, unreadable, not a
// directory on the way), is not there to cite: ok is false and err nil; the
// agent gets a problem to fix. The type is checked before opening and the
// open does not block, so a FIFO the agent made cannot stall the check.
func (f Files) Lines(path string) (int, bool, error) {
	p := filepath.FromSlash(path)
	if !filepath.IsLocal(p) || filepath.Clean(p) != p {
		return 0, false, nil
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = root.Close() }()
	if fi, err := root.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return 0, false, nil
	}
	file, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, false, nil
	}
	defer func() { _ = file.Close() }()
	if fi, err := file.Stat(); err != nil || !fi.Mode().IsRegular() {
		return 0, false, err // swapped between the two checks
	}
	var n int
	last := byte('\n') // an empty file has no unterminated last line
	buf := make([]byte, 32<<10)
	for {
		k, err := file.Read(buf)
		if k > 0 {
			n += bytes.Count(buf[:k], []byte{'\n'})
			last = buf[k-1]
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, false, err
		}
	}
	if last != '\n' {
		n++
	}
	return n, true, nil
}
