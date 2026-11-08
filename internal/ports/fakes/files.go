package fakes

import (
	"path"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Files is an in-memory ports.Files: file contents by relative path.
type Files map[string]string

var _ ports.Files = Files(nil)

// Lines implements ports.Files.
func (f Files) Lines(p string) (int, bool, error) {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return 0, false, nil
	}
	content, ok := f[p]
	if !ok {
		return 0, false, nil
	}
	return CountLines(content), true, nil
}

// CountLines counts lines as ports.Files does.
func CountLines(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
