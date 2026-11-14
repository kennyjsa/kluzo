package ports

import (
	"context"
	"time"
)

// Gates runs the gates of a node (ADR 0020). The supervisor reads the
// definition of each gate from kluzo.yaml at the milestone tip of the
// dispatch (frozen) and passes it in GateRun; the implementation runs the
// command or reads the checks of the tracker. The result comes from the
// command or the CI, never from the agent.
type Gates interface {
	// Run runs one gate against the commit sha of the node worktree. A gate
	// that runs and fails is a GateResult, not an error: an error means the
	// gate could not run at all (retrying may help, unless ErrPermanent).
	Run(ctx context.Context, run GateRun) (GateResult, error)
}

// GateRun asks for one gate.
type GateRun struct {
	Workspace, Node string
	Gate            string // name in kluzo.yaml
	SHA             string // the commit under test
	Workdir         string // the node worktree
	Fast            bool   // a fast gate, before the review
	// The definition of the gate, frozen at the dispatch.
	Command        string        // run: the command, run with sh in Workdir
	Tracker        bool          // source: tracker: the checks of the CI for SHA
	RequiredChecks []string      // with Tracker
	Timeout        time.Duration // zero: the default of the implementation
	Report         string        // a JUnit XML report the command writes, relative to Workdir
	// Env is the whole environment of the command: env_allow of the gate,
	// the caches and COMPOSE_PROJECT_NAME; nothing else of Kluzo.
	Env []string
	// Secrets are the values of env_allow, masked in the log and the result.
	Secrets []string
}

// Gate run states (docs/contracts/data-model.md, gate_runs.state).
const (
	GatePassed = "passed"
	GateFailed = "failed"
	GateError  = "error"
)

// GateResult is how a gate run ended.
type GateResult struct {
	Gate     string
	State    string // GatePassed, GateFailed or GateError
	ExitCode int    // of the command; zero for a gate of the tracker
	Summary  string // the end of the output, redacted, for the next prompt
	LogRef   string // file in logs/gates or the URL of the CI
	// Failures are the structured test failures, when the runner gives them
	// (go test -json, or the JUnit report of the gate).
	Failures []TestFailure
	Report   string // the JUnit report kept with the log, when the gate has one
}

// TestFailure is a structured test failure of a gate.
type TestFailure struct {
	Test, File string
	Line       int
	Message    string
}
