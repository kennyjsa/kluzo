//go:build unix

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
)

// The crash test kills a real process (SIGKILL, no deferred cleanup) at chosen
// points of the cycle "transaction → drain the outbox", then reopens the
// database and finishes the work. Nothing may be lost or duplicated, and the
// external system (a fake tracker keyed by idempotency key) must see each
// effect exactly once, even when the effect ran twice (ADR 0013).

// Crash points.
const (
	crashInTx       = "in-tx"      // inside the transaction, before the commit
	crashAfterTx    = "after-tx"   // committed, outbox not drained yet
	crashAfterExec  = "after-exec" // effect applied externally, not marked done
	crashAfterMarks = "after-mark" // effect marked done
	envPoint        = "KLUZO_CRASH_POINT"
	envIndex        = "KLUZO_CRASH_INDEX"
	envDB           = "KLUZO_CRASH_DB"
	envExternal     = "KLUZO_CRASH_EXTERNAL"
)

// externalTracker is a fake external system. It deduplicates by idempotency key
// like a real adapter must; raw.log records every call, deduped.log the effects.
type externalTracker struct{ dir string }

func (e externalTracker) apply(key string) error {
	seen, err := e.lines("deduped.log")
	if err != nil {
		return err
	}
	if err := appendLine(filepath.Join(e.dir, "raw.log"), key); err != nil {
		return err
	}
	for _, s := range seen {
		if s == key {
			return nil // already applied: idempotent no-op
		}
	}
	return appendLine(filepath.Join(e.dir, "deduped.log"), key)
}

func (e externalTracker) lines(name string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(e.dir, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintln(f, line)
	return errors.Join(werr, f.Close())
}

// crashPlan says where the process should die; the zero value never crashes.
type crashPlan struct {
	point string
	index int
}

func (c crashPlan) at(point string, index int) {
	if c.point == point && c.index == index {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {} // wait for the signal
	}
}

// scriptStep is one transaction of the scripted milestone.
type scriptStep struct {
	events  []string // ids of the events it appends, for the checks
	run     func(x *Tx) error
	effects int
}

func label(id, state string) EffectSpec {
	return EffectSpec{ID: "ef_" + id, Kind: "tracker.set_label", Key: "label", Payload: payload(map[string]string{"label": state})}
}

// script is a workspace with one node walked to in_review. Every id is fixed,
// so running a step twice is detected by the journal (ErrDuplicateEvent).
func script() []scriptStep {
	tr := func(n int, trig core.NodeTrigger, lbl string) scriptStep {
		id := fmt.Sprintf("%04d", n)
		return scriptStep{events: []string{"ev_" + id}, effects: 1, run: func(x *Tx) error {
			_, err := x.TransitionNode("nd_1", core.NodeEvent{Trigger: trig}, limits, hdr(n), label(id, lbl))
			return err
		}}
	}
	return []scriptStep{
		{events: []string{"ev_0001", "ev_0002"}, effects: 1, run: func(x *Tx) error {
			if _, err := x.Append(Event{Header: hdr(1), Kind: contracts.EventWorkspaceCreated,
				Payload: payload(contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v0.1", BaseSHA: "abc"})}); err != nil {
				return err
			}
			_, err := x.TransitionWorkspace(core.TriggerCreate, hdr(2), EffectSpec{ID: "ef_0002", Kind: "tracker.comment", Key: "created", Payload: payload("created")})
			return err
		}},
		{events: []string{"ev_0003"}, run: func(x *Tx) error {
			_, err := x.Append(Event{Header: hdr(3), Kind: contracts.EventNodeAdded, NodeID: "nd_1",
				Payload: payload(contracts.NodeAdded{IssueRef: "1", Title: "T", Areas: []string{"a"}})})
			return err
		}},
		tr(4, core.TriggerDepsSatisfied, "orch/ready"),
		tr(5, core.TriggerDispatched, "orch/running"),
		tr(6, core.TriggerImplementerDone, "orch/review"),
	}
}

// driver runs the script and the outbox, dying where the plan says.
type driver struct {
	db    *DB
	ext   externalTracker
	crash crashPlan
	execs int // effects executed so far, for the exec crash points
}

// run executes every step not yet in the journal, draining the outbox after each.
func (dr *driver) run(ctx context.Context) error {
	for i, step := range script() {
		done, err := dr.committed(ctx, step.events[0])
		if err != nil {
			return err
		}
		if !done {
			err = dr.db.Tx(ctx, func(x *Tx) error {
				if err := step.run(x); err != nil {
					return err
				}
				dr.crash.at(crashInTx, i)
				return nil
			})
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
		}
		dr.crash.at(crashAfterTx, i)
		if err := dr.drain(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (dr *driver) committed(ctx context.Context, eventID string) (bool, error) {
	evs, err := dr.db.Events(ctx, 0, 0)
	if err != nil {
		return false, err
	}
	for _, e := range evs {
		if e.ID == eventID {
			return true, nil
		}
	}
	return false, nil
}

// drain runs the pending effects: external call first, then mark done.
func (dr *driver) drain(ctx context.Context) error {
	pending, err := dr.db.PendingEffects(ctx, epoch.Add(24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, f := range pending {
		if err := dr.ext.apply(f.IdemKey); err != nil {
			return err
		}
		dr.crash.at(crashAfterExec, dr.execs)
		if err := dr.db.Tx(ctx, func(x *Tx) error { return x.CompleteEffect(f.ID, epoch.Add(24*time.Hour)) }); err != nil {
			return err
		}
		dr.crash.at(crashAfterMarks, dr.execs)
		dr.execs++
	}
	return nil
}

// TestCrashChild is the process the crash test kills. It does nothing unless
// started by TestCrashRecovery.
func TestCrashChild(t *testing.T) {
	path := os.Getenv(envDB)
	if path == "" {
		t.Skip("helper process of TestCrashRecovery")
	}
	idx, err := strconv.Atoi(os.Getenv(envIndex))
	must(t, err)
	d, err := Open(context.Background(), path)
	must(t, err)
	dr := &driver{db: d, ext: externalTracker{dir: os.Getenv(envExternal)}, crash: crashPlan{os.Getenv(envPoint), idx}}
	must(t, dr.run(context.Background()))
	t.Fatal("the process was supposed to be killed before finishing")
}

func TestCrashRecovery(t *testing.T) {
	steps := script()
	var allEvents []string
	totalEffects := 0
	for _, s := range steps {
		allEvents = append(allEvents, s.events...)
		totalEffects += s.effects
	}
	// eventsAfterCrash is how many journal events must exist right after the
	// kill: an uncommitted step leaves nothing behind.
	committedThrough := func(step int, inclusive bool) int {
		n := 0
		for i, s := range steps {
			if i < step || (inclusive && i == step) {
				n += len(s.events)
			}
		}
		return n
	}

	type tc struct {
		point string
		index int
		want  int // journal events present right after the crash
	}
	var cases []tc
	for _, i := range []int{0, 1, 2, 4} {
		cases = append(cases, tc{crashInTx, i, committedThrough(i, false)}, tc{crashAfterTx, i, committedThrough(i, true)})
	}
	for _, i := range []int{0, 1, 3} { // effect ordinals
		cases = append(cases, tc{crashAfterExec, i, -1}, tc{crashAfterMarks, i, -1})
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%d", c.point, c.index), func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "state.db")
			ext := externalTracker{dir: dir}

			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$")
			cmd.Env = append(os.Environ(), envDB+"="+dbPath, envExternal+"="+dir, envPoint+"="+c.point, envIndex+"="+strconv.Itoa(c.index))
			out, err := cmd.CombinedOutput()
			exitErr, ok := err.(*exec.ExitError) //nolint:errorlint // exact type wanted
			if !ok || exitErr.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("child was not killed (err=%v):\n%s", err, out)
			}

			// 1. Right after the crash the journal holds exactly the committed steps.
			d := openAt(t, dbPath)
			if c.want >= 0 {
				if got := len(events(t, d)); got != c.want {
					t.Errorf("journal has %d events after the crash, want %d", got, c.want)
				}
			}

			// 2. Restart: the same cycle finishes the work.
			dr := &driver{db: d, ext: ext}
			must(t, dr.run(context.Background()))

			// Nothing lost, nothing duplicated in the journal.
			var ids []string
			for _, e := range events(t, d) {
				ids = append(ids, e.ID)
			}
			if diff := cmp.Diff(allEvents, ids); diff != "" {
				t.Errorf("journal (-want +got):\n%s", diff)
			}
			// Every effect completed, exactly once from the outside.
			pending, err := d.PendingEffects(context.Background(), epoch.Add(48*time.Hour), 100)
			must(t, err)
			if len(pending) != 0 {
				t.Errorf("%d effects still pending after recovery", len(pending))
			}
			deduped, err := ext.lines("deduped.log")
			must(t, err)
			raw, err := ext.lines("raw.log")
			must(t, err)
			seen := map[string]bool{}
			for _, k := range deduped {
				if seen[k] {
					t.Errorf("effect %s reached the external system twice", k)
				}
				seen[k] = true
			}
			if len(deduped) != totalEffects {
				t.Errorf("external system saw %d effects, want %d: %v", len(deduped), totalEffects, deduped)
			}
			if c.point == crashAfterExec && len(raw) != totalEffects+1 {
				t.Errorf("after-exec: %d raw calls, want %d (one repeated execution)", len(raw), totalEffects+1)
			}
			// The projection is the same one a rebuild produces.
			incremental := snapshot(t, d)
			if incremental.Nodes[0].State != core.NodeInReview {
				t.Errorf("node state = %s, want in_review", incremental.Nodes[0].State)
			}
			must(t, d.Rebuild(context.Background()))
			if diff := cmp.Diff(incremental, snapshot(t, d)); diff != "" {
				t.Errorf("rebuild differs after recovery:\n%s", diff)
			}
		})
	}
}
