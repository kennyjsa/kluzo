package app

import (
	"context"
	"errors"
	"os"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/platform/hostcred"
)

// Doctor checks that this machine may run the agents of the project
// (`kluzo doctor`). So far it checks the credentials of the host (ADR 0020,
// item 6); the CLIs, the tokens and the disk come with v0.1 item 10.
type Doctor struct {
	// Env is the environment of Kluzo (os.Environ when nil).
	Env []string
	// Helpers reads the git credential helpers of a HOME
	// (gitops.HostCredentialHelpers when nil).
	Helpers func(ctx context.Context, home, xdgConfig string) ([]string, error)
}

// DoctorInput selects the configuration directory.
type DoctorInput struct{ Dir string }

// DoctorOutput is what the doctor found. Invalid lists the problems of the
// configuration, with file, line and key (nothing else is checked then);
// Refused lines stop the sessions; Warnings do not.
type DoctorOutput struct {
	Invalid  []string
	Refused  []string
	Warnings []string
}

// Execute loads the configuration and checks the host.
func (d Doctor) Execute(ctx context.Context, in DoctorInput) (DoctorOutput, error) {
	p, err := config.Load(in.Dir)
	var invalid config.Errors
	if errors.As(err, &invalid) {
		var out DoctorOutput
		for _, e := range invalid {
			out.Invalid = append(out.Invalid, e.Error())
		}
		return out, nil
	}
	if err != nil {
		return DoctorOutput{}, err
	}
	env, helpers := d.Env, d.Helpers
	if env == nil {
		env = os.Environ()
	}
	if helpers == nil {
		helpers = gitops.New(gitops.Config{ParentEnv: env}).HostCredentialHelpers
	}
	found, err := hostcred.Check(ctx, env, helpers)
	if err != nil {
		return DoctorOutput{}, err
	}
	var out DoctorOutput
	for _, f := range found {
		if p.Config.Security.AllowHostCredentials {
			out.Warnings = append(out.Warnings, f+" (allowed by security.allow_host_credentials)")
		} else {
			out.Refused = append(out.Refused, f)
		}
	}
	return out, nil
}
