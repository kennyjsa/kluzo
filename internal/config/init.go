package config

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed defaults
var defaultsFS embed.FS

const defaultsRoot = "defaults"

// projectNameToken is replaced, in the default kluzo.yaml, by the project name.
const projectNameToken = "__PROJECT_NAME__"

// ErrExists is returned by Init when it would overwrite an existing file.
var ErrExists = errors.New("already exists")

// Init writes the default configuration (kluzo.yaml, roles and flows) into
// dir, and returns the created files relative to dir. It never overwrites: if
// any target exists, nothing is written and the error wraps ErrExists.
//
// The default roles and gates are placeholders that pass validation but are
// marked TODO (docs/planning/questions.md, Q8).
func Init(dir string) ([]string, error) {
	name := projectName(dir)
	var files []string
	err := fs.WalkDir(defaultsFS, defaultsRoot, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, strings.TrimPrefix(p, defaultsRoot+"/"))
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded defaults: %w", err)
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return nil, fmt.Errorf("%s: %w; remove it or choose another directory", filepath.Join(dir, f), ErrExists)
		}
	}
	quoted, _ := json.Marshal(name) // a JSON string is a valid YAML scalar; Marshal of a string cannot fail
	for _, f := range files {
		data, err := defaultsFS.ReadFile(defaultsRoot + "/" + f)
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", f, err)
		}
		data = []byte(strings.ReplaceAll(string(data), projectNameToken, string(quoted)))
		target := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
	}
	return files, nil
}

// projectName guesses the project name from the directory that holds .kluzo/.
func projectName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "my-project"
	}
	name := filepath.Base(filepath.Dir(abs))
	if name == "." || name == string(filepath.Separator) {
		return "my-project"
	}
	return name
}
