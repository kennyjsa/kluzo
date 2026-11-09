package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// configFile is the configuration the gates are frozen from.
const configFile = ".kluzo/kluzo.yaml"

// frozen reads kluzo.yaml at the commit an attempt started from: the
// milestone tip of the dispatch. The gates, their commands and the protected
// paths come from there, never from the worktree of the node (ADR 0020).
func (s *Supervisor) frozen(ctx context.Context, base string) (*config.Config, error) {
	if base == "" {
		return nil, fmt.Errorf("the attempt has no base commit to read the gates from")
	}
	data, err := s.d.Git.ReadAt(ctx, s.cfg.Repo, base, configFile)
	if err != nil {
		return nil, err
	}
	cfg, err := config.ParseConfig(configFile+" at "+base, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", err, ports.ErrPermanent)
	}
	return cfg, nil
}

// checkWork runs the check after an implementer round on the work of the
// attempt (base..head), with the rules of guard: the violations that need a
// human become the reasons of a proposal, the suppressions automatic findings.
func (s *Supervisor) checkWork(ctx context.Context, base, head string) ([]string, []core.Finding, error) {
	frozen, err := s.frozen(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	files, err := s.d.Git.Diff(ctx, s.cfg.Repo, base, head)
	if err != nil {
		return nil, nil, err
	}
	changes := make([]guard.Change, len(files))
	for i, f := range files {
		changes[i] = guard.Change{Path: f.Path, Deleted: f.Deleted}
		for _, l := range f.AddedText {
			changes[i].Added = append(changes[i].Added, guard.Line{N: l.N, Text: l.Text})
		}
		for _, l := range f.RemovedText {
			changes[i].Removed = append(changes[i].Removed, guard.Line{N: l.N, Text: l.Text})
		}
	}
	policy := guard.Policy{ProtectedPaths: frozen.ProtectedPaths}
	var proposal []string
	var auto []core.Finding
	for _, v := range policy.CheckChanges(changes) {
		if v.Proposal() {
			proposal = append(proposal, v.Reason)
			continue
		}
		auto = append(auto, core.Finding{Category: "style", File: v.Path, Line: v.Line, Message: v.Reason, Check: lintCheck(frozen)})
	}
	return proposal, auto, nil
}

// lintCheck is how to verify a suppression is gone: the fast gates, which
// hold the linter in the default configuration.
func lintCheck(cfg *config.Config) string {
	var names []string
	for _, g := range cfg.Gates {
		if g.Fast {
			names = append(names, g.Name)
		}
	}
	if len(names) == 0 {
		return "the suppression is gone from the diff"
	}
	return "the suppression is gone from the diff and the gates " + strings.Join(names, ", ") + " pass"
}

// hostCredentials lists the credentials of the host the sessions could use,
// unless security.allow_host_credentials is set (ADR 0020, item 6).
func (s *Supervisor) hostCredentials(ctx context.Context) ([]string, error) {
	if s.cfg.Project.Config.Security.AllowHostCredentials {
		return nil, nil
	}
	if s.d.HostCredentials == nil {
		return nil, fmt.Errorf("no check of the host credentials: %w", ports.ErrPermanent)
	}
	return s.d.HostCredentials(ctx)
}
