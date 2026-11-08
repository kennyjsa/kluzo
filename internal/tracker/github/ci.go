package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v75/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Checks implements ports.CI with the check runs and the commit statuses of
// the commit. When both report a name, the check run wins.
func (a *Adapter) Checks(ctx context.Context, sha string, required []string) ([]ports.CheckRun, error) {
	statuses, err := a.statuses(ctx, sha)
	if err != nil {
		return nil, a.fail("get statuses of "+sha, err)
	}
	runs, err := a.checkRuns(ctx, sha)
	if err != nil {
		return nil, a.fail("get check runs of "+sha, err)
	}
	return ports.SelectChecks(append(statuses, runs...), required), nil
}

func (a *Adapter) checkRuns(ctx context.Context, sha string) ([]ports.CheckRun, error) {
	opts := &gh.ListCheckRunsOptions{Filter: gh.Ptr("latest"), ListOptions: gh.ListOptions{PerPage: pageSize}}
	var out []ports.CheckRun
	err := pages(func(page int) (int, error) {
		opts.Page = page
		res, resp, err := a.gh.Checks.ListCheckRunsForRef(ctx, a.owner, a.repo, sha, opts)
		if res != nil {
			for _, r := range res.CheckRuns {
				out = append(out, ports.CheckRun{Name: r.GetName(), State: checkRunState(r), URL: firstNonEmpty(r.GetDetailsURL(), r.GetHTMLURL())})
			}
		}
		return nextPage(resp), err
	})
	return out, err
}

func (a *Adapter) statuses(ctx context.Context, sha string) ([]ports.CheckRun, error) {
	opts := &gh.ListOptions{PerPage: pageSize}
	var out []ports.CheckRun
	err := pages(func(page int) (int, error) {
		opts.Page = page
		res, resp, err := a.gh.Repositories.GetCombinedStatus(ctx, a.owner, a.repo, sha, opts)
		if res != nil {
			for _, s := range res.Statuses {
				out = append(out, ports.CheckRun{Name: s.GetContext(), State: statusState(s.GetState()), URL: s.GetTargetURL()})
			}
		}
		return nextPage(resp), err
	})
	return out, err
}

// checkRunState maps a check run to a port state: anything not completed is
// pending; a completed run without a verdict we know is an error.
func checkRunState(r *gh.CheckRun) string {
	if r.GetStatus() != "completed" {
		return ports.CheckPending
	}
	switch r.GetConclusion() {
	case "success", "neutral", "skipped":
		return ports.CheckPassed
	case "failure", "timed_out", "cancelled", "action_required":
		return ports.CheckFailed
	}
	return ports.CheckError
}

func statusState(s string) string {
	switch s {
	case "success":
		return ports.CheckPassed
	case "failure":
		return ports.CheckFailed
	case "pending":
		return ports.CheckPending
	}
	return ports.CheckError
}

// LogURL implements ports.CI: the page where the check shows its output.
func (a *Adapter) LogURL(_ context.Context, check ports.CheckRun) (string, error) {
	if check.URL == "" {
		return "", a.fail("log url", fmt.Errorf("check %s has no log: %w", check.Name, ports.ErrNotFound))
	}
	return check.URL, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
