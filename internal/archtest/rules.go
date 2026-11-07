package archtest

const module = "github.com/kennyjsa/kluzo"

// rule restricts what packages under a path prefix may import.
type rule struct {
	name string
	// applies reports whether the rule covers the package import path.
	applies func(pkg string) bool
	// forbidden reports whether importing imp from pkg breaks the rule.
	forbidden func(pkg, imp string) bool
}

// internal returns the full import path for an internal package.
func internal(p string) string { return module + "/internal/" + p }

func under(prefix string) func(string) bool {
	return func(pkg string) bool { return pkg == prefix || hasPrefix(pkg, prefix+"/") }
}

// inAny reports whether imp is one of the given internal packages or below them.
func inAny(imp string, pkgs ...string) bool {
	for _, p := range pkgs {
		if under(internal(p))(imp) {
			return true
		}
	}
	return false
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func isInternal(imp string) bool { return hasPrefix(imp, module+"/") }

// isStdlib reports whether imp is a standard library package (no dot in the
// first path element).
func isStdlib(imp string) bool {
	for i := 0; i < len(imp); i++ {
		switch imp[i] {
		case '.':
			return false
		case '/':
			return true
		}
	}
	return true
}

// coreAllowedStdlib lists the standard library packages the deterministic core
// may import. Anything doing I/O, randomness or reading the clock is absent.
var coreAllowedStdlib = map[string]bool{
	"context": true, "errors": true, "fmt": true, "sort": true, "slices": true,
	"maps": true, "strings": true, "strconv": true, "time": true, "encoding/json": true,
	"unicode": true, "unicode/utf8": true, "cmp": true, "iter": true, "path": true,
	"bytes": true, "math": true,
}

var rules = []rule{
	{
		name:    "core imports only contracts, ports and pure stdlib (ADR 0012)",
		applies: under(internal("core")),
		forbidden: func(_, imp string) bool {
			if isInternal(imp) {
				return !inAny(imp, "core", "contracts", "ports")
			}
			if isStdlib(imp) {
				return !coreAllowedStdlib[imp]
			}
			return true // no third-party code in the core
		},
	},
	{
		name: "adapters do not import core, app, supervisor or store",
		applies: func(pkg string) bool {
			return under(internal("tracker"))(pkg) || under(internal("runtime"))(pkg) ||
				under(internal("notify"))(pkg) || under(internal("gitops"))(pkg)
		},
		forbidden: func(_, imp string) bool {
			return under(internal("core"))(imp) || under(internal("app"))(imp) ||
				under(internal("supervisor"))(imp) || under(internal("store"))(imp)
		},
	},
	{
		name: "cmd and web talk only to app, contracts and platform",
		applies: func(pkg string) bool {
			return under(module+"/cmd")(pkg) || under(internal("web"))(pkg)
		},
		forbidden: func(pkg, imp string) bool {
			if !isInternal(imp) {
				return false
			}
			if under(internal("web"))(pkg) && under(internal("web"))(imp) {
				return false
			}
			return !inAny(imp, "app", "contracts", "platform")
		},
	},
	{
		name:    "only store imports the SQLite driver",
		applies: func(pkg string) bool { return !under(internal("store"))(pkg) },
		forbidden: func(_, imp string) bool {
			return imp == "modernc.org/sqlite" || hasPrefix(imp, "modernc.org/sqlite/")
		},
	},
	{
		name:      "only platform runs processes (os/exec); others use platform/exec",
		applies:   func(pkg string) bool { return !under(internal("platform"))(pkg) && !under(internal("archtest"))(pkg) },
		forbidden: func(_, imp string) bool { return imp == "os/exec" },
	},
	{
		name:    "platform knows nothing about the domain",
		applies: under(internal("platform")),
		forbidden: func(_, imp string) bool {
			return isInternal(imp) && !under(internal("platform"))(imp)
		},
	},
}
