package fakes_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/contracttest"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// The fakes pass the same suite as the real adapters.
func TestFakesPassTheContract(t *testing.T) {
	tracker := fakes.NewTracker(clock.NewManual(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)))
	scm := fakes.NewSCM("main")
	ci := fakes.NewCI()
	env := contracttest.Env{
		Tracker: tracker, SCM: scm, CI: ci,
		Prefix:     "t" + strconv.Itoa(1),
		Milestone:  tracker.AddMilestone("v0.1", "first milestone"),
		BaseBranch: "main",
		Commit: func(t *testing.T, branch string) string {
			t.Helper()
			sha, err := scm.Commit(branch)
			if err != nil {
				t.Fatal(err)
			}
			return sha
		},
		Merge: func(t *testing.T, prRef string) {
			t.Helper()
			if err := scm.Merge(prRef); err != nil {
				t.Fatal(err)
			}
		},
		Report: func(_ *testing.T, sha string, run ports.CheckRun) { ci.Report(sha, run) },
	}
	t.Run("tracker", func(t *testing.T) { contracttest.RunTracker(t, env) })
	t.Run("scm", func(t *testing.T) { contracttest.RunSCM(t, env) })
	t.Run("ci", func(t *testing.T) { contracttest.RunCI(t, env) })
}
