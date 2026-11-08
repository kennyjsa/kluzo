package fakes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Scenarios the fake runtime plays, chosen by spec.Env[ScenarioEnv].
const (
	ScenarioEnv   = "KLUZO_FAKE_SCENARIO"
	ScenarioHang  = "hang"  // runs until Cancel or the timeout
	ScenarioCrash = "crash" // dies without delivering a result
)

// Runtime is an in-memory ports.Runtime. A session finishes at once with an
// empty result, unless the scenario says otherwise.
type Runtime struct {
	mu       sync.Mutex
	sessions map[string]ports.SegmentHashes // harness ref -> segments at start
}

var _ ports.Runtime = (*Runtime)(nil)

// NewRuntime returns a fake runtime.
func NewRuntime() *Runtime { return &Runtime{sessions: map[string]ports.SegmentHashes{}} }

// Name implements ports.Runtime.
func (*Runtime) Name() string { return "fake" }

// Check implements ports.Runtime.
func (*Runtime) Check(context.Context) (ports.RuntimeInfo, error) {
	return ports.RuntimeInfo{Version: "0.0.0-fake", Authenticated: true, AuthSource: "fake"}, nil
}

// Start implements ports.Runtime.
func (r *Runtime) Start(ctx context.Context, spec ports.SessionSpec) (ports.Session, error) {
	ref := fmt.Sprintf("fake-%s-%d", spec.SessionID, spec.Counter)
	r.mu.Lock()
	r.sessions[ref] = spec.Segments
	r.mu.Unlock()
	return r.run(ctx, ref, spec), nil
}

// Resume implements ports.Runtime.
func (r *Runtime) Resume(ctx context.Context, ref string, spec ports.SessionSpec) (ports.Session, error) {
	r.mu.Lock()
	seg, ok := r.sessions[ref]
	r.mu.Unlock()
	switch {
	case !ok:
		return nil, fmt.Errorf("resume %s: %w", ref, ports.ErrNotFound)
	case seg != spec.Segments:
		return nil, fmt.Errorf("resume %s: %w", ref, ports.ErrResumeMismatch)
	}
	return r.run(ctx, ref, spec), nil
}

func (r *Runtime) run(ctx context.Context, ref string, spec ports.SessionSpec) ports.Session {
	s := &session{ref: ref, events: make(chan ports.Event, 16), done: make(chan struct{}), cancel: make(chan struct{})}
	go s.play(ctx, spec)
	return s
}

type session struct {
	ref    string
	events chan ports.Event
	done   chan struct{}
	cancel chan struct{}
	once   sync.Once

	mu     sync.Mutex
	result ports.Result
}

func sessionEvent(kind string, payload any) ports.Event {
	b, _ := json.Marshal(payload)
	return ports.Event{Kind: kind, Payload: b}
}

func (s *session) play(ctx context.Context, spec ports.SessionSpec) {
	defer close(s.done)
	defer close(s.events)
	s.events <- sessionEvent(contracts.EventSessionStarted, contracts.SessionStarted{Role: spec.Role.Name, Harness: "fake", Attempt: spec.Attempt, Round: spec.Round})

	var timeout <-chan time.Time
	if spec.Timeout > 0 {
		timeout = time.After(spec.Timeout)
	}
	res := ports.Result{State: ports.StateFinished, Raw: json.RawMessage(`{}`)}
	switch spec.Env[ScenarioEnv] {
	case ScenarioHang:
		select {
		case <-s.cancel:
			res = ports.Result{State: ports.StateCancelled}
		case <-timeout:
			res = ports.Result{State: ports.StateTimeout}
		case <-ctx.Done():
			res = ports.Result{State: ports.StateCancelled}
		}
	case ScenarioCrash:
		res = ports.Result{State: ports.StateFailed, Cause: ports.CauseRuntime, Detail: "the fake process died"}
	default:
		if spec.Accepted != nil {
			if raw, ok := spec.Accepted(); ok {
				res.Raw = raw
			}
		}
	}
	s.events <- sessionEvent(contracts.EventSessionFinished, contracts.SessionFinished{State: res.State, Cause: string(res.Cause)})
	s.mu.Lock()
	s.result = res
	s.mu.Unlock()
}

func (s *session) HarnessRef() string         { return s.ref }
func (s *session) Events() <-chan ports.Event { return s.events }
func (s *session) Cancel() error              { s.once.Do(func() { close(s.cancel) }); return nil }

func (s *session) Wait() (ports.Result, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, nil
}

func (s *session) Send(_ context.Context, _ string) error {
	select {
	case <-s.done:
		return errors.New("session ended")
	default:
		return nil
	}
}
