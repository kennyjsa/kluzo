package ports

import "context"

// Gates runs the gates of a node (ADR 0020). The real implementation, which
// reads the definition from the milestone tip and runs the commands or reads
// the checks of the tracker, is item 8; the supervisor only asks for a run.
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
}

// Gate run states (docs/contracts/data-model.md, gate_runs.state).
const (
	GatePassed = "passed"
	GateFailed = "failed"
	GateError  = "error"
)

// GateResult is how a gate run ended.
type GateResult struct {
	Gate    string
	State   string // GatePassed, GateFailed or GateError
	Summary string // the end of the output, redacted, for the next prompt
	LogRef  string // file in logs/gates or the URL of the CI
}
