package fakes

import (
	"context"
	"sync"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Gates is an in-memory ports.Gates: each gate passes unless a result was
// queued for it with Queue. Every run is recorded.
type Gates struct {
	mu     sync.Mutex
	queued map[string][]ports.GateResult
	runs   []ports.GateRun
}

var _ ports.Gates = (*Gates)(nil)

// NewGates returns gates that pass.
func NewGates() *Gates { return &Gates{queued: map[string][]ports.GateResult{}} }

// Queue sets the results of the next runs of gate, in order.
func (g *Gates) Queue(gate string, results ...ports.GateResult) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range results {
		r.Gate = gate
		g.queued[gate] = append(g.queued[gate], r)
	}
}

// Run implements ports.Gates.
func (g *Gates) Run(_ context.Context, run ports.GateRun) (ports.GateResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.runs = append(g.runs, run)
	if q := g.queued[run.Gate]; len(q) > 0 {
		g.queued[run.Gate] = q[1:]
		return q[0], nil
	}
	return ports.GateResult{Gate: run.Gate, State: ports.GatePassed}, nil
}

// Runs returns the runs so far.
func (g *Gates) Runs() []ports.GateRun {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ports.GateRun(nil), g.runs...)
}

// Notifier is an in-memory ports.Notifier that keeps what it was told.
type Notifier struct {
	mu   sync.Mutex
	sent []ports.Notification
}

var _ ports.Notifier = (*Notifier)(nil)

// Notify implements ports.Notifier.
func (n *Notifier) Notify(_ context.Context, msg ports.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, msg)
	return nil
}

// Sent returns the notifications so far.
func (n *Notifier) Sent() []ports.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ports.Notification(nil), n.sent...)
}
