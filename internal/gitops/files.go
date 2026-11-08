package gitops

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Files reads a node worktree for the evidence check of the core (ports.Files).
type Files struct{ root string }

var _ ports.Files = Files{}

// NewFiles returns a Files reading the worktree at root.
func NewFiles(root string) Files { return Files{root: root} }

// Lines implements ports.Files. Paths are opened through os.Root, so neither
// ".." nor a symlink can lead out of the worktree. Opening errors other than
// a denied permission mean the file is not there to cite (missing, outside,
// not a directory on the way): ok is false and err nil.
func (f Files) Lines(path string) (int, bool, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return 0, false, nil
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.FromSlash(path))
	if errors.Is(err, fs.ErrPermission) {
		return 0, false, err
	}
	if err != nil {
		return 0, false, nil
	}
	defer func() { _ = file.Close() }()
	fi, err := file.Stat()
	if err != nil {
		return 0, false, err
	}
	if !fi.Mode().IsRegular() {
		return 0, false, nil
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
