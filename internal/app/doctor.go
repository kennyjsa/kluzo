package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/platform/hostcred"
	"github.com/kennyjsa/kluzo/internal/runtime/claudecode"
)

// ErrDoctor means kluzo doctor is not green.
var ErrDoctor = errors.New("kluzo doctor is not green")

// Doctor checks that this machine may run the agents of the project
// (`kluzo doctor`, F29): the configuration (roles included), the CLIs at
// their minimum versions, the token of the tracker and the credentials of the
// host (ADR 0020), and, as warnings, what Kluzo left on the machine: orphan
// checkouts, Docker resources marked by Kluzo without an active workspace and
// a disk use above retention.disk_warn (ADR 0025).
type Doctor struct {
	Host Host
	// Helpers reads the git credential helpers of a HOME
	// (gitops.HostCredentialHelpers when nil).
	Helpers func(ctx context.Context, home, xdgConfig string) ([]string, error)
	// Git checks the git CLI and returns its version (gitops when nil).
	Git func(ctx context.Context) (string, error)
	// Runtime checks the CLI of a harness and returns its version (the
	// adapter's Check when nil).
	Runtime func(ctx context.Context, h config.Harness, rt config.Runtime, env []string) (string, error)
}

// DoctorInput selects the configuration directory.
type DoctorInput struct{ Dir string }

// DoctorOutput is what the doctor found. Invalid lists the problems of the
// configuration, with file, line and key (nothing else is checked then);
// Problems are the integrations that fail (CLIs, token); Refused are the
// credentials of the host that stop the sessions; Warnings do not stop
// anything; Passed says what was checked and is fine.
type DoctorOutput struct {
	Invalid  []string
	Problems []string
	Refused  []string
	Warnings []string
	Passed   []string
}

// OK reports whether the doctor is green.
func (o DoctorOutput) OK() bool { return len(o.Invalid)+len(o.Problems)+len(o.Refused) == 0 }

// Execute loads the configuration and checks the machine.
func (d Doctor) Execute(ctx context.Context, in DoctorInput) (DoctorOutput, error) {
	var out DoctorOutput
	p, err := config.Load(in.Dir)
	var invalid config.Errors
	if errors.As(err, &invalid) {
		for _, e := range invalid {
			out.Invalid = append(out.Invalid, e.Error())
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Passed = append(out.Passed, fmt.Sprintf("configuration: %d roles valid", len(p.Roles)))
	h := d.Host.fill()
	check := func(what string, version string, err error) {
		if err != nil {
			out.Problems = append(out.Problems, what+": "+err.Error())
			return
		}
		out.Passed = append(out.Passed, what+" "+version)
	}

	git := d.Git
	if git == nil {
		git = gitops.New(gitops.Config{ParentEnv: h.Env}).Version
	}
	v, err := git(ctx)
	check("git", v, err)

	runtime := d.Runtime
	if runtime == nil {
		runtime = checkRuntime
	}
	for _, harness := range harnesses(p) {
		v, err := runtime(ctx, harness, p.Config.Runtimes[harness], h.Env)
		check(string(harness), v, err)
	}

	integ, err := h.Connect(ctx, p, h.Env)
	if err == nil {
		var who string
		who, err = integ.Tracker.Whoami(ctx)
		check("tracker token: acts as", who, err)
	} else {
		check("tracker token", "", err)
	}

	helpers := d.Helpers
	if helpers == nil {
		helpers = gitops.New(gitops.Config{ParentEnv: h.Env}).HostCredentialHelpers
	}
	found, err := hostcred.Check(ctx, h.Env, helpers)
	if err != nil {
		return out, err
	}
	for _, f := range found {
		if p.Config.Security.AllowHostCredentials {
			out.Warnings = append(out.Warnings, f+" (allowed by security.allow_host_credentials)")
		} else {
			out.Refused = append(out.Refused, f)
		}
	}
	if len(found) == 0 {
		out.Passed = append(out.Passed, "host credentials: none visible")
	}
	s, err := surveyProject(ctx, h, p, in.Dir)
	if err != nil {
		return out, err
	}
	out.Warnings = append(out.Warnings, s.warnings()...)
	if !s.above() {
		out.Passed = append(out.Passed, "disk: "+HumanSize(s.Disk)+" in the workspaces and caches of the project")
	}
	return out, nil
}

// harnesses are the harnesses the roles of the project use, sorted.
func harnesses(p *config.Project) []config.Harness {
	var out []config.Harness
	for _, r := range p.Roles {
		h := r.Harness
		if h == "" {
			h = p.Config.Defaults.Harness
		}
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return out
}

// modelCredential is the credential of the model the sessions get, from the
// environment of Kluzo (ADR 0024).
func modelCredential(env []string) claudecode.Credential {
	for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if v := getenv(env, name); v != "" {
			return claudecode.Credential{Name: name, Value: v}
		}
	}
	return claudecode.Credential{}
}

// checkRuntime runs the Check of the adapter of a harness, with a throwaway
// synthetic HOME.
func checkRuntime(ctx context.Context, h config.Harness, rt config.Runtime, env []string) (string, error) {
	if h != config.ClaudeCode {
		return "", fmt.Errorf("harness %s is not supported in v0.1", h)
	}
	home, err := os.MkdirTemp("", "kluzo-doctor-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(home) }()
	info, err := claudecode.New(claudecode.Config{Bin: rt.Bin, MinVersion: rt.MinVersion, Home: home, ConfigDir: home, LogDir: home,
		ParentEnv: env, EnvAllow: rt.EnvAllow, Credential: modelCredential(env)}).Check(ctx)
	return info.Version, err
}
