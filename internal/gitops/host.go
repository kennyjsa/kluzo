package gitops

import (
	"context"
	"errors"
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
)

// HostCredentialHelpers returns the credential helpers the git of the host
// user configures (system, global and XDG files), read with the HOME of that
// user: unlike every other command of this package, it reads the host
// configuration on purpose, for the check of ADR 0020, item 6. It only reads
// one key, with the includes of those files.
func (g *Git) HostCredentialHelpers(ctx context.Context, home string) ([]string, error) {
	env := []string{"HOME=" + home, "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range g.env {
		if k, _, _ := strings.Cut(kv, "="); k == "PATH" || k == "LANG" {
			env = append(env, kv)
		}
	}
	var helpers []string
	for _, scope := range []string{"--system", "--global"} {
		args := []string{"config", scope, "--get-all", "credential.helper"}
		o, err := exec.Run(ctx, exec.Spec{Bin: g.bin, Args: args, Dir: home, Env: env})
		switch {
		case err != nil:
			return nil, &Error{Args: args, Code: o.Status.Code, Stderr: o.Stderr, Err: err}
		case o.Status.Code == 1: // the key is not set
		case o.Status.Code != 0:
			return nil, &Error{Args: args, Code: o.Status.Code, Stderr: o.Stderr, Err: errors.New("git config failed")}
		}
		for l := range strings.Lines(o.Stdout) {
			if h := strings.TrimSpace(l); h != "" {
				helpers = append(helpers, h)
			}
		}
	}
	return helpers, nil
}
