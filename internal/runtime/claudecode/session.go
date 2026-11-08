package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/redact"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// eventBuffer is how many events wait for a slow reader before the stream
// stops being consumed. ports.Session asks the caller to keep reading Events;
// this only absorbs bursts.
const eventBuffer = 4096

type session struct {
	ref     string
	spec    ports.SessionSpec
	proc    *exec.Process
	grace   time.Duration
	started time.Time
	now     func() time.Time
	cleanup func()
	log     *os.File
	rd      *redact.Redactor
	t       *tracker

	events chan ports.Event
	done   chan struct{}

	inMu      sync.Mutex // guards stdin: its writes may block, so it is apart from mu
	stdinOpen bool

	mu        sync.Mutex // guards the fields below
	cancelled bool
	timedOut  bool
	abort     *abort
	result    ports.Result
}

var _ ports.Session = (*session)(nil)

// launch starts the process and the goroutine that reads its stream.
func (rt *Runtime) launch(ctx context.Context, spec ports.SessionSpec, ref string, ts toolSet, args []string, cleanup func()) (*session, error) {
	home, err := rt.home()
	if err != nil {
		cleanup()
		return nil, err
	}
	proc, err := exec.Start(exec.Spec{Bin: rt.cfg.Bin, Args: args, Dir: spec.Workdir, Env: rt.env(spec, home)})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("start claude: %w", err)
	}
	logf, err := rt.openLog(spec.SessionID)
	if err != nil {
		proc.Terminate(0)
		_, _ = io.Copy(io.Discard, proc.Stdout())
		proc.Wait()
		cleanup()
		return nil, err
	}
	rd := redact.New(append(append([]string{}, rt.cfg.Secrets...), rt.cfg.Credential.Value, spec.MCPToken)...)
	s := &session{
		ref: ref, spec: spec, proc: proc, grace: rt.cfg.KillGrace, now: rt.cfg.Clock.Now, started: rt.cfg.Clock.Now(),
		cleanup: cleanup, log: logf, rd: rd, stdinOpen: true,
		t:      newTracker(spec, rt.Name(), ts.expectedInit(), freeze(args, spec.System, true), rd.String),
		events: make(chan ports.Event, eventBuffer), done: make(chan struct{}),
	}
	go s.pump()
	if spec.Timeout > 0 {
		timer := time.AfterFunc(spec.Timeout, func() {
			s.mu.Lock()
			s.timedOut = true
			s.mu.Unlock()
			proc.Terminate(s.grace)
		})
		go func() { <-s.done; timer.Stop() }()
	}
	// The context only matters to stop the session when its owner goes away.
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Cancel()
		case <-s.done:
		}
	}()
	if err := s.Send(ctx, spec.Prompt); err != nil {
		_ = s.Cancel()
		return nil, fmt.Errorf("send the task: %w", err)
	}
	return s, nil
}

// pump reads the stream until the process closes it, normalizes it, then
// classifies the end of the session.
func (s *session) pump() {
	defer close(s.done)
	defer close(s.events)
	r := bufio.NewReaderSize(s.proc.Stdout(), 1<<20)
	for {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 {
			s.line(raw)
		}
		if err != nil {
			break
		}
	}
	st := s.proc.Wait()
	_ = s.log.Close()
	s.cleanup()

	s.mu.Lock()
	o := outcome{abort: s.abort, cancelled: s.cancelled, timedOut: s.timedOut, end: s.t.end, exit: st, stderr: s.rd.String(s.proc.Stderr()), t: s.t}
	s.mu.Unlock()
	if s.spec.Accepted != nil {
		if raw, ok := s.spec.Accepted(); ok {
			o.accepted = raw
		}
	}
	res := classify(o)
	s.emit(event(contracts.EventSessionFinished, contracts.SessionFinished{
		State: res.State, Cause: string(res.Cause), DurationMS: s.now().Sub(s.started).Milliseconds(), CostUSDMicros: res.CostUSD,
	}))
	s.mu.Lock()
	s.result = res
	s.mu.Unlock()
}

// line logs a raw line (redacted) and processes it.
func (s *session) line(raw []byte) {
	_, _ = s.log.WriteString(s.rd.String(string(raw)))
	if raw[len(raw)-1] != '\n' {
		_, _ = s.log.WriteString("\n")
	}
	evs, ab := s.t.handle(raw)
	for _, ev := range evs {
		s.emit(ev)
	}
	if s.t.end != nil {
		s.closeStdin() // one task per session: the result ends the conversation
	}
	if ab != nil {
		s.mu.Lock()
		first := s.abort == nil
		if first {
			s.abort = ab
		}
		s.mu.Unlock()
		if first {
			s.proc.Terminate(s.grace)
		}
	}
}

// emit delivers an event, never dropping one: a reader that stops reading
// stops the session, as documented on ports.Session.
func (s *session) emit(ev ports.Event) { s.events <- ev }

func (s *session) closeStdin() {
	s.inMu.Lock()
	defer s.inMu.Unlock()
	if s.stdinOpen {
		s.stdinOpen = false
		_ = s.proc.Stdin().Close()
	}
}

// HarnessRef implements ports.Session.
func (s *session) HarnessRef() string { return s.ref }

// Events implements ports.Session.
func (s *session) Events() <-chan ports.Event { return s.events }

// Wait implements ports.Session.
func (s *session) Wait() (ports.Result, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, nil
}

// errEnded is returned by Send once the session cannot take messages.
var errEnded = errors.New("the session has ended")

// Send implements ports.Session: it writes a user message in the stream-json
// input format.
func (s *session) Send(ctx context.Context, msg string) error {
	body, err := json.Marshal(map[string]any{
		"type": "user", "message": map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": msg}}},
	})
	if err != nil {
		return err
	}
	s.inMu.Lock()
	defer s.inMu.Unlock()
	if !s.stdinOpen {
		return errEnded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.proc.Stdin().Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write to the session: %w", err)
	}
	return nil
}

// Cancel implements ports.Session: SIGTERM to the process group, SIGKILL after
// the grace period.
func (s *session) Cancel() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	s.proc.Terminate(s.grace)
	return nil
}

func (rt *Runtime) openLog(sessionID string) (*os.File, error) {
	if err := os.MkdirAll(rt.cfg.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("create the session log dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(rt.cfg.LogDir, sessionID+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the session log: %w", err)
	}
	return f, nil
}
