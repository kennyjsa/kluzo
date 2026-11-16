// Package paths holds the one check of path containment: whether a path is a
// directory or inside it.
package paths

import (
	"path/filepath"
	"strings"
)

// Within returns name relative to dir, slash-separated, and whether it is dir
// or inside it. A relative name is taken as relative to dir. The check is
// lexical: links are not resolved.
func Within(dir, name string) (string, bool) {
	if !filepath.IsAbs(name) {
		name = filepath.Join(dir, name)
	}
	rel, err := filepath.Rel(dir, filepath.Clean(name))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// WithinAbs reports whether name is an absolute path that is dir or inside
// it; a relative or empty name is not.
func WithinAbs(dir, name string) bool {
	if !filepath.IsAbs(name) {
		return false
	}
	_, ok := Within(dir, name)
	return ok
}
