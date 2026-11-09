// Package sessionenv builds the environment of agent sessions and gates: a
// synthetic one, never the environment of Kluzo (ADR 0024, item 3), with the
// download and build caches of ADR 0025.
package sessionenv

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Spec says what the environment holds besides the fixed part.
type Spec struct {
	Parent []string          // the environment of Kluzo, as KEY=VALUE; only PATH, LANG and Allow are read from it
	Home   string            // HOME of the workspace, not the real one
	Allow  []string          // env_allow: names copied from Parent
	Extra  map[string]string // set last: credential, caches, COMPOSE_PROJECT_NAME…
}

// fixed are the variables every session gets.
var fixed = map[string]string{
	"GIT_TERMINAL_PROMPT": "0",
	"DISABLE_AUTOUPDATER": "1",
}

// Build returns the KEY=VALUE environment, sorted. There is no SSH_AUTH_SOCK
// and no other variable of Parent unless allowed.
func Build(s Spec) []string {
	parent := map[string]string{}
	for _, kv := range s.Parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parent[k] = v
		}
	}
	env := map[string]string{}
	for _, k := range []string{"PATH", "LANG", "LC_ALL", "TERM"} {
		if v, ok := parent[k]; ok {
			env[k] = v
		}
	}
	for _, k := range s.Allow {
		if v, ok := parent[k]; ok {
			env[k] = v
		}
	}
	env["HOME"] = s.Home // after Allow: an allowed HOME never replaces the synthetic one
	for k, v := range fixed {
		env[k] = v
	}
	for k, v := range s.Extra {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Caches returns the variables that point the given ecosystems at the shared
// download cache of the project (downloads) and at the build cache of the
// workspace (builds). Only caches verified by hash are shared; build caches
// stay per workspace (ADR 0025, item 2).
func Caches(ecosystems []string, downloads, builds string) (map[string]string, error) {
	dl := func(p ...string) string { return filepath.Join(append([]string{downloads}, p...)...) }
	bd := func(p ...string) string { return filepath.Join(append([]string{builds}, p...)...) }
	env := map[string]string{}
	for _, e := range ecosystems {
		switch e {
		case "go":
			env["GOMODCACHE"], env["GOCACHE"] = dl("go", "mod"), bd("go-build")
		case "node":
			env["npm_config_cache"], env["npm_config_store_dir"] = dl("npm"), dl("pnpm-store")
		case "python":
			env["UV_CACHE_DIR"], env["PIP_CACHE_DIR"] = dl("uv"), dl("pip")
		case "rust":
			env["CARGO_HOME"], env["CARGO_TARGET_DIR"] = dl("cargo"), bd("cargo-target")
		case "java":
			env["GRADLE_USER_HOME"], env["MAVEN_OPTS"] = dl("gradle"), "-Dmaven.repo.local="+dl("maven")
		default:
			return nil, fmt.Errorf("unknown cache ecosystem %q", e)
		}
	}
	return env, nil
}

// Compose returns the COMPOSE_PROJECT_NAME of a node, which marks the Docker
// resources the node creates (ADR 0025, item 1).
func Compose(workspace, node string) map[string]string {
	return map[string]string{"COMPOSE_PROJECT_NAME": "kluzo-" + workspace + "-" + node}
}

// Merge returns the union of the maps; later maps win.
func Merge(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
