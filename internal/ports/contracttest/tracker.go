package contracttest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// RunTracker runs the Tracker contract against env.Tracker.
func RunTracker(t *testing.T, env Env) {
	t.Helper()
	ctx := context.Background()
	tr := env.Tracker

	newIssue := func(t *testing.T, name string) ports.Issue {
		t.Helper()
		is, err := tr.CreateIssue(ctx, ports.NewIssue{Title: env.Prefix + " " + name, Body: "body of " + name, MilestoneRef: env.Milestone}, env.Prefix+"/create/"+name)
		if err != nil {
			t.Fatalf("CreateIssue(%s): %v", name, err)
		}
		return is
	}

	t.Run("says who Kluzo is", func(t *testing.T) {
		login, err := tr.Whoami(ctx)
		if err != nil || login == "" {
			t.Fatalf("Whoami = %q, %v; want a login", login, err)
		}
		if again, _ := tr.Whoami(ctx); again != login {
			t.Errorf("Whoami changed between calls: %q then %q", login, again)
		}
	})

	t.Run("reads the milestone and its issues", func(t *testing.T) {
		a, b := newIssue(t, "read-a"), newIssue(t, "read-b")
		m, err := tr.Milestone(ctx, env.Milestone)
		if err != nil || m.Ref != env.Milestone || m.Title == "" || m.State != "open" {
			t.Fatalf("Milestone = %+v, %v", m, err)
		}
		issues, err := tr.MilestoneIssues(ctx, env.Milestone)
		if err != nil {
			t.Fatal(err)
		}
		refs := map[string]ports.Issue{}
		for _, is := range issues {
			refs[is.Ref] = is
		}
		for _, want := range []ports.Issue{a, b} {
			got, ok := refs[want.Ref]
			if !ok || got.Title != want.Title || got.Body != want.Body || got.State != "open" || got.MilestoneRef != env.Milestone {
				t.Errorf("MilestoneIssues[%s] = %+v, want %+v", want.Ref, got, want)
			}
		}
		byTitle, err := tr.MilestonesByTitle(ctx, m.Title)
		if err != nil || len(byTitle) != 1 || byTitle[0].Ref != env.Milestone || byTitle[0].Title != m.Title || byTitle[0].State != "open" {
			t.Errorf("MilestonesByTitle(%q) = %+v, %v; want the milestone %s only", m.Title, byTitle, err, env.Milestone)
		}
		for _, title := range []string{m.Title[:len(m.Title)-1], env.Prefix + " no such milestone"} {
			if none, err := tr.MilestonesByTitle(ctx, title); err != nil || len(none) != 0 {
				t.Errorf("MilestonesByTitle(%q) = %+v, %v; want none (only exact titles match)", title, none, err)
			}
		}
		one, err := tr.Issue(ctx, a.Ref)
		if err != nil || one.Ref != a.Ref || one.Body != "body of read-a" {
			t.Errorf("Issue = %+v, %v", one, err)
		}
	})

	t.Run("missing resources are ErrNotFound", func(t *testing.T) {
		for name, err := range map[string]error{
			"milestone": second(tr.Milestone(ctx, "999999")),
			"issue":     second(tr.Issue(ctx, "999999")),
			"comment":   second(tr.Comment(ctx, "999999", "x", env.Prefix+"/missing")),
			"label":     tr.SetStateLabel(ctx, "999999", "orch/ready"),
		} {
			if !errors.Is(err, ports.ErrNotFound) || !ports.IsPermanent(err) {
				t.Errorf("%s: err = %v, want ErrNotFound (permanent)", name, err)
			}
		}
	})

	t.Run("CreateIssue is idempotent", func(t *testing.T) {
		first := newIssue(t, "idem-issue")
		again, err := tr.CreateIssue(ctx, ports.NewIssue{Title: "other title", Body: "other", MilestoneRef: env.Milestone}, env.Prefix+"/create/idem-issue")
		if err != nil || again.Ref != first.Ref {
			t.Fatalf("repeat = %+v, %v; want issue %s again", again, err, first.Ref)
		}
		if _, err := tr.CreateIssue(ctx, ports.NewIssue{Title: "x", Labels: []string{"orch/ready"}}, env.Prefix+"/create/bad-label"); !errors.Is(err, ports.ErrInvalid) {
			t.Errorf("orch/* label at creation: err = %v, want ErrInvalid", err)
		}
	})

	t.Run("one orch label per issue", func(t *testing.T) {
		is := newIssue(t, "labels")
		if err := tr.AddLabels(ctx, is.Ref, []string{env.Prefix + "-area", env.Prefix + "-area"}); err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"orch/ready", "orch/running", "orch/running"} {
			if err := tr.SetStateLabel(ctx, is.Ref, label); err != nil {
				t.Fatalf("SetStateLabel(%s): %v", label, err)
			}
		}
		got, err := tr.Issue(ctx, is.Ref)
		if err != nil {
			t.Fatal(err)
		}
		labels := slices.Clone(got.Labels)
		slices.Sort(labels)
		if want := []string{"orch/running", env.Prefix + "-area"}; !slices.Equal(labels, sorted(want)) {
			t.Errorf("labels = %v, want %v", labels, sorted(want))
		}
		if err := tr.SetStateLabel(ctx, is.Ref, "bug"); !errors.Is(err, ports.ErrInvalid) {
			t.Errorf("SetStateLabel(bug): err = %v, want ErrInvalid", err)
		}
		if err := tr.AddLabels(ctx, is.Ref, []string{"orch/done"}); !errors.Is(err, ports.ErrInvalid) {
			t.Errorf("AddLabels(orch/done): err = %v, want ErrInvalid", err)
		}
	})

	t.Run("a comment is posted once per key", func(t *testing.T) {
		is := newIssue(t, "comments")
		key := env.Prefix + "/comment/1"
		first, err := tr.Comment(ctx, is.Ref, "node is running", key)
		if err != nil || first.ID == "" {
			t.Fatalf("Comment = %+v, %v", first, err)
		}
		again, err := tr.Comment(ctx, is.Ref, "node is running", key)
		if err != nil || again.ID != first.ID {
			t.Fatalf("repeat = %+v, %v; want comment %s again", again, err, first.ID)
		}
		if _, err := tr.Comment(ctx, is.Ref, "node is in review", env.Prefix+"/comment/2"); err != nil {
			t.Fatal(err)
		}

		all, cursor, err := tr.CommentsSince(ctx, is.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := bodies(all); !slices.Equal(got, []string{"node is running", "node is in review"}) {
			t.Fatalf("comments = %q (hidden markers must not leak into the body)", got)
		}
		if all[0].Author == "" || all[0].CreatedAt.IsZero() {
			t.Errorf("comment = %+v, want author and time", all[0])
		}
		none, next, err := tr.CommentsSince(ctx, is.Ref, cursor)
		if err != nil || len(none) != 0 || next != cursor {
			t.Errorf("after the cursor: %v, %q, %v; want nothing and the same cursor", bodies(none), next, err)
		}
		if _, err := tr.Comment(ctx, is.Ref, "third", env.Prefix+"/comment/3"); err != nil {
			t.Fatal(err)
		}
		news, next, err := tr.CommentsSince(ctx, is.Ref, cursor)
		if err != nil || !slices.Equal(bodies(news), []string{"third"}) || next == cursor {
			t.Errorf("new comments = %q, cursor %q -> %q, %v", bodies(news), cursor, next, err)
		}
	})

	t.Run("dependencies", func(t *testing.T) {
		a, b, c := newIssue(t, "dep-a"), newIssue(t, "dep-b"), newIssue(t, "dep-c")
		deps := []ports.Dependency{
			{FromRef: a.Ref, ToRef: c.Ref, Kind: contracts.EdgeBlocks},
			{FromRef: b.Ref, ToRef: c.Ref, Kind: contracts.EdgeContract},
		}
		for range 2 { // setting twice changes nothing
			if err := tr.SetDependencies(ctx, c.Ref, deps); err != nil {
				t.Fatal(err)
			}
		}
		got, err := tr.Dependencies(ctx, c.Ref)
		if err != nil {
			t.Fatal(err)
		}
		want := []ports.Dependency{
			{FromRef: a.Ref, ToRef: c.Ref, Kind: contracts.EdgeBlocks, Source: ports.SourceNative},
			{FromRef: b.Ref, ToRef: c.Ref, Kind: contracts.EdgeContract, Source: ports.SourceDescription},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Dependencies (-want +got):\n%s", diff)
		}

		if err := tr.SetDependencies(ctx, c.Ref, deps[:1]); err != nil {
			t.Fatal(err)
		}
		got, _ = tr.Dependencies(ctx, c.Ref)
		if diff := cmp.Diff(want[:1], got); diff != "" {
			t.Errorf("after dropping the contract (-want +got):\n%s", diff)
		}
		if err := tr.SetDependencies(ctx, c.Ref, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ = tr.Dependencies(ctx, c.Ref); len(got) != 0 {
			t.Errorf("after clearing: %+v", got)
		}
		if body := mustIssue(t, tr, c.Ref).Body; body != c.Body {
			t.Errorf("body after clearing = %q, want the original %q", body, c.Body)
		}

		self := []ports.Dependency{{FromRef: c.Ref, ToRef: c.Ref, Kind: contracts.EdgeBlocks}}
		if err := tr.SetDependencies(ctx, c.Ref, self); !errors.Is(err, ports.ErrInvalid) {
			t.Errorf("self dependency: err = %v, want ErrInvalid", err)
		}
		ghost := []ports.Dependency{{FromRef: "999999", ToRef: c.Ref, Kind: contracts.EdgeBlocks}}
		if err := tr.SetDependencies(ctx, c.Ref, ghost); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("unknown blocker: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("changes follow the label events of the milestone", func(t *testing.T) {
		is := newIssue(t, "changes")
		env.settle(t)
		none, cursor, err := tr.Changes(ctx, env.Milestone, "")
		if err != nil || len(none) != 0 || cursor == "" {
			t.Fatalf("first poll = %v, %q, %v; want no changes and a cursor", none, cursor, err)
		}
		if err := tr.SetStateLabel(ctx, is.Ref, "orch/ready"); err != nil {
			t.Fatal(err)
		}
		if err := tr.SetStateLabel(ctx, is.Ref, "orch/running"); err != nil {
			t.Fatal(err)
		}
		env.settle(t)
		changes, next, err := tr.Changes(ctx, env.Milestone, cursor)
		if err != nil {
			t.Fatal(err)
		}
		me, err := tr.Whoami(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range changes {
			if c.IssueRef == is.Ref {
				if c.Actor != me || c.At.IsZero() {
					t.Errorf("change %+v: want the actor %q (Whoami) and a time", c, me)
				}
				got = append(got, c.Kind+" "+c.Label)
			}
		}
		// Within one swap the tracker may order "unlabeled old" and "labeled new" either way.
		want := []string{"labeled orch/ready", "labeled orch/running", "unlabeled orch/ready"}
		if slices.Sort(got); !slices.Equal(got, want) {
			t.Errorf("changes = %q, want %q", got, want)
		}
		again, _, err := tr.Changes(ctx, env.Milestone, next)
		if err != nil || len(again) != 0 {
			t.Errorf("after the cursor: %v, %v; want nothing", again, err)
		}
	})
}

func bodies(cs []ports.Comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = strings.TrimSpace(c.Body)
	}
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

func second[T any](_ T, err error) error { return err }

func mustIssue(t *testing.T, tr ports.Tracker, ref string) ports.Issue {
	t.Helper()
	is, err := tr.Issue(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return is
}
