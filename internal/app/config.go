// Package app holds the use cases, one per operation, shared by the CLI and the
// web (docs/architecture/patterns.md, 6).
package app

import (
	"context"
	"errors"
	"sort"

	"github.com/kennyjsa/kluzo/internal/config"
)

// DefaultConfigDir is where the project configuration lives.
const DefaultConfigDir = ".kluzo"

// InitConfig creates the default .kluzo/ configuration (`kluzo init`).
type InitConfig struct{}

// InitConfigInput selects the directory to create.
type InitConfigInput struct{ Dir string }

// InitConfigOutput lists the files created, relative to Dir.
type InitConfigOutput struct{ Files []string }

// Execute writes the default configuration. It fails, writing nothing, if any
// of the files already exists.
func (InitConfig) Execute(_ context.Context, in InitConfigInput) (InitConfigOutput, error) {
	files, err := config.Init(in.Dir)
	if err != nil {
		return InitConfigOutput{}, err
	}
	return InitConfigOutput{Files: files}, nil
}

// CheckRoles validates the whole configuration (roles and flows) against the binary's protocol
// (`kluzo roles check`, docs/contracts/prompt.md).
type CheckRoles struct{}

// CheckRolesInput selects the configuration directory.
type CheckRolesInput struct{ Dir string }

// CheckRolesOutput reports the roles found and the problems with them. A
// non-empty Problems means the configuration is invalid; it is not an error of
// the use case itself.
type CheckRolesOutput struct {
	Roles    []string // valid role names, sorted
	Problems []string // one line per problem, with file, line and key
}

// Execute loads and validates the configuration.
func (CheckRoles) Execute(_ context.Context, in CheckRolesInput) (CheckRolesOutput, error) {
	p, err := config.Load(in.Dir)
	var out CheckRolesOutput
	var invalid config.Errors
	if errors.As(err, &invalid) {
		for _, e := range invalid {
			out.Problems = append(out.Problems, e.Error())
		}
	} else if err != nil {
		return out, err
	}
	if len(out.Problems) == 0 && p != nil {
		for name := range p.Roles {
			out.Roles = append(out.Roles, name)
		}
		sort.Strings(out.Roles)
	}
	return out, nil
}
