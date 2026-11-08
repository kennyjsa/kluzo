// Package config loads and validates the .kluzo/ directory: kluzo.yaml,
// roles/*.md, flows/*.yaml and schemas/*.json (docs/contracts/config.md).
//
// Everything that enters is validated here and converted into Go types, so the
// rest of the system works with valid data (docs/architecture/patterns.md, 9).
// Errors name the file, the line and the key.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Project is a fully loaded and validated .kluzo/ directory.
type Project struct {
	Config  Config
	Roles   map[string]*Role // by role name
	Flows   map[string]*Flow // by flow name: upstream, node, closing
	Schemas []string         // names of the project's own output schemas
}

// loader accumulates the errors of one Load.
type loader struct {
	dir  string
	errs Errors
}

// Load reads and validates the configuration in dir (the .kluzo directory).
// The returned error is an Errors value listing every problem found, or nil.
// The Project is only meaningful when the error is nil.
func Load(dir string) (*Project, error) {
	l := &loader{dir: dir}
	p := l.load(true)
	return p, l.errs.sorted().orNil()
}

// LoadRoles reads and validates kluzo.yaml (for the defaults) and the roles,
// without looking at the flows. It backs `kluzo roles check`.
func LoadRoles(dir string) (*Project, error) {
	l := &loader{dir: dir}
	p := l.load(false)
	return p, l.errs.sorted().orNil()
}

func (l *loader) load(withFlows bool) *Project {
	cfg, ok := l.loadConfig()
	if !ok {
		return nil // without a readable kluzo.yaml nothing else can be judged
	}
	p := &Project{Config: *cfg, Roles: map[string]*Role{}, Flows: map[string]*Flow{}}
	p.Schemas = l.loadSchemas()
	outputs := append(contracts.OutputSchemas(), p.Schemas...)
	invalid := l.loadRoles(p, outputs)
	if withFlows {
		l.loadFlows(p, invalid)
	}
	return p
}

// path returns the display path of a file inside the config directory.
func (l *loader) path(rel ...string) string { return filepath.Join(append([]string{l.dir}, rel...)...) }

// read returns the file content, or records an error and returns false.
func (l *loader) read(file string) (string, bool) {
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		return string(data), true
	case errors.Is(err, fs.ErrNotExist):
		l.errs = append(l.errs, Error{File: file, Msg: "file not found (run `kluzo init` to create the configuration)"})
	default:
		l.errs = append(l.errs, Error{File: file, Msg: err.Error()})
	}
	return "", false
}

// list returns the names (without extension) of the files with the given
// extension in a subdirectory of the config, sorted. A missing directory is empty.
func (l *loader) list(sub, ext string) []string {
	entries, err := os.ReadDir(l.path(sub))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			l.errs = append(l.errs, Error{File: l.path(sub), Msg: err.Error()})
		}
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			names = append(names, strings.TrimSuffix(e.Name(), ext))
		}
	}
	return names
}

func (l *loader) loadConfig() (*Config, bool) {
	file := l.path("kluzo.yaml")
	data, ok := l.read(file)
	if !ok {
		return nil, false
	}
	var cfg Config
	src, errs := decode(file, []byte(data), 0, &cfg)
	if errs != nil {
		l.errs = append(l.errs, errs...)
		return nil, false
	}
	cfg.validate(src.report(&l.errs))
	return &cfg, true
}

func (l *loader) loadSchemas() []string {
	names := l.list("schemas", ".json")
	for _, name := range names {
		file := l.path("schemas", name+".json")
		if data, ok := l.read(file); ok {
			l.errs = append(l.errs, checkSchema(file, []byte(data))...)
		}
	}
	return names
}

// loadRoles loads roles/*.md into p and returns the names of the roles whose
// file could not be parsed.
func (l *loader) loadRoles(p *Project, outputs []string) map[string]bool {
	invalid := map[string]bool{}
	for _, name := range l.list("roles", ".md") {
		file := l.path("roles", name+".md")
		data, ok := l.read(file)
		if !ok {
			invalid[name] = true
			continue
		}
		ro, src, errs := parseRole(file, name, data)
		if errs != nil {
			l.errs = append(l.errs, errs...)
			invalid[name] = true
			continue
		}
		ro.validate(src.report(&l.errs), p.Config.Defaults, outputs)
		p.Roles[name] = ro
	}
	return invalid
}

func (l *loader) loadFlows(p *Project, invalidRoles map[string]bool) {
	ctx := flowContext{
		roles:        p.Roles,
		invalidRoles: invalidRoles,
		gates:        gateNames(p.Config.Gates),
		milestone:    gateNames(p.Config.MilestoneGates),
	}
	for _, name := range l.list("flows", ".yaml") {
		file := l.path("flows", name+".yaml")
		if !slices.Contains(flowNames, name) {
			l.errs = append(l.errs, Error{File: file, Msg: fmt.Sprintf("unknown flow %q (want %s)", name, strings.Join(flowNames, ", "))})
			continue
		}
		data, ok := l.read(file)
		if !ok {
			continue
		}
		f := &Flow{Name: name}
		src, errs := decode(file, []byte(data), 0, f)
		if errs != nil {
			l.errs = append(l.errs, errs...)
			continue
		}
		f.validate(src.report(&l.errs), ctx)
		p.Flows[name] = f
	}
}

func gateNames(gates []Gate) map[string]bool {
	m := make(map[string]bool, len(gates))
	for _, g := range gates {
		m[g.Name] = true
	}
	return m
}
