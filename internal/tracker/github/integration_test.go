//go:build integration

package github

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v75/github"

	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/contracttest"
)

// The contract suite against a real sandbox repository. It runs only when
// KLUZO_IT_GITHUB_REPO ("owner/name") and KLUZO_IT_GITHUB_TOKEN are set, and it
// refuses to touch the Kluzo's own repository. Everything it creates carries a
// unique prefix; the milestone is closed and the branches deleted at the end.
//
//	KLUZO_IT_GITHUB_REPO=me/kluzo-sandbox KLUZO_IT_GITHUB_TOKEN=... make test-integration
func TestIntegrationContract(t *testing.T) {
	repo, token := os.Getenv("KLUZO_IT_GITHUB_REPO"), os.Getenv("KLUZO_IT_GITHUB_TOKEN")
	if repo == "" || token == "" {
		t.Skip("KLUZO_IT_GITHUB_REPO and KLUZO_IT_GITHUB_TOKEN are not set")
	}
	if strings.EqualFold(repo, "kennyjsa/kluzo") {
		t.Fatal("the integration tests must never run against kennyjsa/kluzo; use a sandbox repository")
	}
	ctx := context.Background()
	a, err := New(ctx, Options{Repo: repo, Auth: Auth{Kind: AuthToken, Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	raw := a.gh // go-github, for what only the harness needs (seeding and cleanup)
	owner, name := a.owner, a.repo

	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	prefix := "kit-" + hex.EncodeToString(suffix)

	info, _, err := raw.Repositories.Get(ctx, owner, name)
	if err != nil {
		t.Fatalf("read the sandbox repository: %v", err)
	}
	ms, _, err := raw.Issues.CreateMilestone(ctx, owner, name, &gh.Milestone{Title: gh.Ptr(prefix + " milestone")})
	if err != nil {
		t.Fatalf("create the milestone: %v", err)
	}
	t.Cleanup(func() { cleanup(raw, owner, name, prefix, ms.GetNumber()) })

	env := contracttest.Env{
		Tracker: a, SCM: a, CI: a, Prefix: prefix,
		Milestone:  strconv.Itoa(ms.GetNumber()),
		BaseBranch: info.GetDefaultBranch(),
		Commit: func(t *testing.T, branch string) string {
			t.Helper()
			res, _, err := raw.Repositories.CreateFile(ctx, owner, name, prefix+"/"+branch+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".txt",
				&gh.RepositoryContentFileOptions{Message: gh.Ptr("test: commit on " + branch), Content: []byte("kluzo integration test\n"), Branch: &branch})
			if err != nil {
				t.Fatalf("commit on %s: %v", branch, err)
			}
			return res.Commit.GetSHA()
		},
		Merge: func(t *testing.T, prRef string) {
			t.Helper()
			n, _ := strconv.Atoi(prRef)
			if _, _, err := raw.PullRequests.Merge(ctx, owner, name, n, "test merge", nil); err != nil {
				t.Fatalf("merge PR %s: %v", prRef, err)
			}
		},
		// A token cannot create check runs (only an App can), so checks are
		// published as commit statuses.
		Report: func(t *testing.T, sha string, run ports.CheckRun) {
			t.Helper()
			state := map[string]string{ports.CheckPassed: "success", ports.CheckFailed: "failure", ports.CheckPending: "pending", ports.CheckError: "error"}[run.State]
			st := &gh.RepoStatus{State: &state, Context: &run.Name}
			if run.URL != "" {
				st.TargetURL = &run.URL
			}
			if _, _, err := raw.Repositories.CreateStatus(ctx, owner, name, sha, st); err != nil {
				t.Fatalf("report %s: %v", run.Name, err)
			}
		},
		// The issue event feed lags a little behind the writes.
		Settle: func(*testing.T) { time.Sleep(3 * time.Second) },
	}
	t.Run("tracker", func(t *testing.T) { contracttest.RunTracker(t, env) })
	t.Run("scm", func(t *testing.T) { contracttest.RunSCM(t, env) })
	t.Run("ci", func(t *testing.T) { contracttest.RunCI(t, env) })
}

// cleanup closes what the run opened. It is best effort: a failure here must
// not hide the result of the test.
func cleanup(raw *gh.Client, owner, name, prefix string, milestone int) {
	ctx := context.Background()
	issues, _, _ := raw.Issues.ListByRepo(ctx, owner, name, &gh.IssueListByRepoOptions{Milestone: strconv.Itoa(milestone), State: "open", ListOptions: gh.ListOptions{PerPage: 100}})
	for _, is := range issues {
		_, _, _ = raw.Issues.Edit(ctx, owner, name, is.GetNumber(), &gh.IssueRequest{State: gh.Ptr("closed")})
	}
	prs, _, _ := raw.PullRequests.List(ctx, owner, name, &gh.PullRequestListOptions{State: "open", ListOptions: gh.ListOptions{PerPage: 100}})
	for _, pr := range prs {
		if strings.HasPrefix(pr.GetHead().GetRef(), prefix) {
			_, _, _ = raw.PullRequests.Edit(ctx, owner, name, pr.GetNumber(), &gh.PullRequest{State: gh.Ptr("closed")})
		}
	}
	branches, _, _ := raw.Repositories.ListBranches(ctx, owner, name, &gh.BranchListOptions{ListOptions: gh.ListOptions{PerPage: 100}})
	for _, b := range branches {
		if strings.HasPrefix(b.GetName(), prefix) {
			_, _ = raw.Git.DeleteRef(ctx, owner, name, "heads/"+b.GetName())
		}
	}
	_, _, _ = raw.Issues.EditMilestone(ctx, owner, name, milestone, &gh.Milestone{State: gh.Ptr("closed")})
}
