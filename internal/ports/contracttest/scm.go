package contracttest

import (
	"context"
	"errors"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// RunSCM runs the SCM contract against env.SCM.
func RunSCM(t *testing.T, env Env) {
	t.Helper()
	ctx := context.Background()
	scm := env.SCM

	t.Run("branches", func(t *testing.T) {
		name := env.Prefix + "/branch"
		base, err := scm.BranchHead(ctx, env.BaseBranch)
		if err != nil || base == "" {
			t.Fatalf("BranchHead(base) = %q, %v", base, err)
		}
		sha, err := scm.EnsureBranch(ctx, name, env.BaseBranch)
		if err != nil || sha != base {
			t.Fatalf("EnsureBranch = %q, %v; want the base head %q", sha, err, base)
		}
		pushed := env.Commit(t, name)
		again, err := scm.EnsureBranch(ctx, name, env.BaseBranch)
		if err != nil || again != pushed {
			t.Errorf("EnsureBranch on an existing branch = %q, %v; want it untouched at %q", again, err, pushed)
		}
		if head, err := scm.BranchHead(ctx, name); err != nil || head != pushed {
			t.Errorf("BranchHead = %q, %v; want %q", head, err, pushed)
		}
		fromSHA, err := scm.EnsureBranch(ctx, name+"-from-sha", base)
		if err != nil || fromSHA != base {
			t.Errorf("EnsureBranch from a sha = %q, %v; want %q", fromSHA, err, base)
		}
		if _, err := scm.BranchHead(ctx, env.Prefix+"/absent"); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("BranchHead(absent): err = %v, want ErrNotFound", err)
		}
		if _, err := scm.EnsureBranch(ctx, env.Prefix+"/orphan", env.Prefix+"/absent"); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("EnsureBranch from an absent ref: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		head := env.Prefix + "/pr"
		if _, err := scm.EnsureBranch(ctx, head, env.BaseBranch); err != nil {
			t.Fatal(err)
		}
		in := ports.NewPR{Title: env.Prefix + " milestone", Body: "all nodes integrated", Head: head, Base: env.BaseBranch}
		if _, err := scm.OpenPR(ctx, in, env.Prefix+"/pr/1"); !errors.Is(err, ports.ErrInvalid) {
			t.Fatalf("OpenPR without commits: err = %v, want ErrInvalid", err)
		}
		sha := env.Commit(t, head)
		pr, err := scm.OpenPR(ctx, in, env.Prefix+"/pr/1")
		if err != nil {
			t.Fatalf("OpenPR: %v", err)
		}
		if pr.Ref == "" || pr.Head != head || pr.Base != env.BaseBranch || pr.State != "open" || pr.Merged || pr.HeadSHA != sha || pr.Body != in.Body || pr.Title != in.Title {
			t.Errorf("PR = %+v", pr)
		}
		again, err := scm.OpenPR(ctx, in, env.Prefix+"/pr/1")
		if err != nil || again.Ref != pr.Ref {
			t.Fatalf("repeat = %+v, %v; want PR %s again", again, err, pr.Ref)
		}

		if err := scm.UpdatePR(ctx, pr.Ref, "updated body"); err != nil {
			t.Fatal(err)
		}
		if got, err := scm.PR(ctx, pr.Ref); err != nil || got.Body != "updated body" {
			t.Errorf("PR after update = %+v, %v", got, err)
		}
		if merged, err := scm.IsMerged(ctx, pr.Ref); err != nil || merged {
			t.Errorf("IsMerged before = %v, %v", merged, err)
		}
		env.Merge(t, pr.Ref)
		if merged, err := scm.IsMerged(ctx, pr.Ref); err != nil || !merged {
			t.Errorf("IsMerged after = %v, %v", merged, err)
		}
		if got, err := scm.PR(ctx, pr.Ref); err != nil || !got.Merged || got.State != "closed" {
			t.Errorf("PR after merge = %+v, %v", got, err)
		}
		if again, err := scm.OpenPR(ctx, in, env.Prefix+"/pr/1"); err != nil || again.Ref != pr.Ref {
			t.Errorf("repeat after merge = %+v, %v; want the same PR, not a new one", again, err)
		}
		if _, err := scm.PR(ctx, "999999"); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("PR(absent): err = %v, want ErrNotFound", err)
		}
	})
}
