package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Scenario is what a session of the runtime under test must do.
type Scenario string

// Scenarios the suite asks for.
const (
	Finish Scenario = "finish" // delivers a result and ends
	Hang   Scenario = "hang"   // runs until cancelled or timed out
	Crash  Scenario = "crash"  // dies without delivering a result
)

// RuntimeEnv is what the Runtime suite needs.
type RuntimeEnv struct {
	Runtime ports.Runtime
	// Spec returns a spec whose session plays the scenario. Each call must
	// return a spec with a session id not used before in this run.
	Spec func(t *testing.T, s Scenario) ports.SessionSpec
}

// RunRuntime runs the Runtime contract.
func RunRuntime(t *testing.T, env RuntimeEnv) {
	t.Helper()
	ctx := context.Background()
	rt := env.Runtime

	t.Run("name and check", func(t *testing.T) {
		if rt.Name() == "" {
			t.Error("Name is empty")
		}
		info, err := rt.Check(ctx)
		if err != nil || info.Version == "" || !info.Authenticated {
			t.Errorf("Check = %+v, %v", info, err)
		}
	})

	t.Run("a finished session", func(t *testing.T) {
		s, kinds := start(t, rt, env.Spec(t, Finish))
		res := wait(t, s)
		if res.State != ports.StateFinished || !json.Valid(res.Raw) || len(res.Raw) == 0 {
			t.Errorf("result = %+v", res)
		}
		got := kinds()
		if len(got) < 2 || got[0] != contracts.EventSessionStarted || got[len(got)-1] != contracts.EventSessionFinished {
			t.Errorf("events = %v, want session.started first and session.finished last", got)
		}
		if s.HarnessRef() == "" {
			t.Error("HarnessRef is empty")
		}
		if err := s.Send(ctx, "late"); err == nil {
			t.Error("Send to an ended session succeeded")
		}
		if err := s.Cancel(); err != nil {
			t.Errorf("Cancel after the end: %v", err)
		}
	})

	t.Run("cancel stops a running session, twice is harmless", func(t *testing.T) {
		s, _ := start(t, rt, env.Spec(t, Hang))
		if err := s.Send(ctx, "are you there?"); err != nil {
			t.Errorf("Send to a running session: %v", err)
		}
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		if err := s.Cancel(); err != nil {
			t.Errorf("second Cancel: %v", err)
		}
		if res := wait(t, s); res.State != ports.StateCancelled {
			t.Errorf("state = %q, want cancelled", res.State)
		}
	})

	t.Run("a timeout ends the session", func(t *testing.T) {
		spec := env.Spec(t, Hang)
		spec.Timeout = 300 * time.Millisecond
		s, _ := start(t, rt, spec)
		if res := wait(t, s); res.State != ports.StateTimeout {
			t.Errorf("state = %q, want timeout", res.State)
		}
	})

	t.Run("a dead process is a failure with a cause", func(t *testing.T) {
		s, _ := start(t, rt, env.Spec(t, Crash))
		res := wait(t, s)
		if res.State != ports.StateFailed || res.Cause == "" {
			t.Errorf("result = %+v, want failed with a cause", res)
		}
	})

	t.Run("resume needs the same segments", func(t *testing.T) {
		spec := env.Spec(t, Finish)
		s, _ := start(t, rt, spec)
		wait(t, s)
		again, err := rt.Resume(ctx, s.HarnessRef(), spec)
		if err != nil {
			t.Fatalf("Resume with the same segments: %v", err)
		}
		drain(again)
		if res := wait(t, again); res.State != ports.StateFinished {
			t.Errorf("resumed state = %q", res.State)
		}
		changed := spec
		changed.Segments.Rules += "x"
		if _, err := rt.Resume(ctx, s.HarnessRef(), changed); !errors.Is(err, ports.ErrResumeMismatch) {
			t.Errorf("Resume with another rules segment: %v, want ErrResumeMismatch", err)
		}
	})
}

// start launches the session and collects its event kinds in the background.
func start(t *testing.T, rt ports.Runtime, spec ports.SessionSpec) (ports.Session, func() []string) {
	t.Helper()
	s, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Cancel() })
	var kinds []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range s.Events() {
			kinds = append(kinds, ev.Kind)
		}
	}()
	return s, func() []string {
		<-done
		return kinds
	}
}

func drain(s ports.Session) {
	go func() {
		for range s.Events() {
		}
	}()
}

func wait(t *testing.T, s ports.Session) ports.Result {
	t.Helper()
	type out struct {
		res ports.Result
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := s.Wait()
		ch <- out{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("Wait: %v", o.err)
		}
		return o.res
	case <-time.After(20 * time.Second):
		t.Fatal("Wait did not return")
		return ports.Result{}
	}
}
