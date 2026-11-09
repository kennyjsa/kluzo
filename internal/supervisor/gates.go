package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/store"
)

// gates runs the gates of a gate step through the Gates port, against the
// last commit of the implementer, with the definitions frozen at the base of
// the attempt, and stores them as GatesDone with their gate.started and
// gate.finished events. A gate that could not run is an error: the outbox
// runs the effect again.
func (s *Supervisor) gates(ctx context.Context, f store.Effect, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	r, err := run(n)
	if err != nil {
		return err
	}
	if r.Step != p.Do.Step {
		return s.decide(ctx, n, core.GatesDone{Step: p.Do.Step}, completed(f)) // stale: dropped by the core
	}
	st := s.flow.Steps[r.Step]
	frozen, err := s.frozen(ctx, r.Base)
	if err != nil {
		return err
	}
	// The gates run in a clean checkout of the commit, never in the worktree
	// of the node: ignored files, index flags and caches there are the
	// agent's (ADR 0020).
	dir := filepath.Join(s.cfg.Dir, "gates", n.IssueRef)
	if err := s.d.Git.Checkout(ctx, s.cfg.Repo, dir, r.Head); err != nil {
		return err
	}
	if err := s.setup(ctx, n, dir, gatesPlace); err != nil {
		return fmt.Errorf("prepare the checkout of the gates: %w", err)
	}
	in := core.GatesDone{Step: r.Step}
	var events []store.Event
	for _, g := range st.Gates {
		i := slices.IndexFunc(frozen.Gates, func(d config.Gate) bool { return d.Name == g })
		if i < 0 {
			return fmt.Errorf("gate %s is not in %s at %s: %w", g, configFile, r.Base, ports.ErrPermanent)
		}
		def := frozen.Gates[i]
		started, _ := json.Marshal(contracts.GateRun{Gate: g, SHA: r.Head}) // plain data
		if err := s.append(ctx, store.Event{Kind: contracts.EventGateStarted, NodeID: n.ID, Payload: started}); err != nil {
			return err
		}
		run, err := s.gateRun(n, r, st, def, dir)
		if err != nil {
			return err
		}
		res, err := s.d.Gates.Run(ctx, run)
		if err != nil {
			return err
		}
		res.Gate = g
		in.Results = append(in.Results, res)
		finished, _ := json.Marshal(contracts.GateRun{Gate: g, SHA: r.Head, State: res.State, LogRef: res.LogRef})
		events = append(events, store.Event{Kind: contracts.EventGateFinished, Payload: finished})
	}
	// The finished gates are journaled with the decision they lead to.
	return s.decide(ctx, n, in, completed(f), events...)
}

// gateRun is the run of one gate in the checkout dir: its frozen definition,
// and an environment with only its env_allow, the caches and
// COMPOSE_PROJECT_NAME (ADR 0020, ADR 0025).
func (s *Supervisor) gateRun(n store.NodeRow, r core.Run, st core.FlowStep, def config.Gate, dir string) (ports.GateRun, error) {
	env, err := s.env(n, gatesPlace, def.EnvAllow)
	if err != nil {
		return ports.GateRun{}, err
	}
	var secrets []string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); slices.Contains(def.EnvAllow, k) {
			secrets = append(secrets, v)
		}
	}
	return ports.GateRun{
		Workspace: s.cfg.Workspace, Node: n.ID, Gate: def.Name, SHA: r.Head, Workdir: dir, Fast: st.Fast,
		Command: def.Run, Tracker: def.Source == "tracker", RequiredChecks: def.RequiredChecks, Timeout: def.Timeout, Report: def.Report,
		Env: env, Secrets: secrets,
	}, nil
}
