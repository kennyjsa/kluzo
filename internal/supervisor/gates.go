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
// gate.finished events. With Freshness it runs the full gates again on the
// rebased branch of an integrating node. The node branch is pushed, with the
// credentials of the bot, before a gate that reads the CI of the tracker.
// Green gates that lead to the merge queue come with the check of the areas
// (ADR 0011). A gate that could not run is an error: the outbox runs the
// effect again.
func (s *Supervisor) gates(ctx context.Context, f store.Effect, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	r, err := run(n)
	if err != nil {
		return err
	}
	if r.Step != p.Do.Step || (p.Do.Freshness && n.State != core.NodeIntegrating) {
		return s.decide(ctx, n, core.GatesDone{Step: p.Do.Step}, completed(f)) // stale: dropped by the core
	}
	st := s.flow.Steps[r.Step]
	names := st.Gates
	if p.Do.Freshness {
		names = s.flow.FullGates()
	}
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
	in := core.GatesDone{Step: r.Step, Freshness: p.Do.Freshness}
	var events []store.Event
	pushed := false
	for _, g := range names {
		i := slices.IndexFunc(frozen.Gates, func(d config.Gate) bool { return d.Name == g })
		if i < 0 {
			return fmt.Errorf("gate %s is not in %s at %s: %w", g, configFile, r.Base, ports.ErrPermanent)
		}
		def := frozen.Gates[i]
		if def.Source == "tracker" && !pushed {
			if err := s.push(ctx, "+"+r.Head+":"+branchRef(s.branch(n))); err != nil {
				return err
			}
			pushed = true
		}
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
	green := !slices.ContainsFunc(in.Results, func(g ports.GateResult) bool { return g.State != ports.GatePassed })
	switch {
	case p.Do.Freshness && !green:
		if in.Changed, err = s.changedSince(ctx, r.Base, r.Tip); err != nil {
			return err
		}
	case !p.Do.Freshness && green && s.flow.Steps[r.Step+1].Kind == core.StepMergeQueue:
		areas, err := s.areaCheck(ctx, n, r)
		if err != nil {
			return err
		}
		events = append(events, areas...)
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
