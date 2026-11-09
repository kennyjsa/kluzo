package gates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

func newRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	logs := filepath.Join(t.TempDir(), "logs", "gates")
	return New(Config{LogDir: logs, Secrets: []string{"kluzo-secret-value"}}), logs
}

func env(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

func TestCommandGates(t *testing.T) {
	r, logs := newRunner(t)
	for _, tt := range []struct {
		name     string
		command  string
		env      []string
		timeout  time.Duration
		state    string
		code     int
		contains string
		missing  string
	}{
		{name: "passes", command: "echo ok", state: ports.GatePassed, contains: "ok"},
		{name: "fails with the exit code and standard error in the log", command: "echo out; echo boom >&2; exit 3", state: ports.GateFailed, code: 3, contains: "boom"},
		{name: "gets only its environment", command: `test -z "$KLUZO_LEAK" && test "$DATABASE_URL_TEST" = db && echo clean`, env: env("DATABASE_URL_TEST=db"), state: ports.GatePassed, contains: "clean"},
		{name: "times out as an error", command: "sleep 5", timeout: 200 * time.Millisecond, state: ports.GateError, contains: "timed out"},
		{name: "secrets are masked", command: "echo kluzo-secret-value; echo $TOKEN_TEST", env: env("TOKEN_TEST=gate-secret-1234"), state: ports.GatePassed, missing: "secret-value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KLUZO_LEAK", "1")
			e := tt.env
			if e == nil {
				e = env()
			}
			res, err := r.Run(context.Background(), ports.GateRun{Node: "nd_1", Gate: "g", SHA: "0123456789abcdef", Workdir: t.TempDir(),
				Command: tt.command, Env: e, Timeout: tt.timeout, Secrets: []string{"gate-secret-1234"}})
			if err != nil {
				t.Fatal(err)
			}
			if res.State != tt.state || res.ExitCode != tt.code && tt.state != ports.GateError || !strings.HasPrefix(res.LogRef, logs) {
				t.Errorf("result = %+v", res)
			}
			log, _ := os.ReadFile(res.LogRef)
			if tt.contains != "" && !strings.Contains(res.Summary, tt.contains) {
				t.Errorf("summary %q misses %q", res.Summary, tt.contains)
			}
			if tt.missing != "" && (strings.Contains(string(log), tt.missing) || strings.Contains(res.Summary, tt.missing) || strings.Contains(string(log), "gate-secret-1234")) {
				t.Errorf("secret leaked: %q", log)
			}
		})
	}
}

func TestEachRunHasItsOwnLog(t *testing.T) {
	r, _ := newRunner(t)
	run := ports.GateRun{Node: "nd_1", Gate: "g", SHA: "abc", Workdir: t.TempDir(), Command: "echo x", Env: env()}
	a, _ := r.Run(context.Background(), run)
	b, _ := r.Run(context.Background(), run)
	if a.LogRef == b.LogRef {
		t.Errorf("both runs wrote %s", a.LogRef)
	}
}

func TestGateThatCannotRunIsAnError(t *testing.T) {
	r, _ := newRunner(t)
	if _, err := r.Run(context.Background(), ports.GateRun{Gate: "g", Workdir: "/does/not/exist", Command: "true", Env: env()}); err == nil {
		t.Error("a missing worktree is not an error")
	}
	if _, err := r.Run(context.Background(), ports.GateRun{Gate: "g"}); !ports.IsPermanent(err) {
		t.Errorf("a gate without definition: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Run(ctx, ports.GateRun{Gate: "g", Workdir: t.TempDir(), Command: "true", Env: env()}); !errors.Is(err, context.Canceled) {
		t.Errorf("stopping Kluzo: %v", err)
	}
}

const goJSON = `{"Action":"run","Package":"ex/agenda","Test":"TestOverlap"}
{"Action":"output","Package":"ex/agenda","Test":"TestOverlap","Output":"=== RUN   TestOverlap\n"}
{"Action":"run","Package":"ex/agenda","Test":"TestOverlap/same_slot"}
{"Action":"output","Package":"ex/agenda","Test":"TestOverlap/same_slot","Output":"    conflict_test.go:41: got nil, want ErrConflict\n"}
{"Action":"output","Package":"ex/agenda","Test":"TestOverlap/same_slot","Output":"--- FAIL: TestOverlap/same_slot (0.00s)\n"}
{"Action":"fail","Package":"ex/agenda","Test":"TestOverlap/same_slot"}
{"Action":"fail","Package":"ex/agenda","Test":"TestOverlap"}
{"Action":"pass","Package":"ex/agenda","Test":"TestList"}
{"Action":"fail","Package":"ex/agenda"}
{"Action":"output","Package":"ex/broken","Output":"broken.go:3:1: syntax error\n"}
{"Action":"fail","Package":"ex/broken"}
`

func TestGoTestJSON(t *testing.T) {
	text, failures := goTestJSON(goJSON + "plain line\n")
	if !strings.Contains(text, "--- FAIL: TestOverlap/same_slot") || !strings.Contains(text, "plain line") || strings.Contains(text, `"Action"`) {
		t.Errorf("text = %q", text)
	}
	want := []ports.TestFailure{
		{Test: "TestOverlap/same_slot", File: "conflict_test.go", Line: 41, Message: "got nil, want ErrConflict"},
		{Test: "ex/broken", Message: "broken.go:3:1: syntax error"},
	}
	if len(failures) != len(want) || failures[0] != want[0] || failures[1] != want[1] {
		t.Errorf("failures = %+v", failures)
	}
	if _, f := goTestJSON("FAIL\tex/agenda\n"); f != nil {
		t.Errorf("plain output gave failures %+v", f)
	}
}

// A command that runs go test -json gives structured failures.
func TestCommandWithGoTestJSON(t *testing.T) {
	r, _ := newRunner(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte(goJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), ports.GateRun{Node: "n", Gate: "test", SHA: "s", Workdir: dir, Command: "cat out.json; exit 1", Env: env()})
	if err != nil || res.State != ports.GateFailed || len(res.Failures) != 2 || res.Failures[0].Line != 41 {
		t.Errorf("result = %+v, %v", res, err)
	}
}

const junit = `<?xml version="1.0"?>
<testsuites><testsuite name="agenda">
  <testcase classname="agenda.Conflict" name="overlap" file="src/agenda.test.ts" line="12"><failure message="expected conflict">at src/agenda.test.ts:12</failure></testcase>
  <testcase classname="agenda.List" name="lists"/>
  <testsuite name="nested"><testcase name="boot"><error message="db down"/></testcase></testsuite>
</testsuite></testsuites>`

func TestJUnitReport(t *testing.T) {
	r, _ := newRunner(t)
	dir := t.TempDir()
	cmd := "mkdir -p reports && cat > reports/junit.xml <<'X'\n" + junit + "\nX\nexit 1"
	res, err := r.Run(context.Background(), ports.GateRun{Node: "n", Gate: "e2e", SHA: "s", Workdir: dir, Command: cmd, Env: env(), Report: "reports/junit.xml"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.TestFailure{
		{Test: "agenda.Conflict.overlap", File: "src/agenda.test.ts", Line: 12, Message: "expected conflict\nat src/agenda.test.ts:12"},
		{Test: "boot", Message: "db down"},
	}
	if len(res.Failures) != 2 || res.Failures[0] != want[0] || res.Failures[1] != want[1] || res.Report == "" {
		t.Fatalf("result = %+v", res)
	}
	if b, err := os.ReadFile(res.Report); err != nil || !strings.Contains(string(b), "overlap") {
		t.Errorf("report copy: %v", err)
	}
	// A report that leaves the worktree through a symlink is not read.
	outside := filepath.Join(t.TempDir(), "junit.xml")
	_ = os.WriteFile(outside, []byte(junit), 0o644)
	dir = t.TempDir()
	_ = os.Symlink(outside, filepath.Join(dir, "junit.xml"))
	res, err = r.Run(context.Background(), ports.GateRun{Node: "n", Gate: "e2e", SHA: "s", Workdir: dir, Command: "exit 1", Env: env(), Report: "junit.xml"})
	if err != nil || len(res.Failures) != 0 || !strings.Contains(res.Summary, "could not be read") {
		t.Errorf("symlinked report: %+v %v", res, err)
	}
	// A missing report is said in the summary.
	res, _ = r.Run(context.Background(), ports.GateRun{Node: "n", Gate: "e2e", SHA: "s", Workdir: t.TempDir(), Command: "exit 1", Env: env(), Report: "junit.xml"})
	if !strings.Contains(res.Summary, "was not written") {
		t.Errorf("missing report: %q", res.Summary)
	}
}

func TestSummaryKeepsHeadAndEnd(t *testing.T) {
	s := "HEAD" + strings.Repeat("x", 30_000) + "END"
	got := summary(s)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "END") || len([]rune(got)) > summaryMax+100 {
		t.Errorf("summary of %d runes: %d runes", len(s), len([]rune(got)))
	}
}

func TestTrackerGates(t *testing.T) {
	ci := fakes.NewCI()
	waits := 0
	r := New(Config{CI: ci, Wait: func(context.Context, time.Duration) error {
		waits++
		if waits == 2 {
			ci.Report("sha2", ports.CheckRun{Name: "test", State: ports.CheckPassed})
		}
		return nil
	}})
	ci.Report("sha1", ports.CheckRun{Name: "build", State: ports.CheckPassed})
	ci.Report("sha1", ports.CheckRun{Name: "test", State: ports.CheckFailed, URL: "https://ci/1"})
	run := ports.GateRun{Gate: "ci", SHA: "sha1", Tracker: true, RequiredChecks: []string{"build", "test"}}
	res, err := r.Run(context.Background(), run)
	if err != nil || res.State != ports.GateFailed || res.LogRef != "https://ci/1" || !strings.Contains(res.Summary, "check test: failed") {
		t.Errorf("failed check: %+v %v", res, err)
	}
	// Pending until it reports.
	ci.Report("sha2", ports.CheckRun{Name: "build", State: ports.CheckPassed})
	run.SHA = "sha2"
	if res, err := r.Run(context.Background(), run); err != nil || res.State != ports.GatePassed || waits != 2 {
		t.Errorf("pending then passed: %+v %v after %d waits", res, err, waits)
	}
	// Still pending at the timeout: an error result.
	r = New(Config{CI: ci, Wait: func(context.Context, time.Duration) error { return context.DeadlineExceeded }})
	run.SHA = "sha3"
	if res, err := r.Run(context.Background(), run); err != nil || res.State != ports.GateError || !strings.Contains(res.Summary, "pending") {
		t.Errorf("pending at the timeout: %+v %v", res, err)
	}
	// No CI: permanent.
	if _, err := New(Config{}).Run(context.Background(), run); !ports.IsPermanent(err) {
		t.Errorf("no CI: %v", err)
	}
}
