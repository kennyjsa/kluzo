// Package contracttest is the suite every Tracker, SCM and CI implementation
// must pass: the in-memory fakes and the real adapters run the same checks, so
// the fakes behave like the trackers (docs/development.md, "Contrato dos
// adaptadores").
package contracttest

import (
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Env is what a suite run needs: the implementations under test plus a few
// hooks for things only a human or another tool does on a real tracker.
type Env struct {
	Tracker ports.Tracker
	SCM     ports.SCM
	CI      ports.CI

	// Prefix makes every name the suite creates unique, so repeated runs against
	// the same sandbox repository do not collide.
	Prefix string
	// Milestone is the ref of an open milestone the suite may add issues to.
	Milestone string
	// BaseBranch is an existing branch to branch from and open PRs against.
	BaseBranch string

	// Commit pushes a new commit to the branch and returns its sha.
	Commit func(t *testing.T, branch string) string
	// Merge merges the PR, as the human does.
	Merge func(t *testing.T, prRef string)
	// Report publishes the state of a check on a commit.
	Report func(t *testing.T, sha string, run ports.CheckRun)
	// Settle waits until what was just written is visible to reads (the
	// tracker's event feed is eventually consistent). Nil means no wait.
	Settle func(t *testing.T)
}

func (e Env) settle(t *testing.T) {
	t.Helper()
	if e.Settle != nil {
		e.Settle(t)
	}
}
