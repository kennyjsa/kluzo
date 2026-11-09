package supervisor_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// crashDir tells the test binary to run as the child of the crash test.
const crashDir = "KLUZO_SUPERVISOR_CRASH_DIR"

// Lines the child prints, which the parent counts to choose where to kill it.
const (
	lineStep    = "KLUZO-CRASH step"
	lineSession = "KLUZO-CRASH session"
)

// Killing Kluzo anywhere and starting it again continues from the last role
// that finished, without repeating an effect (ADR 0013): the node ends in the
// merge queue with each transition journaled once and each comment posted once.
func TestCrashResumesWithoutDuplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the supervisor in child processes")
	}
	full := runChild(t, newCrashDir(t), 0)
	total := len(full)
	if total < 10 || count(full, lineSession) != 2 {
		t.Fatalf("a full run printed %d lines, %d sessions", total, count(full, lineSession))
	}
	// Every point: after each step and inside each session.
	for k := 1; k < total; k++ {
		t.Run(fmt.Sprintf("kill after line %d of %d", k, total), func(t *testing.T) {
			dir := newCrashDir(t)
			before := runChild(t, dir, k)
			if len(before) < k {
				t.Fatalf("the child printed %d lines before the kill at %d", len(before), k)
			}
			after := runChild(t, dir, 0)
			// Only a session the kill interrupted runs again.
			again := 0
			if before[len(before)-1] == lineSession {
				again = 1
			}
			if n := count(before, lineSession) + count(after, lineSession); n != 2+again {
				t.Errorf("sessions = %d, want %d", n, 2+again)
			}
			checkCrashOutcome(t, dir)
		})
	}
}

func count(lines []string, line string) int {
	n := 0
	for _, l := range lines {
		if l == line {
			n++
		}
	}
	return n
}

// newCrashDir prepares the repository and the journal of the child.
func newCrashDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	makeRepo(t, filepath.Join(dir, "repo"))
	db := openDB(t, filepath.Join(dir, "state.db"))
	seed(t, db, ids.NewGenerator(rand.Reader), "1", 6_000_000)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runChild runs the supervisor in a child process until it settles, or kills
// it with SIGKILL after it printed killAt lines (0: never). It returns the lines.
func runChild(t *testing.T, dir string, killAt int) []string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), crashDir+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var lines, all []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		all = append(all, sc.Text())
		if strings.HasPrefix(sc.Text(), "KLUZO-CRASH") {
			lines = append(lines, sc.Text())
			if killAt > 0 && len(lines) == killAt {
				_ = cmd.Process.Kill()
				break
			}
		}
	}
	err = cmd.Wait()
	if killAt == 0 && err != nil {
		t.Fatalf("child failed: %v\n%s", err, strings.Join(all, "\n"))
	}
	return lines
}

// TestCrashChild is the child process of the crash test.
func TestCrashChild(t *testing.T) {
	dir := os.Getenv(crashDir)
	if dir == "" {
		t.Skip("run by TestCrashResumesWithoutDuplicates")
	}
	db := openDB(t, filepath.Join(dir, "state.db"))
	clk := clock.NewManual(epoch)
	tracker := &fileTracker{Tracker: fakes.NewTracker(clk), path: filepath.Join(dir, "tracker.jsonl")}
	rt := &script{t: t, repo: filepath.Join(dir, "repo"), refs: map[string]bool{},
		byRole:   map[string]step{core.RoleImplementer: impl(implemented()), core.RoleReviewer: rev(reviewed(true, nil))},
		announce: func() { fmt.Println(lineSession) }}
	sup := build(t, dir, deps{db: db, clock: clk, ids: ids.NewGenerator(rand.Reader), tracker: tracker, runtime: rt, gates: fakes.NewGates(), notifier: &fakes.Notifier{}}, "1")
	for i := 0; i < 200; i++ {
		busy, err := sup.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(lineStep)
		if !busy {
			return
		}
	}
	t.Fatal("the supervisor did not settle")
}

// checkCrashOutcome checks the journal and the tracker after the final run.
func checkCrashOutcome(t *testing.T, dir string) {
	t.Helper()
	e := &env{t: t, db: openDB(t, filepath.Join(dir, "state.db"))}
	snap, err := e.db.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.node = snap.Nodes[0].ID
	if n, r := e.nodeRow(); n.State != core.NodeQueued || !r.Ready {
		t.Fatalf("node %s, run %+v", n.State, r)
	}
	if want := []string{"ready", "running", "in_review", "gating", "queued"}; !slices.Equal(e.moves(), want) {
		t.Errorf("moves = %v, want each once: %v", e.moves(), want)
	}
	// Every session that finished is paid, the ones a kill made run again too.
	if n, _ := e.nodeRow(); n.CostUSDMicros != int64(len(e.events(contracts.EventSessionFinished)))*10_000 || snap.Workspace.CostUSDMicros != n.CostUSDMicros {
		t.Errorf("node cost %d, workspace cost %d, %d sessions finished", n.CostUSDMicros, snap.Workspace.CostUSDMicros, len(e.events(contracts.EventSessionFinished)))
	}
	ft := &fileTracker{path: filepath.Join(dir, "tracker.jsonl")}
	recs := ft.read()
	var comments, labels []string
	for _, r := range recs {
		if r.Label != "" {
			labels = append(labels, r.Label)
		} else {
			comments = append(comments, r.Body)
		}
	}
	if len(comments) != 5 {
		t.Errorf("comments = %d, want 5 (one per transition):\n%s", len(comments), strings.Join(comments, "\n"))
	}
	if len(labels) == 0 || labels[len(labels)-1] != "orch/queued" {
		t.Errorf("labels = %v", labels)
	}
}

// fileTracker keeps the writes of the tracker in a file, so they outlive the
// child process, and deduplicates comments on the idempotency key as the real
// adapters do. Reads go to the in-memory fake, with issue 1.
type fileTracker struct {
	*fakes.Tracker
	path string
	mu   sync.Mutex
}

type trackerRecord struct {
	Label string `json:"label,omitempty"`
	Key   string `json:"key,omitempty"`
	Body  string `json:"body,omitempty"`
}

func (f *fileTracker) read() []trackerRecord {
	data, _ := os.ReadFile(f.path)
	var out []trackerRecord
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r trackerRecord
		if json.Unmarshal([]byte(ln), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (f *fileTracker) write(r trackerRecord) error {
	fh, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	if _, err := fh.Write(append(b, '\n')); err != nil {
		_ = fh.Close()
		return err
	}
	return fh.Close()
}

func (f *fileTracker) Issue(context.Context, string) (ports.Issue, error) {
	return ports.Issue{Ref: "1", Title: "Agenda API", Body: "Create the agenda endpoints.", State: ports.IssueOpen}, nil
}

func (f *fileTracker) SetStateLabel(_ context.Context, _ string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.write(trackerRecord{Label: label})
}

func (f *fileTracker) Comment(_ context.Context, _ string, body, key string) (ports.CommentRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.read() {
		if r.Key == key {
			return ports.CommentRef{ID: key}, nil
		}
	}
	return ports.CommentRef{ID: key}, f.write(trackerRecord{Key: key, Body: body})
}
