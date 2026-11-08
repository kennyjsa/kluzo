package github

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	gh "github.com/google/go-github/v75/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// branchRef is the name go-github's Git API expects for a branch.
func branchRef(name string) string { return "heads/" + name }

// EnsureBranch implements ports.SCM. An existing branch is never moved.
func (a *Adapter) EnsureBranch(ctx context.Context, name, fromRef string) (string, error) {
	if sha, err := a.BranchHead(ctx, name); err == nil {
		return sha, nil
	} else if !errors.Is(err, ports.ErrNotFound) {
		return "", err
	}
	sha := fromRef
	if !fullSHA.MatchString(fromRef) {
		var err error
		if sha, err = a.BranchHead(ctx, fromRef); err != nil {
			return "", err
		}
	}
	_, _, err := a.gh.Git.CreateRef(ctx, a.owner, a.repo, gh.CreateRef{Ref: "refs/" + branchRef(name), SHA: sha})
	switch {
	case err == nil:
		return sha, nil
	case isStatus(err, 422):
		// Someone created it between our check and our create: take theirs.
		if head, headErr := a.BranchHead(ctx, name); headErr == nil {
			return head, nil
		}
	}
	return "", a.fail("create branch "+name, err)
}

// BranchHead implements ports.SCM.
func (a *Adapter) BranchHead(ctx context.Context, name string) (string, error) {
	ref, _, err := a.gh.Git.GetRef(ctx, a.owner, a.repo, branchRef(name))
	if err != nil {
		return "", a.fail("get branch "+name, err)
	}
	return ref.GetObject().GetSHA(), nil
}

// OpenPR implements ports.SCM. The PR is looked up by head and base before
// being created, so a repeat (even after the PR was merged) returns the same one.
func (a *Adapter) OpenPR(ctx context.Context, in ports.NewPR, idemKey string) (ports.PR, error) {
	if err := checkKey(idemKey); err != nil {
		return ports.PR{}, a.fail("open PR", err)
	}
	existing, _, err := a.gh.PullRequests.List(ctx, a.owner, a.repo, &gh.PullRequestListOptions{
		State: "all", Head: a.owner + ":" + in.Head, Base: in.Base, ListOptions: gh.ListOptions{PerPage: pageSize},
	})
	if err != nil {
		return ports.PR{}, a.fail("look for the PR of "+in.Head, err)
	}
	if pr := pickPR(existing); pr != nil {
		return toPR(pr), nil
	}
	pr, _, err := a.gh.PullRequests.Create(ctx, a.owner, a.repo, &gh.NewPullRequest{
		Title: &in.Title, Head: &in.Head, Base: &in.Base, Body: gh.Ptr(withMarker(in.Body, idemKey)),
	})
	if err != nil {
		return ports.PR{}, a.fail(fmt.Sprintf("open PR %s -> %s", in.Head, in.Base), err)
	}
	return toPR(pr), nil
}

// pickPR prefers an open PR; otherwise the newest (the list is newest first).
func pickPR(prs []*gh.PullRequest) *gh.PullRequest {
	for _, p := range prs {
		if p.GetState() == "open" {
			return p
		}
	}
	if len(prs) > 0 {
		return prs[0]
	}
	return nil
}

// UpdatePR implements ports.SCM, keeping the hidden marker of the PR.
func (a *Adapter) UpdatePR(ctx context.Context, ref, body string) error {
	n, err := number("PR", ref)
	if err != nil {
		return err
	}
	current, _, err := a.gh.PullRequests.Get(ctx, a.owner, a.repo, n)
	if err != nil {
		return a.fail("get PR "+ref, err)
	}
	if _, key := splitMarker(current.GetBody()); key != "" {
		body = withMarker(body, key)
	}
	if _, _, err := a.gh.PullRequests.Edit(ctx, a.owner, a.repo, n, &gh.PullRequest{Body: &body}); err != nil {
		return a.fail("update PR "+ref, err)
	}
	return nil
}

// PR implements ports.SCM.
func (a *Adapter) PR(ctx context.Context, ref string) (ports.PR, error) {
	n, err := number("PR", ref)
	if err != nil {
		return ports.PR{}, err
	}
	pr, _, err := a.gh.PullRequests.Get(ctx, a.owner, a.repo, n)
	if err != nil {
		return ports.PR{}, a.fail("get PR "+ref, err)
	}
	return toPR(pr), nil
}

// IsMerged implements ports.SCM. It only reads: merging is the human's.
func (a *Adapter) IsMerged(ctx context.Context, prRef string) (bool, error) {
	pr, err := a.PR(ctx, prRef)
	return pr.Merged, err
}

func toPR(p *gh.PullRequest) ports.PR {
	body, _ := splitMarker(p.GetBody())
	return ports.PR{
		Ref: strconv.Itoa(p.GetNumber()), Title: p.GetTitle(), Body: body,
		Head: p.GetHead().GetRef(), Base: p.GetBase().GetRef(), HeadSHA: p.GetHead().GetSHA(),
		State: p.GetState(), Merged: p.GetMerged(), URL: p.GetHTMLURL(),
	}
}
