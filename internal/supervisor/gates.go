package supervisor

import (
	"context"
	"encoding/json"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/text"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/store"
)

// gateSummaryMax bounds the output of a failed gate kept for the next prompt;
// the full log stays at LogRef.
const gateSummaryMax = 4000

// gates runs the gates of a gate step through the Gates port, against the
// last commit of the implementer, and stores them as GatesDone with their
// gate.started and gate.finished events. A gate that could not run is an
// error: the outbox runs the effect again.
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
	in := core.GatesDone{Step: r.Step}
	var events []store.Event
	for _, g := range st.Gates {
		res, err := s.d.Gates.Run(ctx, ports.GateRun{Workspace: s.cfg.Workspace, Node: n.ID, Gate: g, SHA: r.Head, Workdir: s.worktree(n), Fast: st.Fast})
		if err != nil {
			return err
		}
		res.Gate, res.Summary = g, text.Clip(res.Summary, gateSummaryMax, text.Ellipsis)
		in.Results = append(in.Results, res)
		started, _ := json.Marshal(contracts.GateRun{Gate: g, SHA: r.Head}) // plain data
		finished, _ := json.Marshal(contracts.GateRun{Gate: g, SHA: r.Head, State: res.State, LogRef: res.LogRef})
		events = append(events,
			store.Event{Kind: contracts.EventGateStarted, Payload: started},
			store.Event{Kind: contracts.EventGateFinished, Payload: finished})
	}
	return s.decide(ctx, n, in, completed(f), events...)
}
