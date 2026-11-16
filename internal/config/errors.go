package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Error is one problem found in the configuration. It always names the file;
// Line and Key are set whenever they are known (docs/contracts/config.md).
type Error struct {
	File string // path of the offending file, as passed to Load
	Line int    // 1-based; 0 when the problem is not tied to a line
	Key  string // dotted path of the offending key, e.g. "steps[2].role"
	Msg  string
}

func (e Error) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d", e.Line)
	}
	b.WriteString(": ")
	if e.Key != "" {
		b.WriteString(e.Key + ": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// Errors is the list of problems found while loading a configuration.
type Errors []Error

func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	return strings.Join(lines, "\n")
}

// sorted orders the errors by file and line so the output is deterministic.
func (es Errors) sorted() Errors {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].File != es[j].File {
			return es[i].File < es[j].File
		}
		return es[i].Line < es[j].Line
	})
	return es
}

// orNil returns nil for an empty list, so it can be returned as an error.
func (es Errors) orNil() error {
	if len(es) == 0 {
		return nil
	}
	return es
}

var indexSegment = regexp.MustCompile(`\.(\d+)`)

// displayKey turns an internal path ("steps.2.role") into "steps[2].role".
func displayKey(path string) string { return indexSegment.ReplaceAllString(path, "[$1]") }
