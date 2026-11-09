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
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
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
	extra, err := s.projectEnv(n)
	if err != nil {
		return err
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
		res, err := s.d.Gates.Run(ctx, s.gateRun(n, r, st, def, extra))
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

// gateRun is the run of one gate: its frozen definition, and an environment
// with only its env_allow, the caches and COMPOSE_PROJECT_NAME (ADR 0020,
// ADR 0025).
func (s *Supervisor) gateRun(n store.NodeRow, r core.Run, st core.FlowStep, def config.Gate, extra map[string]string) ports.GateRun {
	env := sessionenv.Build(sessionenv.Spec{Parent: s.cfg.ParentEnv, Home: filepath.Join(s.cfg.Dir, "home"), Allow: def.EnvAllow, Extra: extra})
	var secrets []string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); slices.Contains(def.EnvAllow, k) {
			secrets = append(secrets, v)
		}
	}
	return ports.GateRun{
		Workspace: s.cfg.Workspace, Node: n.ID, Gate: def.Name, SHA: r.Head, Workdir: s.worktree(n), Fast: st.Fast,
		Command: def.Run, Tracker: def.Source == "tracker", RequiredChecks: def.RequiredChecks, Timeout: def.Timeout, Report: def.Report,
		Env: env, Secrets: secrets,
	}
}
