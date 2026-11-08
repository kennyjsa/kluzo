package config

import (
	"errors"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// source is a parsed YAML (or JSON) document with an index from key paths to
// file lines. go-yaml reports positions only for syntax and type errors, so
// semantic validation looks its lines up here.
type source struct {
	file   string
	offset int            // lines before the document (frontmatter starts after `---`)
	lines  map[string]int // dotted path -> file line
}

// join appends a path segment to a dotted path.
func join(path, seg string) string {
	if path == "" {
		return seg
	}
	return path + "." + seg
}

func idx(path string, i int) string { return join(path, strconv.Itoa(i)) }

// decode parses data as strict YAML into v and indexes the line of every key.
// offset is the number of file lines that precede data.
func decode(file string, data []byte, offset int, v any) (*source, Errors) {
	s := &source{file: file, offset: offset, lines: map[string]int{}}
	doc, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, Errors{s.fromYAML(err)}
	}
	for _, d := range doc.Docs {
		s.index(d.Body, "")
	}
	if err := yaml.UnmarshalWithOptions(data, v, yaml.Strict()); err != nil {
		return nil, Errors{s.fromYAML(err)}
	}
	return s, nil
}

// fromYAML converts a go-yaml error, which carries a token position, into an Error.
func (s *source) fromYAML(err error) Error {
	var pe interface {
		GetToken() *token.Token
		GetMessage() string
	}
	if errors.As(err, &pe) && pe.GetToken() != nil {
		return Error{File: s.file, Line: pe.GetToken().Position.Line + s.offset, Msg: pe.GetMessage()}
	}
	return Error{File: s.file, Line: max(1, s.offset), Msg: err.Error()}
}

func (s *source) index(n ast.Node, path string) {
	switch v := n.(type) {
	case *ast.MappingNode:
		for _, mv := range v.Values {
			s.indexEntry(mv, path)
		}
	case *ast.MappingValueNode:
		s.indexEntry(v, path)
	case *ast.SequenceNode:
		for i, item := range v.Values {
			p := idx(path, i)
			s.lines[p] = item.GetToken().Position.Line + s.offset
			s.index(item, p)
		}
	}
}

func (s *source) indexEntry(mv *ast.MappingValueNode, path string) {
	k := mv.Key.GetToken()
	p := join(path, k.Value)
	s.lines[p] = k.Position.Line + s.offset
	s.index(mv.Value, p)
}

// has reports whether the key path appears in the document.
func (s *source) has(path string) bool {
	_, ok := s.lines[path]
	return ok
}

// line returns the line of the path, or of its closest existing ancestor, so
// that a missing key is reported at the object that should contain it.
func (s *source) line(path string) int {
	for p := path; ; {
		if l, ok := s.lines[p]; ok {
			return l
		}
		i := strings.LastIndex(p, ".")
		if i < 0 {
			return max(1, s.offset)
		}
		p = p[:i]
	}
}

// report collects semantic errors for one source file.
type report struct {
	src  *source
	errs *Errors
}

func (s *source) report(errs *Errors) report { return report{src: s, errs: errs} }

// add records an error at the given key path.
func (r report) add(path, msg string) { r.addAt(r.src.line(path), path, msg) }

// addAt records an error at an explicit line, for problems found outside the
// YAML structure (a role's markdown body).
func (r report) addAt(line int, path, msg string) {
	*r.errs = append(*r.errs, Error{File: r.src.file, Line: line, Key: displayKey(path), Msg: msg})
}
