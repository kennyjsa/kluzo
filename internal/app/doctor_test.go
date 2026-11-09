package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// doctorConfig writes a valid .kluzo/ and returns its directory and kluzo.yaml.
func doctorConfig(t *testing.T) (dir, cfg string, raw []byte) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), ".kluzo")
	if _, err := config.Init(dir); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(dir, "kluzo.yaml")
	raw, _ = os.ReadFile(cfg)
	raw = []byte(strings.NewReplacer("__PROJECT_NAME__", "demo", "OWNER/REPO", "acme/demo").Replace(string(raw)))
	if err := os.WriteFile(cfg, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, cfg, raw
}

// greenDoctor is a doctor whose CLIs and tracker are fine.
func greenDoctor(env []string, helpers func(context.Context, string, string) ([]string, error)) Doctor {
	return Doctor{
		Host: Host{Env: env, Connect: func(context.Context, *config.Project, []string) (Integrations, error) {
			return Integrations{Tracker: fakes.NewTracker(clock.System{})}, nil
		}},
		Helpers: helpers,
		Git:     func(context.Context) (string, error) { return "2.43.0", nil },
		Runtime: func(context.Context, config.Harness, config.Runtime, []string) (string, error) { return "2.1.319", nil },
	}
}

func TestDoctorHostCredentials(t *testing.T) {
	dir, cfg, raw := doctorConfig(t)
	home := t.TempDir()
	env := []string{"HOME=" + home}
	none := func(context.Context, string, string) ([]string, error) { return nil, nil }
	keychain := func(context.Context, string, string) ([]string, error) { return []string{"osxkeychain"}, nil }

	out, err := greenDoctor(env, none).Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || !out.OK() || len(out.Warnings) != 0 {
		t.Errorf("clean host: %+v %v", out, err)
	}
	out, err = greenDoctor(env, keychain).Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || out.OK() || len(out.Refused) != 1 || !strings.Contains(out.Refused[0], "osxkeychain") {
		t.Errorf("helper: %+v %v", out, err)
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(string(raw), "allow_host_credentials: false", "allow_host_credentials: true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = greenDoctor(env, keychain).Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || !out.OK() || len(out.Warnings) != 1 {
		t.Errorf("allowed: %+v %v", out, err)
	}
	if _, err := greenDoctor([]string{}, none).Execute(context.Background(), DoctorInput{Dir: dir}); err == nil {
		t.Error("no HOME is not an error")
	}
	if err := os.WriteFile(cfg, []byte("version: 1\nnope: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := greenDoctor(env, none).Execute(context.Background(), DoctorInput{Dir: dir}); err != nil || len(out.Invalid) == 0 || out.OK() {
		t.Errorf("invalid configuration: %+v %v", out, err)
	}
}

// The CLIs and the token: an old git, a harness that fails its check and a
// tracker that refuses the token are problems of integration, each named.
func TestDoctorIntegrations(t *testing.T) {
	dir, _, _ := doctorConfig(t)
	env := []string{"HOME=" + t.TempDir()}
	none := func(context.Context, string, string) ([]string, error) { return nil, nil }
	d := greenDoctor(env, none)
	out, err := d.Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || !out.OK() {
		t.Fatalf("green: %+v %v", out, err)
	}
	for _, want := range []string{"git 2.43.0", "claude-code 2.1.319", "tracker token: acts as", "configuration:", "host credentials: none visible"} {
		if !strings.Contains(strings.Join(out.Passed, "\n"), want) {
			t.Errorf("passed %q misses %q", out.Passed, want)
		}
	}
	d.Git = func(context.Context) (string, error) {
		return "2.30.1", errors.New("git 2.30.1 is installed, kluzo needs 2.38 or newer")
	}
	d.Runtime = func(context.Context, config.Harness, config.Runtime, []string) (string, error) {
		return "", errors.New("claude: not found")
	}
	d.Host.Connect = func(context.Context, *config.Project, []string) (Integrations, error) {
		return Integrations{}, errors.New("KLUZO_GH_TOKEN is not set")
	}
	out, err = d.Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || out.OK() || len(out.Problems) != 3 {
		t.Fatalf("problems: %+v %v", out, err)
	}
	for i, want := range []string{"git: git 2.30.1", "claude-code: claude: not found", "tracker token: KLUZO_GH_TOKEN"} {
		if !strings.HasPrefix(out.Problems[i], want) {
			t.Errorf("problem %d = %q, want %q", i, out.Problems[i], want)
		}
	}
}

// Without a model credential the real check of Claude Code fails, and an
// unknown harness is refused in v0.1.
func TestCheckRuntime(t *testing.T) {
	if _, err := checkRuntime(context.Background(), config.OpenCode, config.Runtime{}, nil); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("opencode: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "claude")
	if _, err := checkRuntime(context.Background(), config.ClaudeCode, config.Runtime{Bin: missing}, []string{"PATH=/usr/bin"}); err == nil {
		t.Error("a missing claude passed")
	}
	if c := modelCredential([]string{"ANTHROPIC_API_KEY=k", "CLAUDE_CODE_OAUTH_TOKEN=o"}); c.Name != "CLAUDE_CODE_OAUTH_TOKEN" || c.Value != "o" {
		t.Errorf("credential = %+v", c)
	}
}
