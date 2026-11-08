package sessionenv

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildIsSyntheticAndSorted(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "LANG=C.UTF-8", "HOME=/home/real", "SSH_AUTH_SOCK=/tmp/agent",
		"KLUZO_GH_TOKEN=ghp_secret", "ANTHROPIC_API_KEY=host-key", "DATABASE_URL_TEST=postgres://t", "OTHER=1",
	}
	env := Build(Spec{
		Parent: parent, Home: "/ws/home", Allow: []string{"DATABASE_URL_TEST", "MISSING"},
		Extra: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok", "GIT_TERMINAL_PROMPT": "1"},
	})
	want := []string{
		"CLAUDE_CODE_OAUTH_TOKEN=tok", "DATABASE_URL_TEST=postgres://t", "DISABLE_AUTOUPDATER=1",
		"GIT_TERMINAL_PROMPT=1", "HOME=/ws/home", "LANG=C.UTF-8", "PATH=/usr/bin",
	}
	if !slices.Equal(env, want) {
		t.Errorf("env = %v\nwant  %v", env, want)
	}
	for _, kv := range env {
		for _, leak := range []string{"SSH_AUTH_SOCK", "KLUZO_GH_TOKEN", "host-key", "/home/real"} {
			if strings.Contains(kv, leak) {
				t.Errorf("%q leaked into the session environment", kv)
			}
		}
	}
	if got := Build(Spec{Home: "/h"}); !slices.Contains(got, "GIT_TERMINAL_PROMPT=0") || !slices.Contains(got, "DISABLE_AUTOUPDATER=1") {
		t.Errorf("fixed variables missing: %v", got)
	}
}

func TestCaches(t *testing.T) {
	env, err := Caches([]string{"go", "node", "python", "rust", "java"}, "/cache/proj", "/ws/build")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"GOMODCACHE": "/cache/proj/go/mod", "GOCACHE": "/ws/build/go-build", // download shared, build per workspace
		"npm_config_cache": "/cache/proj/npm", "npm_config_store_dir": "/cache/proj/pnpm-store",
		"UV_CACHE_DIR": "/cache/proj/uv", "PIP_CACHE_DIR": "/cache/proj/pip",
		"CARGO_HOME": "/cache/proj/cargo", "CARGO_TARGET_DIR": "/ws/build/cargo-target",
		"GRADLE_USER_HOME": "/cache/proj/gradle", "MAVEN_OPTS": "-Dmaven.repo.local=/cache/proj/maven",
	} {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if _, err := Caches([]string{"cobol"}, "/a", "/b"); err == nil {
		t.Error("unknown ecosystem accepted")
	}
	if got, _ := Caches(nil, "/a", "/b"); len(got) != 0 {
		t.Errorf("no ecosystems gave %v", got)
	}
}

func TestComposeAndMerge(t *testing.T) {
	m := Merge(Compose("v2", "nd42"), map[string]string{"A": "1"}, map[string]string{"A": "2"})
	if m["COMPOSE_PROJECT_NAME"] != "kluzo-v2-nd42" || m["A"] != "2" {
		t.Errorf("merge = %v", m)
	}
}
