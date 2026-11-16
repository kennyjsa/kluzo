package ports

import "context"

// CI reads the checks of a commit.
type CI interface {
	// Checks returns the state of the required checks of the commit (all of
	// them when required is empty). A required check that has not reported yet
	// is "pending".
	Checks(ctx context.Context, sha string, required []string) ([]CheckRun, error)
	// LogURL returns where to read the log of the check.
	LogURL(ctx context.Context, check CheckRun) (string, error)
}

// Check states.
const (
	CheckPending = "pending"
	CheckPassed  = "passed"
	CheckFailed  = "failed"
	CheckError   = "error"
)

// CheckRun is one check of a commit.
type CheckRun struct {
	Name  string
	State string // CheckPending | CheckPassed | CheckFailed | CheckError
	URL   string
}

// SelectChecks applies the "required" rule of CI.Checks to the runs reported
// for a commit: with no required names it returns every run; otherwise one
// entry per required name, "pending" when nothing reported it yet. Adapters and
// fakes share it so they cannot disagree.
func SelectChecks(reported []CheckRun, required []string) []CheckRun {
	if len(required) == 0 {
		return append([]CheckRun(nil), reported...)
	}
	out := make([]CheckRun, 0, len(required))
	for _, name := range required {
		run := CheckRun{Name: name, State: CheckPending}
		for _, r := range reported {
			if r.Name == name {
				run = r
			}
		}
		out = append(out, run)
	}
	return out
}
