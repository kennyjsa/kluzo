package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/contracttest"
)

var epoch = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// harness bundles a fake GitHub with an adapter wired to it.
type harness struct {
	f      *fakeGitHub
	a      *Adapter
	clock  *clock.Manual
	sleeps []time.Duration
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFakeGitHub(t)
	h := &harness{f: f, clock: clock.NewManual(epoch)}
	h.a = h.adapter(t, Auth{Kind: AuthToken, Token: f.token})
	return h
}

func (h *harness) adapter(t *testing.T, auth Auth) *Adapter {
	t.Helper()
	a, err := New(context.Background(), Options{
		Repo: h.f.owner + "/" + h.f.repo, Auth: auth, BaseURL: h.f.srv.URL, HTTPClient: h.f.srv.Client(),
		Clock: h.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sleep: func(_ context.Context, d time.Duration) error { h.sleeps = append(h.sleeps, d); return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// env wires the contract suite to the fake server. reportAsStatus chooses how
// checks are published: commit statuses (what a token can create against the
// real API) or check runs.
func (h *harness) env(prefix string, reportAsStatus bool) contracttest.Env {
	return contracttest.Env{
		Tracker: h.a, SCM: h.a, CI: h.a, Prefix: prefix,
		Milestone:  strconv.Itoa(h.f.addMilestone("v0.1")),
		BaseBranch: "main",
		Commit:     func(_ *testing.T, branch string) string { return h.f.commit(branch) },
		Merge:      func(_ *testing.T, prRef string) { n, _ := strconv.Atoi(prRef); h.f.merge(n) },
		Report: func(_ *testing.T, sha string, run ports.CheckRun) {
			if reportAsStatus {
				h.f.setStatus(sha, run.Name, statusFor(run.State), run.URL)
				return
			}
			h.f.setCheckRun(sha, checkRunFor(run))
		},
	}
}

func statusFor(state string) string {
	return map[string]string{ports.CheckPassed: "success", ports.CheckFailed: "failure", ports.CheckPending: "pending", ports.CheckError: "error"}[state]
}

func checkRunFor(run ports.CheckRun) ghCheckRun {
	r := ghCheckRun{Name: run.Name, URL: run.URL, Status: "completed"}
	switch run.State {
	case ports.CheckPassed:
		r.Conclusion = "success"
	case ports.CheckFailed:
		r.Conclusion = "failure"
	case ports.CheckPending:
		r.Status = "in_progress"
	default:
		r.Conclusion = "stale"
	}
	return r
}

// The adapter passes the same suite as the in-memory fakes.
func TestAdapterPassesTheContract(t *testing.T) {
	t.Run("tracker", func(t *testing.T) { contracttest.RunTracker(t, newHarness(t).env("t1", true)) })
	t.Run("scm", func(t *testing.T) { contracttest.RunSCM(t, newHarness(t).env("t2", true)) })
	t.Run("ci with commit statuses", func(t *testing.T) { contracttest.RunCI(t, newHarness(t).env("t3", true)) })
	t.Run("ci with check runs", func(t *testing.T) { contracttest.RunCI(t, newHarness(t).env("t4", false)) })
}

func TestRepeatedWritesDoNotDuplicate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	env := h.env("idem", true)
	ms := env.Milestone
	is, err := h.a.CreateIssue(ctx, ports.NewIssue{Title: "node", Body: "b", MilestoneRef: ms}, "idem/issue")
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if _, err := h.a.CreateIssue(ctx, ports.NewIssue{Title: "node", Body: "b", MilestoneRef: ms}, "idem/issue"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.a.Comment(ctx, is.Ref, "running", "idem/comment"); err != nil {
			t.Fatal(err)
		}
		if err := h.a.SetStateLabel(ctx, is.Ref, "orch/running"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.a.EnsureBranch(ctx, "milestone/v0.1", "main"); err != nil {
			t.Fatal(err)
		}
		h.f.commit("milestone/v0.1")
		if _, err := h.a.OpenPR(ctx, ports.NewPR{Title: "v0.1", Head: "milestone/v0.1", Base: "main"}, "idem/pr"); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.f.requestCount("POST /repos/acme/sandbox/issues/1/comments"); got != 1 {
		t.Errorf("comment posts = %d, want 1", got)
	}
	if got := h.f.requestCount("PUT /repos/acme/sandbox/issues/1/labels"); got != 1 {
		t.Errorf("label writes = %d, want 1", got)
	}
	if got := h.f.requestCount("POST /repos/acme/sandbox/pulls"); got != 1 {
		t.Errorf("PR creations = %d, want 1", got)
	}
	if got := h.f.requestCount("POST /repos/acme/sandbox/issues"); got != 1 {
		t.Errorf("issue creations = %d, want 1", got)
	}
}

func TestStateLabelReplacesTheOldOne(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	is, _ := h.a.CreateIssue(ctx, ports.NewIssue{Title: "n", Labels: []string{"area/core"}}, "k/issue")
	// A human or a bug left two orch labels; the next call converges.
	h.f.mu.Lock()
	h.f.issue(1).Labels = append(h.f.issue(1).Labels, "orch/ready", "orch/blocked")
	h.f.mu.Unlock()
	if err := h.a.SetStateLabel(ctx, is.Ref, "orch/running"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.a.Issue(ctx, is.Ref)
	slices.Sort(got.Labels)
	if want := []string{"area/core", "orch/running"}; !slices.Equal(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
}

func TestMilestoneIssuesPaginatesAndSkipsPRs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ms := h.env("pg", true).Milestone
	var want []string
	for i := range 5 {
		is, err := h.a.CreateIssue(ctx, ports.NewIssue{Title: fmt.Sprint("n", i), MilestoneRef: ms}, fmt.Sprint("pg/", i))
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, is.Ref)
	}
	h.f.mu.Lock() // a PR in the same milestone is not a node
	h.f.issues = append(h.f.issues, &ghIssue{Number: 6, ID: 99, Title: "pr", State: "open", PR: true, Milestone: 1})
	h.f.mu.Unlock()

	got, err := h.a.MilestoneIssues(ctx, ms)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, is := range got {
		refs = append(refs, is.Ref)
	}
	if !slices.Equal(refs, want) {
		t.Errorf("refs = %v, want %v", refs, want)
	}
	if _, err := h.a.Issue(ctx, "6"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Issue(PR): err = %v, want ErrNotFound", err)
	}
}

func TestChangesIgnoresOtherMilestonesAndKeepsActors(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mine := h.env("ch", true).Milestone
	other := strconv.Itoa(h.f.addMilestone("v0.2"))
	a, _ := h.a.CreateIssue(ctx, ports.NewIssue{Title: "a", MilestoneRef: mine}, "ch/a")
	b, _ := h.a.CreateIssue(ctx, ports.NewIssue{Title: "b", MilestoneRef: other}, "ch/b")
	_, cursor, _ := h.a.Changes(ctx, mine, "")
	for _, ref := range []string{a.Ref, b.Ref} {
		if err := h.a.SetStateLabel(ctx, ref, "orch/ready"); err != nil {
			t.Fatal(err)
		}
	}
	changes, _, err := h.a.Changes(ctx, mine, cursor)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.Change{{Kind: ports.ChangeLabeled, IssueRef: a.Ref, Label: "orch/ready", Actor: h.f.actor}}
	for i := range changes {
		changes[i].At = time.Time{}
	}
	if diff := cmp.Diff(want, changes); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
}

func TestCursorMustBeANumber(t *testing.T) {
	h := newHarness(t)
	ms := h.env("cur", true).Milestone
	if _, _, err := h.a.Changes(context.Background(), ms, "abc"); !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("Changes: err = %v, want ErrInvalid", err)
	}
	is, _ := h.a.CreateIssue(context.Background(), ports.NewIssue{Title: "n"}, "cur/i")
	if _, _, err := h.a.CommentsSince(context.Background(), is.Ref, "-1"); !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("CommentsSince: err = %v, want ErrInvalid", err)
	}
}

func TestDependenciesReadTheDescription(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for i := range 3 {
		if _, err := h.a.CreateIssue(ctx, ports.NewIssue{Title: fmt.Sprint("n", i), Body: "plain"}, fmt.Sprint("d/", i)); err != nil {
			t.Fatal(err)
		}
	}
	body := "Implements the thing.\n\nDepende de: #1, #2\ncontrato: #3 e #1\nDepende de: #3\n"
	h.f.mu.Lock()
	h.f.issue(3).Body = body
	h.f.blockedBy[3] = []int{1} // native, also written in the description
	h.f.mu.Unlock()

	got, err := h.a.Dependencies(ctx, "3")
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.Dependency{
		{FromRef: "1", ToRef: "3", Kind: contracts.EdgeBlocks, Source: ports.SourceNative},
		{FromRef: "2", ToRef: "3", Kind: contracts.EdgeBlocks, Source: ports.SourceDescription},
		{FromRef: "1", ToRef: "3", Kind: contracts.EdgeContract, Source: ports.SourceDescription},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Dependencies (-want +got):\n%s", diff)
	}

	// Rewriting contracts replaces the human's line and keeps the rest.
	if err := h.a.SetDependencies(ctx, "3", []ports.Dependency{{FromRef: "2", ToRef: "3", Kind: contracts.EdgeContract}}); err != nil {
		t.Fatal(err)
	}
	is, _ := h.a.Issue(ctx, "3")
	if want := "Implements the thing.\n\nDepende de: #1, #2\nDepende de: #3\n\nContrato: #2"; is.Body != want {
		t.Errorf("body = %q, want %q", is.Body, want)
	}
}

func TestSetDependenciesWithAnUnknownBlockerChangesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.a.CreateIssue(ctx, ports.NewIssue{Title: "a"}, "u/a") //nolint:errcheck // seeding
	h.a.CreateIssue(ctx, ports.NewIssue{Title: "b"}, "u/b") //nolint:errcheck // seeding
	deps := []ports.Dependency{
		{FromRef: "1", ToRef: "2", Kind: contracts.EdgeBlocks},
		{FromRef: "999", ToRef: "2", Kind: contracts.EdgeBlocks},
	}
	if err := h.a.SetDependencies(ctx, "2", deps); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got, _ := h.a.Dependencies(ctx, "2"); len(got) != 0 {
		t.Errorf("dependencies = %+v, want none: nothing may be written before every blocker is checked", got)
	}
}

func TestChecksMapGitHubStates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sha := h.f.commit("main")
	runs := []ghCheckRun{
		{Name: "ok", Status: "completed", Conclusion: "success"}, {Name: "neutral", Status: "completed", Conclusion: "neutral"},
		{Name: "skipped", Status: "completed", Conclusion: "skipped"}, {Name: "red", Status: "completed", Conclusion: "failure"},
		{Name: "timeout", Status: "completed", Conclusion: "timed_out"}, {Name: "cancel", Status: "completed", Conclusion: "cancelled"},
		{Name: "stale", Status: "completed", Conclusion: "stale"}, {Name: "queued", Status: "queued"}, {Name: "going", Status: "in_progress"},
	}
	for _, r := range runs {
		h.f.setCheckRun(sha, r)
	}
	h.f.setStatus(sha, "legacy-ok", "success", "")
	h.f.setStatus(sha, "legacy-bad", "error", "")
	h.f.setStatus(sha, "both", "failure", "")
	h.f.setCheckRun(sha, ghCheckRun{Name: "both", Status: "completed", Conclusion: "success"})

	got, err := h.a.Checks(ctx, sha, nil)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, c := range got {
		states[c.Name] = c.State
	}
	want := map[string]string{
		"ok": "passed", "neutral": "passed", "skipped": "passed", "red": "failed", "timeout": "failed", "cancel": "failed",
		"stale": "error", "queued": "pending", "going": "pending",
		"legacy-ok": "passed", "legacy-bad": "error", "both": "passed", // the check run wins over the status
	}
	if diff := cmp.Diff(want, states); diff != "" {
		t.Errorf("states (-want +got):\n%s", diff)
	}
}

func TestSetContractLine(t *testing.T) {
	tests := []struct {
		name, body string
		refs       []string
		want       string
	}{
		{"nothing to do", "text", nil, "text"},
		{"appends", "text", []string{"1", "2"}, "text\n\nContrato: #1, #2"},
		{"empty body", "", []string{"7"}, "Contrato: #7"},
		{"replaces", "text\n\nContrato: #9\nmore", []string{"1"}, "text\n\nmore\n\nContrato: #1"},
		{"removes", "text\n\nContrato: #9\n", nil, "text"},
		{"case and spaces", "text\n  contrato :#9", []string{"1"}, "text\n\nContrato: #1"},
		{"idempotent", "text\n\nContrato: #1", []string{"1"}, "text\n\nContrato: #1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setContractLine(tt.body, tt.refs)
			if got != tt.want {
				t.Errorf("setContractLine = %q, want %q", got, tt.want)
			}
			if again := setContractLine(got, tt.refs); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestMarkers(t *testing.T) {
	body := withMarker("hello\n\nworld", "ef_1/comment")
	clean, key := splitMarker(body)
	if clean != "hello\n\nworld" || key != "ef_1/comment" {
		t.Errorf("split = %q, %q", clean, key)
	}
	if clean, key := splitMarker(withMarker("", "k")); clean != "" || key != "k" {
		t.Errorf("empty body: %q, %q", clean, key)
	}
	if clean, key := splitMarker("no marker <!-- kluzo:idem=k --> in the middle\nand more"); key != "" || !strings.Contains(clean, "more") {
		t.Errorf("a marker that is not last is plain text: %q, %q", clean, key)
	}
	for _, bad := range []string{"", "a b", "a-->b", "a--b", "x\ny", strings.Repeat("a", 201)} {
		if err := checkKey(bad); !errors.Is(err, ports.ErrInvalid) {
			t.Errorf("checkKey(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestRateLimit(t *testing.T) {
	limited := func(remaining string, reset time.Time, retryAfter string) func(http.ResponseWriter, *http.Request) bool {
		return func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("X-RateLimit-Remaining", remaining)
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			writeJSON(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
			return true
		}
	}
	once := func(h *harness, respond func(http.ResponseWriter, *http.Request) bool) {
		calls := 0
		h.f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
			calls++
			return calls == 1 && respond(w, r)
		}
	}

	t.Run("waits for the reset and repeats the request", func(t *testing.T) {
		h := newHarness(t)
		once(h, limited("0", epoch.Add(30*time.Second), ""))
		if _, err := h.a.Milestone(context.Background(), strconv.Itoa(h.f.addMilestone("m"))); err != nil {
			t.Fatalf("Milestone: %v", err)
		}
		if want := []time.Duration{31 * time.Second}; !slices.Equal(h.sleeps, want) {
			t.Errorf("sleeps = %v, want %v", h.sleeps, want)
		}
	})
	t.Run("honors Retry-After on a secondary limit", func(t *testing.T) {
		h := newHarness(t)
		once(h, limited("12", epoch, "60"))
		if _, err := h.a.Milestone(context.Background(), strconv.Itoa(h.f.addMilestone("m"))); err != nil {
			t.Fatalf("Milestone: %v", err)
		}
		if want := []time.Duration{61 * time.Second}; !slices.Equal(h.sleeps, want) {
			t.Errorf("sleeps = %v, want %v", h.sleeps, want)
		}
	})
	t.Run("an exhausted quota holds the next request", func(t *testing.T) {
		h := newHarness(t)
		ms := strconv.Itoa(h.f.addMilestone("m"))
		h.f.intercept = func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(epoch.Add(10*time.Second).Unix(), 10))
			return false // answer normally, but say the quota is spent
		}
		if _, err := h.a.Milestone(context.Background(), ms); err != nil {
			t.Fatal(err)
		}
		if len(h.sleeps) != 0 {
			t.Fatalf("sleeps = %v before the second request", h.sleeps)
		}
		if _, err := h.a.Milestone(context.Background(), ms); err != nil {
			t.Fatal(err)
		}
		if want := []time.Duration{11 * time.Second}; !slices.Equal(h.sleeps, want) {
			t.Errorf("sleeps = %v, want %v", h.sleeps, want)
		}
	})
	t.Run("a wait that is too long is returned as an error", func(t *testing.T) {
		h := newHarness(t)
		h.f.intercept = limited("0", epoch.Add(3*time.Hour), "")
		_, err := h.a.Milestone(context.Background(), "1")
		if err == nil || ports.IsPermanent(err) || len(h.sleeps) != 0 {
			t.Errorf("err = %v (permanent %v), sleeps %v; want a retryable error and no sleep", err, ports.IsPermanent(err), h.sleeps)
		}
	})
	t.Run("a plain 403 is a permission problem, not a limit", func(t *testing.T) {
		h := newHarness(t)
		h.f.intercept = func(w http.ResponseWriter, _ *http.Request) bool {
			writeJSON(w, http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
			return true
		}
		_, err := h.a.Milestone(context.Background(), "1")
		if !errors.Is(err, ports.ErrPermanent) || len(h.sleeps) != 0 || h.f.requestCount("GET /repos/acme/sandbox/milestones/1") != 1 {
			t.Errorf("err = %v, sleeps %v, requests %d; want ErrPermanent without retry", err, h.sleeps, h.f.requestCount("GET /repos/acme/sandbox/milestones/1"))
		}
	})
	t.Run("gives up after a few refusals", func(t *testing.T) {
		h := newHarness(t)
		h.f.intercept = limited("12", epoch, "1")
		if _, err := h.a.Milestone(context.Background(), "1"); err == nil {
			t.Fatal("want an error")
		}
		if got := len(h.sleeps); got != maxRateLimitRetries {
			t.Errorf("sleeps = %d, want %d", got, maxRateLimitRetries)
		}
	})
}

func TestErrorClassification(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.a.Milestone(ctx, "404"); !errors.Is(err, ports.ErrNotFound) || !ports.IsPermanent(err) {
		t.Errorf("404: err = %v", err)
	}
	if _, err := h.a.Milestone(ctx, "abc"); !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("non-numeric ref: err = %v", err)
	}
	h.f.intercept = func(w http.ResponseWriter, _ *http.Request) bool {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "oops"})
		return true
	}
	if _, err := h.a.Milestone(ctx, "1"); err == nil || ports.IsPermanent(err) {
		t.Errorf("502: err = %v, want a retryable error", err)
	}
}

func TestBadCredentialsAreNotLeaked(t *testing.T) {
	h := newHarness(t)
	secret := "ghp_" + strings.Repeat("S", 36)
	bad := h.adapter(t, Auth{Kind: AuthToken, Token: secret})
	_, err := bad.Milestone(context.Background(), "1")
	if !errors.Is(err, ports.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the token: %v", err)
	}

	// Even if the server echoes the secret back, the message is masked.
	h.f.intercept = func(w http.ResponseWriter, _ *http.Request) bool {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "bad value " + h.f.token + " and " + secret})
		return true
	}
	good := h.a
	_, err = good.Milestone(context.Background(), "1")
	if err == nil || strings.Contains(err.Error(), h.f.token) || strings.Contains(err.Error(), secret) {
		t.Errorf("error = %v, want the secrets masked", err)
	}
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("masking must keep the classification: %v", err)
	}
}

func TestGitTokenOfAToken(t *testing.T) {
	tok := "ghp_" + strings.Repeat("g", 36)
	a := newHarness(t).adapter(t, Auth{Kind: AuthToken, Token: tok})
	if got, err := a.GitToken(context.Background()); err != nil || got != tok {
		t.Errorf("GitToken = %q, %v", got, err)
	}
}

func TestWhoamiOfATokenIsTheUserLogin(t *testing.T) {
	h := newHarness(t)
	for range 2 { // the second call is served from memory
		if login, err := h.a.Whoami(context.Background()); err != nil || login != h.f.actor {
			t.Fatalf("Whoami = %q, %v; want %q", login, err, h.f.actor)
		}
	}
	if n := h.f.requestCount("GET /user"); n != 1 {
		t.Errorf("GET /user was called %d times, want 1", n)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	ctx := context.Background()
	tok := Auth{Kind: AuthToken, Token: "ghp_" + strings.Repeat("a", 36)}
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"repo without owner", Options{Repo: "sandbox", Auth: tok}, "owner/name"},
		{"repo with extra slash", Options{Repo: "a/b/c", Auth: tok}, "owner/name"},
		{"unknown kind", Options{Repo: "a/b", Auth: Auth{Kind: "oauth"}}, "unknown kind"},
		{"empty token", Options{Repo: "a/b", Auth: Auth{Kind: AuthToken}}, "token is empty"},
		{"app without key", Options{Repo: "a/b", Auth: Auth{Kind: AuthGitHubApp, AppID: 1}}, "private key"},
		{"bad base url", Options{Repo: "a/b", Auth: tok, BaseURL: "::"}, "base url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(ctx, tt.opts); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestGitHubAppAuthentication(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	h := newHarness(t)
	h.f.token = "ghs_" + strings.Repeat("i", 36) // the installation token
	var asked []string
	h.f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/app":
			asked = append(asked, "app")
			writeJSON(w, http.StatusOK, map[string]any{"id": 4242, "slug": "kluzo-test"})
		case "/repos/acme/sandbox/installation":
			asked = append(asked, "installation")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer eyJ") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "a JWT is required"})
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": 77})
		case "/app/installations/77/access_tokens":
			asked = append(asked, "token")
			writeJSON(w, http.StatusCreated, map[string]any{"token": h.f.token, "expires_at": epoch.Add(time.Hour)})
		default:
			return false
		}
		return true
	}
	a := h.adapter(t, Auth{Kind: AuthGitHubApp, AppID: 4242, PrivateKey: pemKey})
	ms := strconv.Itoa(h.f.addMilestone("v0.1"))
	if _, err := a.Milestone(context.Background(), ms); err != nil {
		t.Fatalf("Milestone with an installation token: %v", err)
	}
	if want := []string{"app", "installation", "token"}; !slices.Equal(asked, want) {
		t.Errorf("app handshake = %v, want %v", asked, want)
	}
	if login, err := a.Whoami(context.Background()); err != nil || login != "kluzo-test[bot]" {
		t.Errorf("Whoami of an App = %q, %v; want kluzo-test[bot]", login, err)
	}
	if tok, err := a.GitToken(context.Background()); err != nil || tok != h.f.token {
		t.Errorf("GitToken of an App = %q, %v; want the installation token", tok, err)
	}

	_, err = New(context.Background(), Options{Repo: "acme/sandbox", Auth: Auth{Kind: AuthGitHubApp, AppID: 1, PrivateKey: []byte("not a key")}, BaseURL: h.f.srv.URL, HTTPClient: h.f.srv.Client()})
	if err == nil || strings.Contains(err.Error(), "not a key") {
		t.Errorf("bad private key: err = %v, want an error that does not echo the key", err)
	}
}

// A closed PR of the same branches that this key did not open (an earlier use
// of the branch name, or someone else's) is not reused; the PR this key
// opened is, even after its merge.
func TestOpenPRIgnoresAForeignClosedPR(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.a.EnsureBranch(ctx, "milestone/v0.1", "main"); err != nil {
		t.Fatal(err)
	}
	h.f.commit("milestone/v0.1")
	h.f.mu.Lock()
	h.f.prs = append(h.f.prs, &ghPR{Number: 99, Title: "old", Body: "an earlier milestone", Head: "milestone/v0.1", Base: "main", State: "closed", Merged: true})
	h.f.mu.Unlock()
	pr, err := h.a.OpenPR(ctx, ports.NewPR{Title: "v0.1", Head: "milestone/v0.1", Base: "main"}, "idem/pr")
	if err != nil || pr.Ref == "99" || pr.Merged {
		t.Fatalf("OpenPR = %+v, %v; want a new PR", pr, err)
	}
	h.f.merge(2) // the second PR of the fake, by position
	if again, err := h.a.OpenPR(ctx, ports.NewPR{Title: "v0.1", Head: "milestone/v0.1", Base: "main"}, "idem/pr"); err != nil || again.Ref != pr.Ref || !again.Merged {
		t.Errorf("OpenPR again = %+v, %v; want the merged PR of the key", again, err)
	}
}
