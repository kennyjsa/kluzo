package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestLoadValid(t *testing.T) {
	p, err := Load(fixture(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := p.Config.Project, (ProjectInfo{Name: "agiclin", Language: "pt-BR"}); got != want {
		t.Errorf("project = %+v, want %+v", got, want)
	}
	if got := p.Config.Tracker.PollInterval; got != 30*time.Second {
		t.Errorf("poll_interval = %v, want 30s", got)
	}
	if got := p.Config.Gates[1]; got.Timeout != 15*time.Minute || !slices.Equal(got.EnvAllow, []string{"DATABASE_URL_TEST"}) {
		t.Errorf("gate test = %+v", got)
	}
	if got := p.Config.Runtimes[ClaudeCode].MinVersion; got != "2.1.186" {
		t.Errorf("min_version = %q, want 2.1.186", got)
	}
	if got := p.Config.Budgets.Milestone; got != (MilestoneBudget{USD: 60, AlertAt: 1.1}) {
		t.Errorf("milestone budget = %+v", got)
	}

	impl := p.Roles["implementer"]
	wantFallback := []Override{{Model: "opus"}, {Harness: OpenCode, Model: "openrouter/qwen3-coder"}}
	if diff := cmp.Diff(wantFallback, impl.Fallback); diff != "" {
		t.Errorf("fallback mismatch (-want +got):\n%s", diff)
	}
	if impl.Advisor != "opus" || impl.PromptLimits.Findings != 10 || impl.Budget.USD != 4 {
		t.Errorf("implementer = %+v", impl)
	}
	if !strings.HasPrefix(impl.Body, "You implement the node") {
		t.Errorf("body = %q", impl.Body)
	}
	if diff := cmp.Diff([]string{"closing", "node", "upstream"}, sortedKeys(p.Flows)); diff != "" {
		t.Errorf("flows mismatch (-want +got):\n%s", diff)
	}
	if steps := p.Flows["upstream"].Steps; steps[1].Loop == nil || steps[1].Loop.MaxRounds != 3 || len(steps[1].Loop.Steps) != 2 {
		t.Errorf("upstream loop = %+v", steps[1])
	}
	if got := p.Config.CodeMap; got.Adapter != "cartog" || !slices.Equal(got.Exclude, []string{"vendor/**", "web/dist/**"}) {
		t.Errorf("code_map = %+v", got)
	}
	if diff := cmp.Diff([]string{"custom-report"}, p.Schemas); diff != "" {
		t.Errorf("schemas mismatch (-want +got):\n%s", diff)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestLoadErrorsMore covers cases that need more than a fixture overlay.
func TestLoadErrorsMore(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "absent")
		_, err := Load(dir)
		assertErrors(t, dir, err, []want{{"kluzo.yaml", 0, "", "kluzo init"}})
	})
	t.Run("empty kluzo.yaml", func(t *testing.T) {
		dir := fixture(t, "cfg-empty")
		_, err := Load(dir)
		var got Errors
		if !errors.As(err, &got) || len(got) < 3 {
			t.Fatalf("want errors for the missing required keys, got %v", err)
		}
		for _, key := range []string{"version", "project.name", "tracker.kind", "tracker.repo"} {
			if !slices.ContainsFunc(got, func(e Error) bool { return e.Key == key && e.File == filepath.Join(dir, "kluzo.yaml") }) {
				t.Errorf("no error for %s in:\n%v", key, err)
			}
		}
	})
	t.Run("CRLF line endings", func(t *testing.T) {
		dir := fixture(t, "")
		file := filepath.Join(dir, "roles", "reviewer.md")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(string(data), "\n", "\r\n")
		if err := os.WriteFile(file, []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err != nil {
			t.Errorf("a role with CRLF endings must load: %v", err)
		}
	})
}

// LoadRoles must not look at the flows (kluzo roles check validates roles only).
func TestLoadRolesIgnoresFlows(t *testing.T) {
	dir := fixture(t, "flow-unknown-role")
	if _, err := Load(dir); err == nil {
		t.Fatal("Load must reject the flow")
	}
	p, err := LoadRoles(dir)
	if err != nil {
		t.Fatalf("LoadRoles: %v", err)
	}
	if len(p.Roles) != 3 || len(p.Flows) != 0 {
		t.Errorf("roles = %d, flows = %d, want 3 and 0", len(p.Roles), len(p.Flows))
	}
}

func TestLoadRolesReportsRoleErrors(t *testing.T) {
	dir := fixture(t, "role-body-tools")
	_, err := LoadRoles(dir)
	assertErrors(t, dir, err, []want{
		{"roles/reviewer.md", 9, "", "kluzo_ask, which is not listed in mcp"},
		{"roles/reviewer.md", 10, "", "kluzo_propose_node, which is not listed in mcp"},
	})
}

func TestErrorFormat(t *testing.T) {
	tests := []struct {
		err  Error
		want string
	}{
		{Error{File: "a.yaml", Line: 3, Key: "steps[1].role", Msg: "bad"}, "a.yaml:3: steps[1].role: bad"},
		{Error{File: "a.yaml", Line: 3, Msg: "bad"}, "a.yaml:3: bad"},
		{Error{File: "a.yaml", Msg: "missing"}, "a.yaml: missing"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
	if got := displayKey("steps.12.loop.steps.0.id"); got != "steps[12].loop.steps[0].id" {
		t.Errorf("displayKey = %q", got)
	}
}

func TestInit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my-app")
	dir := filepath.Join(root, ".kluzo")

	files, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !slices.Contains(files, "kluzo.yaml") || !slices.Contains(files, "roles/implementer.md") || !slices.Contains(files, "flows/node.yaml") {
		t.Errorf("files = %v", files)
	}

	// The generated configuration passes the loader.
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("the default configuration must be valid:\n%v", err)
	}
	if p.Config.Project.Name != "my-app" {
		t.Errorf("project name = %q, want my-app", p.Config.Project.Name)
	}
	for _, name := range []string{"implementer", "reviewer", "business", "po", "requirements", "architect", "planner", "critic", "triage", "reporter"} {
		if p.Roles[name] == nil {
			t.Errorf("missing default role %q", name)
		}
	}
	if !slices.Contains(p.Config.ProtectedPaths, "AGENTS.md") {
		t.Errorf("protected_paths = %v, want AGENTS.md in it (ADR 0022)", p.Config.ProtectedPaths)
	}
	if len(p.Flows) != 3 {
		t.Errorf("flows = %v, want upstream, node and closing", sortedKeys(p.Flows))
	}

	// Placeholders are marked TODO, pointing to investigation 2.
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(dir, f))
		switch {
		case strings.HasPrefix(f, "roles/") && !strings.Contains(string(data), "TODO(investigation 2)"):
			t.Errorf("%s has no TODO(investigation 2) marker", f)
		case f == "kluzo.yaml" && !strings.Contains(string(data), "TODO"):
			t.Errorf("%s has no TODO marker", f)
		}
	}

	// A second run refuses to overwrite and changes nothing.
	custom := filepath.Join(dir, "roles", "implementer.md")
	if err := os.WriteFile(custom, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); !errors.Is(err, ErrExists) {
		t.Errorf("second Init error = %v, want ErrExists", err)
	}
	if data, _ := os.ReadFile(custom); string(data) != "mine" {
		t.Error("Init overwrote an existing file")
	}
}

func TestInitProjectName(t *testing.T) {
	tests := []struct{ name, dir, want string }{
		{"quotes are escaped", filepath.Join(t.TempDir(), `we"ird: name`, ".kluzo"), `we"ird: name`},
		{"root falls back", string(filepath.Separator), "my-project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectName(tt.dir); got != tt.want {
				t.Errorf("projectName = %q, want %q", got, tt.want)
			}
		})
	}
	// A name with YAML metacharacters must still produce a loadable file.
	dir := filepath.Join(t.TempDir(), `we"ird: name`, ".kluzo")
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Config.Project.Name != `we"ird: name` {
		t.Errorf("name = %q", p.Config.Project.Name)
	}
}
