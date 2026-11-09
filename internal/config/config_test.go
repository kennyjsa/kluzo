package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture copies testdata/valid into a temp dir, then lays testdata/cases/<name>
// over it: a file in the case replaces the valid one. It returns the directory.
func fixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "valid"), dir)
	if name != "" {
		copyTree(t, filepath.Join("testdata", "cases", name), dir)
	}
	return dir
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", from, err)
	}
}

// want is one expected error; File is relative to the config dir.
type want struct {
	File string
	Line int
	Key  string
	Msg  string // substring
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string // a directory in testdata/cases
		want []want
	}{
		// kluzo.yaml
		{"cfg-unknown-key", []want{{"kluzo.yaml", 5, "", `unknown field "lang"`}}},
		{"cfg-syntax", []want{{"kluzo.yaml", 10, "", "must be specified"}}},
		{"cfg-wrong-type", []want{{"kluzo.yaml", 14, "", "cannot unmarshal"}}},
		{"cfg-duplicate-key", []want{{"kluzo.yaml", 9, "", "already defined"}}},
		{"cfg-bad-resources", []want{
			{"kluzo.yaml", 79, "secret_paths[0]", "not a valid glob"},
			{"kluzo.yaml", 82, "worktree.include[0]", "clean path"},
			{"kluzo.yaml", 83, "worktree.timeout", "negative"},
			{"kluzo.yaml", 84, "worktree.env_allow[1]", `"NPM-TOKEN" is not an environment variable name`},
			{"kluzo.yaml", 84, "worktree.env_allow[2]", `duplicate variable "NPM_TOKEN"`},
			{"kluzo.yaml", 84, "worktree.env_allow[3]", `"HOME" is reserved to Kluzo`},
			{"kluzo.yaml", 84, "worktree.env_allow[4]", `"GIT_DIR" is reserved to Kluzo`},
			{"kluzo.yaml", 86, "caches[1]", `unknown ecosystem "cobol"`},
			{"kluzo.yaml", 89, "retention.logs", "days like 90d"},
			{"kluzo.yaml", 90, "retention.disk_warn", "size like 20GB"},
			{"kluzo.yaml", 91, "retention.docker_build_cache", "or off"},
		}},
		{"cfg-bad-values", []want{
			{"kluzo.yaml", 1, "version", "unsupported version 2"},
			{"kluzo.yaml", 3, "project.name", "required"},
			{"kluzo.yaml", 7, "tracker.kind", `unknown kind "svn"`},
			{"kluzo.yaml", 8, "tracker.repo", "owner/name"},
			{"kluzo.yaml", 10, "tracker.auth.kind", `unknown kind "password"`},
			{"kluzo.yaml", 47, "scheduler.max_parallel", "negative"},
			{"kluzo.yaml", 68, "defaults.model", "claude-code"},
			// roles without their own model inherit the invalid default
			{"roles/reviewer.md", 1, "model", "gpt-5"},
			{"roles/reporter.md", 1, "model", "gpt-5"},
		}},
		{"cfg-bad-token-env", []want{{"kluzo.yaml", 12, "tracker.auth.token_env", "not an environment variable name"}}},
		{"cfg-token-env-with-app", []want{{"kluzo.yaml", 14, "tracker.auth.token_env", "only valid when tracker.auth.kind is token"}}},
		{"cfg-bad-gates", []want{
			{"kluzo.yaml", 41, "gates[3]", "either run or source"},
			{"kluzo.yaml", 44, "gates[4]", "needs run"},
			{"kluzo.yaml", 47, "gates[6].name", `duplicate gate "dup"`},
			{"kluzo.yaml", 49, "gates[7].required_checks", "required when source is tracker"},
			{"kluzo.yaml", 53, "gates[8].required_checks", "only valid with source"},
			{"kluzo.yaml", 54, "gates[9].name", "required"},
			{"kluzo.yaml", 61, "milestone_gates[1].source", `unknown source "ci"`},
		}},
		{"cfg-bad-runtime", []want{
			{"kluzo.yaml", 60, "runtimes.claude-code.min_version", "not a version"},
			{"kluzo.yaml", 61, "runtimes.claude-code.env_allow[1]", "not an environment variable name"},
			{"kluzo.yaml", 62, "runtimes.codex", "unknown harness"},
		}},
		{"cfg-bad-code-map", []want{
			{"kluzo.yaml", 76, "code_map.adapter", `unknown adapter "ctags"`},
			{"kluzo.yaml", 77, "code_map.exclude[1]", "invalid glob"},
		}},
		// roles: frontmatter
		{"role-no-frontmatter", []want{{"roles/implementer.md", 1, "", "must start with"}}},
		{"role-unclosed-frontmatter", []want{{"roles/implementer.md", 1, "", "not closed"}}},
		{"role-unknown-key", []want{{"roles/implementer.md", 6, "", `unknown field "mdl"`}}},
		{"role-bad-yaml", []want{{"roles/implementer.md", 11, "", "must be specified"}}},
		{"role-missing-required", []want{
			{"roles/implementer.md", 1, "description", "required"},
			{"roles/implementer.md", 1, "tools", "required"},
			{"roles/implementer.md", 1, "mcp", "kluzo_result"},
			{"roles/implementer.md", 1, "output", "required"},
		}},
		{"role-bad-versions", []want{
			{"roles/implementer.md", 2, "version", "unsupported role version 2"},
			{"roles/implementer.md", 3, "protocol", "protocol 2, this binary implements protocol 1"},
		}},
		{"role-bad-name", []want{{"roles/Bad_Name.md", 1, "", "lowercase"}}},
		{"role-bad-enums", []want{
			{"roles/implementer.md", 9, "effort", `unknown effort "extreme"`},
			{"roles/implementer.md", 10, "tools[1]", `unknown tool "write"`},
			{"roles/implementer.md", 12, "mcp[1]", `unknown tool "kluzo_everything"`},
			{"roles/implementer.md", 13, "output", `unknown output schema "node-resultt"`},
		}},
		{"role-bad-limits", []want{
			{"roles/implementer.md", 14, "budget", "negative"},
			{"roles/implementer.md", 15, "max_turns", "negative"},
			{"roles/implementer.md", 16, "prompt_limits", "negative"},
		}},
		// roles: harness + model
		{"role-bad-pair-claude", []want{{"roles/implementer.md", 6, "model", "not valid for claude-code"}}},
		{"role-bad-pair-opencode", []want{{"roles/implementer.md", 6, "model", "provider/model"}}},
		{"role-unknown-harness", []want{{"roles/implementer.md", 5, "harness", `unknown harness "codex"`}}},
		{"role-no-harness-no-default", []want{
			{"roles/reviewer.md", 1, "harness", "no harness"},
			{"roles/reporter.md", 1, "harness", "no harness"},
		}},
		{"role-advisor-opencode", []want{
			{"roles/implementer.md", 7, "advisor", "only supported by claude-code"},
			{"roles/implementer.md", 8, "subagents", "only supported by claude-code"},
		}},
		{"role-advisor-weaker", []want{
			{"roles/implementer.md", 7, "advisor", "not supported by the claude-code adapter yet"},
			{"roles/implementer.md", 7, "advisor", "weaker than model"},
		}},
		{"role-bad-fallback", []want{
			{"roles/implementer.md", 8, "fallback[0]", "set harness, model or both"},
			{"roles/implementer.md", 9, "fallback[1].model", "not valid for claude-code"},
			{"roles/implementer.md", 10, "fallback[2].model", "provider/model"},
		}},
		// roles: rules of config.md and prompt.md
		{"role-no-kluzo-result", []want{{"roles/implementer.md", 6, "mcp", "must include kluzo_result"}}},
		{"role-body-markers", []want{
			{"roles/implementer.md", 21, "", `reserved marker "<<<UNTRUSTED"`},
			{"roles/implementer.md", 23, "", `reserved marker "UNTRUSTED:"`},
		}},
		{"role-body-tools", []want{
			{"roles/reviewer.md", 9, "", "kluzo_ask, which is not listed in mcp"},
			{"roles/reviewer.md", 10, "", "kluzo_propose_node, which is not listed in mcp"},
		}},
		// flows
		{"flow-reviewer-writes", []want{{"flows/node.yaml", 9, "steps[2].role", `must be read-only: remove "edit" and "bash"`}}},
		{"flow-bad-on-fail", []want{
			{"flows/node.yaml", 5, "steps[2].on_fail", `"q" is not an earlier role step`},
			{"flows/node.yaml", 6, "steps[3].on_fail", `"later" is not an earlier role step`},
		}},
		{"flow-unknown-role", []want{{"flows/node.yaml", 9, "steps[2].role", `role "tester" does not exist`}}},
		{"flow-unknown-gate", []want{
			{"flows/node.yaml", 14, "steps[3].gate[1]", `gate "lint" is not defined in gates`},
			{"flows/closing.yaml", 3, "steps[0].gate[0]", `gate "build" is not defined in milestone_gates`},
		}},
		{"flow-bad-structure", []want{
			{"flows/node.yaml", 1, "version", "unsupported flow version"},
			{"flows/node.yaml", 3, "steps[0].phase", `unknown phase "done"`},
			{"flows/node.yaml", 3, "steps[0].max_rounds", "at least 1"},
			{"flows/node.yaml", 4, "steps[1]", "mixes several kinds"},
		}},
		{"flow-step-kinds", []want{
			{"flows/node.yaml", 3, "steps[0]", "needs one of"},
			{"flows/node.yaml", 4, "steps[1].phase", "not valid on a gate step"},
			{"flows/node.yaml", 5, "steps[2].gate", "list at least one gate"},
			{"flows/node.yaml", 6, "steps[3].approval", `unknown value "code"`},
			{"flows/node.yaml", 7, "steps[4].scope", `unknown scope "repo"`},
			{"flows/node.yaml", 8, "steps[5].id", "required"},
			{"flows/node.yaml", 9, "steps[6].id", `duplicate step id "b"`},
		}},
		{"flow-bad-references", []want{
			{"flows/node.yaml", 3, "steps[0].on_reject", `"b" is not an earlier step`},
			{"flows/node.yaml", 5, "steps[2].on_fail", `"nope" is not an earlier role step`},
			{"flows/node.yaml", 6, "steps[3].on_reject", `"d" is not an earlier step`},
		}},
		{"flow-node-merge-queue", []want{
			{"flows/node.yaml", 4, "steps[1].merge_queue", "must be the last step"},
		}},
		{"flow-node-no-merge-queue", []want{{"flows/node.yaml", 2, "steps", "exactly one merge_queue step, found 0"}}},
		{"flow-node-two-merge-queues", []want{
			{"flows/node.yaml", 3, "steps[0].merge_queue", "must be the last step"},
			{"flows/node.yaml", 2, "steps", "found 2"},
		}},
		{"flow-loop", []want{
			{"flows/upstream.yaml", 4, "steps[0].loop.max_rounds", "at least 1"},
			{"flows/upstream.yaml", 5, "steps[0].loop.steps", "at least one step"},
			{"flows/upstream.yaml", 11, "steps[1].loop.steps[0].loop", "cannot be nested"},
		}},
		{"flow-unknown-file", []want{{"flows/review.yaml", 0, "", `unknown flow "review"`}}},
		{"flow-unknown-key", []want{{"flows/node.yaml", 4, "", `unknown field "roel"`}}},
		{"flow-bad-yaml", []want{{"flows/node.yaml", 2, "", "not found"}}},
		{"flow-empty", []want{{"flows/closing.yaml", 2, "steps", "at least one step"}}},
		// schemas
		{"schema-bad-json", []want{{"schemas/custom-report.json", 4, "", "invalid JSON"}}},
		{"schema-invalid", []want{{"schemas/custom-report.json", 7, "properties.summary.minLength", "invalid JSON Schema"}}},
		{"schema-wrong-type-keyword", []want{{"schemas/custom-report.json", 3, "type", "invalid JSON Schema"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fixture(t, tt.name)
			_, err := Load(dir)
			assertErrors(t, dir, err, tt.want)
		})
	}
}

func assertErrors(t *testing.T, dir string, err error, want []want) {
	t.Helper()
	var got Errors
	if err != nil && !errors.As(err, &got) {
		t.Fatalf("error is %T, want config.Errors: %v", err, err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d errors, want %d:\n%v", len(got), len(want), err)
	}
	for _, w := range want {
		found := false
		for _, e := range got {
			rel, _ := filepath.Rel(dir, e.File)
			if rel == w.File && e.Line == w.Line && e.Key == w.Key && strings.Contains(e.Msg, w.Msg) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing error %+v in:\n%v", w, err)
		}
	}
}

func TestTokenEnvDefaultsByTracker(t *testing.T) {
	tests := []struct {
		name    string
		auth    TrackerAuth
		tracker string
		want    string
	}{
		{"github default", TrackerAuth{Kind: "token"}, "github", "KLUZO_GH_TOKEN"},
		{"gitlab default", TrackerAuth{Kind: "token"}, "gitlab", "KLUZO_GL_TOKEN"},
		{"explicit", TrackerAuth{Kind: "token", TokenEnv: "MY_TOKEN"}, "github", "MY_TOKEN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.auth.TokenEnvName(tt.tracker); got != tt.want {
				t.Errorf("TokenEnvName = %q, want %q", got, tt.want)
			}
		})
	}
}

// The default implementer and reviewer are the normative texts of
// docs/contracts/roles/ (ADR 0023): the two must never drift apart.
func TestDefaultRolesAreTheContractTexts(t *testing.T) {
	for _, name := range []string{"implementer", "reviewer"} {
		want, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "roles", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := defaultsFS.ReadFile(defaultsRoot + "/roles/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("embedded roles/%s.md differs from docs/contracts/roles/%s.md", name, name)
		}
	}
}

// The variables of Kluzo's own credentials are reserved too.
func TestEnvAllowRefusesCredentials(t *testing.T) {
	var es Errors
	cfg := &Config{Tracker: Tracker{Kind: "github", Auth: TrackerAuth{Kind: "token", TokenEnv: "GITHUB_TOKEN"}}, Notify: &Notify{Kind: "ntfy", URLEnv: "NTFY_URL"}}
	validateEnvAllow((&source{}).report(&es), "gates[0].env_allow", []string{"DATABASE_URL", "GITHUB_TOKEN", "NTFY_URL"}, cfg.credentialEnv())
	if len(es) != 2 || !strings.Contains(es[0].Msg, "GITHUB_TOKEN") || !strings.Contains(es[1].Msg, "NTFY_URL") {
		t.Errorf("errors = %v", es)
	}
}
