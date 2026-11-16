// Package gates implements ports.Gates: it runs the command of a gate in the
// worktree of the node, or reads the checks of the tracker's CI, and turns
// what happened into a GateResult. The result comes from the command or the
// CI, never from the agent (ADR 0020). The definition of the gate arrives in
// the GateRun, frozen by the supervisor at the dispatch of the node.
package gates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/redact"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Defaults of Config.
const (
	DefaultTimeout = 30 * time.Minute
	DefaultPoll    = 30 * time.Second
)

// Bounds of what a run keeps on disk, in memory and in the result.
const (
	reportMax     = 16 << 20 // bytes of a JUnit report read
	defaultLogMax = 64 << 20 // bytes of a gate log
	lineMax       = 1 << 20  // bytes of a line before it is cut
	summaryHead   = 4_000    // bytes of the start of the output kept for the summary
	summaryEnd    = 12_000   // and of its end
	failuresMax   = 200      // structured failures kept
	messageMax    = 4_000    // characters of a failure message
	testOutMax    = 16 << 10 // bytes of output kept per test of go test -json
	outputGrace   = 5 * time.Second
)

// Config configures the runner.
type Config struct {
	// LogDir receives the log of each command gate (logs/gates of the
	// workspace) and the copy of its JUnit report.
	LogDir string
	CI     ports.CI // for the gates with source: tracker
	// Timeout is the limit of a gate without its own (DefaultTimeout).
	Timeout time.Duration
	// Poll is how often the checks of the CI are read (DefaultPoll); Wait
	// waits between two reads (time.After when nil).
	Poll time.Duration
	Wait func(ctx context.Context, d time.Duration) error
	// Secrets are values masked in every log, besides the token shapes.
	Secrets []string
	// LogMax bounds each log, in bytes (64 MiB).
	LogMax int
}

// Runner runs gates.
type Runner struct{ cfg Config }

var _ ports.Gates = (*Runner)(nil)

// New returns a runner.
func New(cfg Config) *Runner {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.LogMax <= 0 {
		cfg.LogMax = defaultLogMax
	}
	if cfg.Poll <= 0 {
		cfg.Poll = DefaultPoll
	}
	if cfg.Wait == nil {
		cfg.Wait = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	return &Runner{cfg: cfg}
}

// Run implements ports.Gates.
func (r *Runner) Run(ctx context.Context, run ports.GateRun) (ports.GateResult, error) {
	timeout := run.Timeout
	if timeout <= 0 {
		timeout = r.cfg.Timeout
	}
	switch {
	case run.Tracker:
		return r.checks(ctx, run, timeout)
	case run.Command != "":
		return r.command(ctx, run, timeout)
	default:
		return ports.GateResult{}, fmt.Errorf("gate %s has neither a command nor source tracker: %w", run.Gate, ports.ErrPermanent)
	}
}

// command runs the command with sh in the checkout of the gate, with only
// the environment of the run. Its output goes, redacted, to the log as it
// arrives; only a bounded part of it stays in memory.
func (r *Runner) command(ctx context.Context, run ports.GateRun, timeout time.Duration) (ports.GateResult, error) {
	if run.Report != "" {
		// A report from an earlier run, or one committed by the agent, must
		// not pass for the failures of this run.
		if err := removeReport(run.Workdir, run.Report); err != nil {
			return ports.GateResult{}, fmt.Errorf("gate %s: old report: %w", run.Gate, err)
		}
	}
	log, err := r.newLog(run)
	if err != nil {
		return ports.GateResult{}, err
	}
	f, err := os.OpenFile(log, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return ports.GateResult{}, fmt.Errorf("gate log: %w", err)
	}
	rd := redact.New(append(append([]string(nil), r.cfg.Secrets...), run.Secrets...)...)
	out := newSink(f, rd, r.cfg.LogMax)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	st, runErr := exec.RunTo(runCtx, exec.Spec{Bin: "sh", Args: []string{"-c", run.Command}, Dir: run.Workdir, Env: run.Env}, out, outputGrace)
	out.flush()
	if err := f.Close(); err != nil {
		return ports.GateResult{}, fmt.Errorf("gate log: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ports.GateResult{}, err // Kluzo is stopping: the gate runs again later
	}
	timedOut := errors.Is(runErr, context.DeadlineExceeded)
	if runErr != nil && !timedOut {
		return ports.GateResult{}, fmt.Errorf("gate %s did not run: %w", run.Gate, runErr)
	}
	res := ports.GateResult{Gate: run.Gate, State: ports.GatePassed, ExitCode: st.Code, LogRef: log, Failures: out.tests.failures()}
	switch {
	case timedOut:
		res.State = ports.GateError
		out.text.add(fmt.Sprintf("\n[kluzo] the gate timed out after %s\n", timeout))
	case st.Code != 0:
		res.State = ports.GateFailed
	}
	if run.Report != "" && res.State != ports.GatePassed {
		report, junit, err := readReport(run.Workdir, run.Report)
		switch {
		case errors.Is(err, os.ErrNotExist):
			out.text.add(fmt.Sprintf("\n[kluzo] the report %s was not written\n", run.Report))
		case err != nil:
			out.text.add(fmt.Sprintf("\n[kluzo] the report %s could not be read: %v\n", run.Report, err))
		default:
			res.Failures = append(res.Failures, junit...)
			res.Report = strings.TrimSuffix(log, ".log") + ".junit.xml"
			if err := os.WriteFile(res.Report, []byte(rd.String(string(report))), 0o600); err != nil {
				return ports.GateResult{}, fmt.Errorf("gate report: %w", err)
			}
		}
	}
	res.Failures = res.Failures[:min(len(res.Failures), failuresMax)]
	for i := range res.Failures {
		f := &res.Failures[i]
		f.Test, f.File, f.Message = rd.String(f.Test), rd.String(f.File), rd.String(clip(f.Message, messageMax))
	}
	res.Summary = out.text.String()
	return res, nil
}

// newLog returns a new log file for the run: <node>-<gate>-<sha>[-n].log.
func (r *Runner) newLog(run ports.GateRun) (string, error) {
	if err := os.MkdirAll(r.cfg.LogDir, 0o700); err != nil {
		return "", fmt.Errorf("gate log: %w", err)
	}
	base := fmt.Sprintf("%s-%s-%s", safeName(run.Node), safeName(run.Gate), safeName(run.SHA[:min(len(run.SHA), 12)]))
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name += fmt.Sprintf("-%d", n)
		}
		path := filepath.Join(r.cfg.LogDir, name+".log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("gate log: %w", err)
		}
		return path, f.Close()
	}
}

// safeName keeps the characters a file name may hold.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}

// removeReport deletes the report inside the checkout, if it is there.
func removeReport(workdir, report string) error {
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(report); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readReport reads the JUnit report from inside the worktree: a path that
// leaves it, through .. or a symlink, is refused by os.Root.
func readReport(workdir, report string) ([]byte, []ports.TestFailure, error) {
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(report)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, reportMax))
	if err != nil {
		return nil, nil, err
	}
	failures, err := junitFailures(data)
	return data, failures, err
}

// checks reads the required checks of the commit from the CI until none is
// pending, or the timeout passes.
func (r *Runner) checks(ctx context.Context, run ports.GateRun, timeout time.Duration) (ports.GateResult, error) {
	if r.cfg.CI == nil {
		return ports.GateResult{}, fmt.Errorf("gate %s reads the CI, and the tracker has none: %w", run.Gate, ports.ErrPermanent)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		checks, err := r.cfg.CI.Checks(runCtx, run.SHA, run.RequiredChecks)
		if err != nil {
			if ctx.Err() == nil && runCtx.Err() != nil {
				return ports.GateResult{Gate: run.Gate, State: ports.GateError, Summary: fmt.Sprintf("the checks could not be read in %s: %v", timeout, err)}, nil
			}
			return ports.GateResult{}, err
		}
		if res, done := r.judge(ctx, run, checks); done {
			return res, nil
		}
		if err := r.cfg.Wait(runCtx, r.cfg.Poll); err != nil {
			if ctx.Err() != nil {
				return ports.GateResult{}, ctx.Err()
			}
			res, _ := r.judge(ctx, run, checks)
			res.State = ports.GateError
			res.Summary = fmt.Sprintf("checks still pending after %s\n%s", timeout, res.Summary)
			return res, nil
		}
	}
}

// judge turns the checks into a result; done is false while one is pending.
func (r *Runner) judge(ctx context.Context, run ports.GateRun, checks []ports.CheckRun) (ports.GateResult, bool) {
	res := ports.GateResult{Gate: run.Gate, State: ports.GatePassed}
	done := true
	var lines []string
	for _, c := range checks {
		lines = append(lines, fmt.Sprintf("check %s: %s %s", c.Name, c.State, c.URL))
		switch c.State {
		case ports.CheckPending:
			done = false
		case ports.CheckPassed:
		default:
			res.State = ports.GateFailed
			if res.LogRef == "" {
				if url, err := r.cfg.CI.LogURL(ctx, c); err == nil {
					res.LogRef = url
				}
			}
		}
	}
	res.Summary = strings.Join(lines, "\n")
	return res, done
}
