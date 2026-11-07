package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

type goPackage struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func listPackages(t *testing.T) []goPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []goPackage
	for {
		var p goPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestDependencyRules checks production imports against the rules.
// Test files may import fakes and helpers, so only Imports is checked.
func TestDependencyRules(t *testing.T) {
	for _, p := range listPackages(t) {
		for _, r := range rules {
			if !r.applies(p.ImportPath) {
				continue
			}
			for _, imp := range p.Imports {
				if r.forbidden(p.ImportPath, imp) {
					t.Errorf("%s imports %s: violates rule %q", p.ImportPath, imp, r.name)
				}
			}
		}
	}
}

// forbiddenCoreCalls are functions the deterministic core must never call,
// even though their packages may be imported for types.
var forbiddenCoreCalls = map[string]map[string]bool{
	"time": {"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true, "Tick": true, "NewTimer": true, "NewTicker": true, "AfterFunc": true},
}

// TestCoreDoesNotReadTheClock inspects the core's source for calls such as
// time.Now(): time must arrive inside the inputs (ADR 0012).
func TestCoreDoesNotReadTheClock(t *testing.T) {
	fset := token.NewFileSet()
	for _, p := range listPackages(t) {
		if !under(internal("core"))(p.ImportPath) {
			continue
		}
		for _, name := range p.GoFiles {
			path := filepath.Join(p.Dir, name)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			aliases := importAliases(f)
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkg := aliases[id.Name]
				if forbiddenCoreCalls[pkg][sel.Sel.Name] {
					t.Errorf("%s: core must not call %s.%s", fset.Position(sel.Pos()), pkg, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

func importAliases(f *ast.File) map[string]string {
	m := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		m[name] = path
	}
	return m
}
