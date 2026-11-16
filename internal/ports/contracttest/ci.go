package contracttest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// RunCI runs the CI contract against env.CI, reporting checks on a commit it
// creates with env.Commit.
func RunCI(t *testing.T, env Env) {
	t.Helper()
	ctx := context.Background()
	branch := env.Prefix + "/ci"
	if _, err := env.SCM.EnsureBranch(ctx, branch, env.BaseBranch); err != nil {
		t.Fatal(err)
	}
	sha := env.Commit(t, branch)
	lint, build, test := env.Prefix+"-lint", env.Prefix+"-build", env.Prefix+"-test"

	check := func(t *testing.T, required []string) []ports.CheckRun {
		t.Helper()
		env.settle(t)
		got, err := env.CI.Checks(ctx, sha, required)
		if err != nil {
			t.Fatal(err)
		}
		return withoutURLs(got)
	}

	t.Run("a required check that has not reported is pending", func(t *testing.T) {
		want := []ports.CheckRun{{Name: lint, State: ports.CheckPending}}
		if diff := cmp.Diff(want, check(t, []string{lint})); diff != "" {
			t.Errorf("Checks (-want +got):\n%s", diff)
		}
	})

	t.Run("states and selection", func(t *testing.T) {
		env.Report(t, sha, ports.CheckRun{Name: lint, State: ports.CheckPassed, URL: "https://ci.example/lint"})
		env.Report(t, sha, ports.CheckRun{Name: build, State: ports.CheckFailed, URL: "https://ci.example/build"})
		env.Report(t, sha, ports.CheckRun{Name: test, State: ports.CheckPending})
		want := []ports.CheckRun{
			{Name: test, State: ports.CheckPending},
			{Name: lint, State: ports.CheckPassed},
			{Name: build, State: ports.CheckFailed},
		}
		if diff := cmp.Diff(want, check(t, []string{test, lint, build})); diff != "" {
			t.Errorf("required checks, in the order asked (-want +got):\n%s", diff)
		}
		if got := check(t, nil); len(got) < 3 {
			t.Errorf("without required names Checks = %+v, want every reported check", got)
		}
	})

	t.Run("the latest report of a check wins", func(t *testing.T) {
		env.Report(t, sha, ports.CheckRun{Name: build, State: ports.CheckPassed, URL: "https://ci.example/build"})
		want := []ports.CheckRun{{Name: build, State: ports.CheckPassed}}
		if diff := cmp.Diff(want, check(t, []string{build})); diff != "" {
			t.Errorf("Checks (-want +got):\n%s", diff)
		}
	})

	t.Run("log url", func(t *testing.T) {
		url, err := env.CI.LogURL(ctx, ports.CheckRun{Name: lint, State: ports.CheckPassed, URL: "https://ci.example/lint"})
		if err != nil || url != "https://ci.example/lint" {
			t.Errorf("LogURL = %q, %v", url, err)
		}
		if _, err := env.CI.LogURL(ctx, ports.CheckRun{Name: lint}); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("LogURL without a URL: err = %v, want ErrNotFound", err)
		}
	})
}

// withoutURLs drops the URLs, which differ between implementations.
func withoutURLs(runs []ports.CheckRun) []ports.CheckRun {
	out := make([]ports.CheckRun, len(runs))
	for i, r := range runs {
		out[i] = ports.CheckRun{Name: r.Name, State: r.State}
	}
	return out
}
