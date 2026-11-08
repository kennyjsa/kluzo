package github

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v75/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Milestone implements ports.Tracker. A milestone ref is its number.
func (a *Adapter) Milestone(ctx context.Context, ref string) (ports.Milestone, error) {
	n, err := number("milestone", ref)
	if err != nil {
		return ports.Milestone{}, err
	}
	m, _, err := a.gh.Issues.GetMilestone(ctx, a.owner, a.repo, n)
	if err != nil {
		return ports.Milestone{}, a.fail("get milestone "+ref, err)
	}
	return ports.Milestone{Ref: ref, Title: m.GetTitle(), Description: m.GetDescription(), State: m.GetState(), URL: m.GetHTMLURL()}, nil
}

// MilestoneIssues implements ports.Tracker: every issue of the milestone, open
// or closed, oldest first. Pull requests are not nodes and are skipped.
func (a *Adapter) MilestoneIssues(ctx context.Context, ref string) ([]ports.Issue, error) {
	if _, err := a.Milestone(ctx, ref); err != nil {
		return nil, err
	}
	raw, err := a.listIssues(ctx, ref)
	if err != nil {
		return nil, a.fail("list issues of milestone "+ref, err)
	}
	out := make([]ports.Issue, len(raw))
	for i, is := range raw {
		out[i] = toIssue(is)
	}
	return out, nil
}

// listIssues returns the issues (not PRs) of a milestone, or of the whole
// repository when milestone is empty, oldest first. Bodies keep their markers.
func (a *Adapter) listIssues(ctx context.Context, milestone string) ([]*gh.Issue, error) {
	opts := &gh.IssueListByRepoOptions{
		Milestone: milestone, State: "all", Sort: "created", Direction: "asc",
		ListOptions: gh.ListOptions{PerPage: pageSize},
	}
	var out []*gh.Issue
	err := pages(func(page int) (int, error) {
		opts.ListOptions.Page = page
		batch, resp, err := a.gh.Issues.ListByRepo(ctx, a.owner, a.repo, opts)
		for _, is := range batch {
			if !is.IsPullRequest() {
				out = append(out, is)
			}
		}
		return nextPage(resp), err
	})
	return out, err
}

// Issue implements ports.Tracker.
func (a *Adapter) Issue(ctx context.Context, ref string) (ports.Issue, error) {
	is, err := a.getIssue(ctx, ref)
	if err != nil {
		return ports.Issue{}, err
	}
	return toIssue(is), nil
}

// getIssue fetches an issue; a pull request is not an issue for the Kluzo.
func (a *Adapter) getIssue(ctx context.Context, ref string) (*gh.Issue, error) {
	n, err := number("issue", ref)
	if err != nil {
		return nil, err
	}
	is, _, err := a.gh.Issues.Get(ctx, a.owner, a.repo, n)
	if err != nil {
		return nil, a.fail("get issue "+ref, err)
	}
	if is.IsPullRequest() {
		return nil, a.fail("get issue "+ref, fmt.Errorf("%w: it is a pull request", ports.ErrNotFound))
	}
	return is, nil
}

func toIssue(is *gh.Issue) ports.Issue {
	body, _ := splitMarker(is.GetBody())
	out := ports.Issue{
		Ref: strconv.Itoa(is.GetNumber()), Title: is.GetTitle(), Body: body, State: is.GetState(), URL: is.GetHTMLURL(),
	}
	for _, l := range is.Labels {
		out.Labels = append(out.Labels, l.GetName())
	}
	if m := is.Milestone; m != nil {
		out.MilestoneRef = strconv.Itoa(m.GetNumber())
	}
	return out
}

// CreateIssue implements ports.Tracker. The issue carries the idempotency key
// in a hidden marker; a repeat finds it and returns the issue already created.
func (a *Adapter) CreateIssue(ctx context.Context, in ports.NewIssue, idemKey string) (ports.Issue, error) {
	if err := checkKey(idemKey); err != nil {
		return ports.Issue{}, a.fail("create issue", err)
	}
	if i := slices.IndexFunc(in.Labels, isStateLabel); i >= 0 {
		return ports.Issue{}, a.fail("create issue", fmt.Errorf("label %q at creation, use SetStateLabel: %w", in.Labels[i], ports.ErrInvalid))
	}
	existing, err := a.listIssues(ctx, in.MilestoneRef)
	if err != nil {
		return ports.Issue{}, a.fail("look for issue "+idemKey, err)
	}
	for _, is := range existing {
		if _, key := splitMarker(is.GetBody()); key == idemKey {
			return toIssue(is), nil
		}
	}
	req := &gh.IssueRequest{Title: &in.Title, Body: gh.Ptr(withMarker(in.Body, idemKey))}
	if len(in.Labels) > 0 {
		req.Labels = &in.Labels
	}
	if in.MilestoneRef != "" {
		m, err := number("milestone", in.MilestoneRef)
		if err != nil {
			return ports.Issue{}, err
		}
		req.Milestone = &m
	}
	is, _, err := a.gh.Issues.Create(ctx, a.owner, a.repo, req)
	if err != nil {
		return ports.Issue{}, a.fail("create issue", err)
	}
	return toIssue(is), nil
}

func isStateLabel(l string) bool { return strings.HasPrefix(l, ports.StateLabelPrefix) }

// SetStateLabel implements ports.Tracker: the labels become the issue's other
// labels plus label, in one call, so no orch/* is left over. Repeating it
// changes nothing.
func (a *Adapter) SetStateLabel(ctx context.Context, issueRef, label string) error {
	if !isStateLabel(label) {
		return a.fail("set state label", fmt.Errorf("label %q must start with %s: %w", label, ports.StateLabelPrefix, ports.ErrInvalid))
	}
	is, err := a.getIssue(ctx, issueRef)
	if err != nil {
		return err
	}
	wanted, changed := []string{}, true
	for _, l := range is.Labels {
		switch name := l.GetName(); {
		case name == label:
			changed = false // already there; any other orch/* still has to go
		case !isStateLabel(name):
			wanted = append(wanted, name)
		}
	}
	wanted = append(wanted, label)
	if !changed && len(wanted) == len(is.Labels) {
		return nil
	}
	if _, _, err := a.gh.Issues.ReplaceLabelsForIssue(ctx, a.owner, a.repo, is.GetNumber(), wanted); err != nil {
		return a.fail("set label "+label, err)
	}
	return nil
}

// AddLabels implements ports.Tracker.
func (a *Adapter) AddLabels(ctx context.Context, issueRef string, labels []string) error {
	if i := slices.IndexFunc(labels, isStateLabel); i >= 0 {
		return a.fail("add labels", fmt.Errorf("label %q is an orch/* label, use SetStateLabel: %w", labels[i], ports.ErrInvalid))
	}
	n, err := number("issue", issueRef)
	if err != nil {
		return err
	}
	if len(labels) == 0 {
		return nil
	}
	if _, _, err := a.gh.Issues.AddLabelsToIssue(ctx, a.owner, a.repo, n, labels); err != nil {
		return a.fail("add labels to issue "+issueRef, err)
	}
	return nil
}

// Comment implements ports.Tracker. The comment ends with a hidden marker with
// the key; a repeat finds it and posts nothing.
func (a *Adapter) Comment(ctx context.Context, issueRef, body, idemKey string) (ports.CommentRef, error) {
	if err := checkKey(idemKey); err != nil {
		return ports.CommentRef{}, a.fail("comment", err)
	}
	n, err := number("issue", issueRef)
	if err != nil {
		return ports.CommentRef{}, err
	}
	all, err := a.listComments(ctx, n)
	if err != nil {
		return ports.CommentRef{}, a.fail("list comments of issue "+issueRef, err)
	}
	for _, c := range all {
		if _, key := splitMarker(c.GetBody()); key == idemKey {
			return toCommentRef(c), nil
		}
	}
	c, _, err := a.gh.Issues.CreateComment(ctx, a.owner, a.repo, n, &gh.IssueComment{Body: gh.Ptr(withMarker(body, idemKey))})
	if err != nil {
		return ports.CommentRef{}, a.fail("comment on issue "+issueRef, err)
	}
	return toCommentRef(c), nil
}

func toCommentRef(c *gh.IssueComment) ports.CommentRef {
	return ports.CommentRef{ID: strconv.FormatInt(c.GetID(), 10), URL: c.GetHTMLURL()}
}

func (a *Adapter) listComments(ctx context.Context, issue int) ([]*gh.IssueComment, error) {
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: pageSize}}
	var out []*gh.IssueComment
	err := pages(func(page int) (int, error) {
		opts.Page = page
		batch, resp, err := a.gh.Issues.ListComments(ctx, a.owner, a.repo, issue, opts)
		out = append(out, batch...)
		return nextPage(resp), err
	})
	return out, err
}

// CommentsSince implements ports.Tracker. The cursor is the id of the last
// comment seen.
func (a *Adapter) CommentsSince(ctx context.Context, issueRef, cursor string) ([]ports.Comment, string, error) {
	n, err := number("issue", issueRef)
	if err != nil {
		return nil, "", err
	}
	after, err := parseCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	all, err := a.listComments(ctx, n)
	if err != nil {
		return nil, "", a.fail("list comments of issue "+issueRef, err)
	}
	next := after
	var out []ports.Comment
	for _, c := range all {
		if c.GetID() <= after {
			continue
		}
		body, _ := splitMarker(c.GetBody())
		out = append(out, ports.Comment{ID: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(), Body: body, CreatedAt: c.GetCreatedAt().Time})
		next = max(next, c.GetID())
	}
	return out, strconv.FormatInt(next, 10), nil
}

// Changes implements ports.Tracker with the repository's issue event feed. The
// cursor is the id of the newest event seen.
func (a *Adapter) Changes(ctx context.Context, milestoneRef, cursor string) ([]ports.Change, string, error) {
	if _, err := a.Milestone(ctx, milestoneRef); err != nil {
		return nil, "", err
	}
	after, err := parseCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	type found struct {
		id     int64
		change ports.Change
	}
	var events []found
	head := int64(0)
	opts := &gh.ListOptions{PerPage: pageSize}
	if cursor == "" {
		opts.PerPage = 1 // only the position of the present is needed
	}
	err = pages(func(page int) (int, error) {
		opts.Page = page
		batch, resp, err := a.gh.Issues.ListRepositoryEvents(ctx, a.owner, a.repo, opts) // newest first
		for _, e := range batch {
			head = max(head, e.GetID())
			if cursor == "" || e.GetID() <= after {
				return 0, err // everything older was already seen
			}
			if c, ok := toChange(e, milestoneRef); ok {
				events = append(events, found{e.GetID(), c})
			}
		}
		return nextPage(resp), err
	})
	if err != nil {
		return nil, "", a.fail("list events", err)
	}
	if cursor != "" {
		head = max(head, after)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].id < events[j].id })
	var out []ports.Change
	for _, e := range events {
		out = append(out, e.change)
	}
	return out, strconv.FormatInt(head, 10), nil
}

// toChange keeps the label events of the milestone's issues.
func toChange(e *gh.IssueEvent, milestoneRef string) (ports.Change, bool) {
	kind := e.GetEvent()
	if (kind != ports.ChangeLabeled && kind != ports.ChangeUnlabeled) || e.Label == nil {
		return ports.Change{}, false
	}
	is := e.Issue
	if is == nil || is.IsPullRequest() || strconv.Itoa(is.GetMilestone().GetNumber()) != milestoneRef {
		return ports.Change{}, false
	}
	return ports.Change{
		Kind: kind, IssueRef: strconv.Itoa(is.GetNumber()), Label: e.Label.GetName(),
		Actor: e.GetActor().GetLogin(), At: e.GetCreatedAt().Time,
	}, true
}

func parseCursor(c string) (int64, error) {
	if c == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(c, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cursor %q: %w", c, ports.ErrInvalid)
	}
	return n, nil
}

func nextPage(resp *gh.Response) int {
	if resp == nil {
		return 0
	}
	return resp.NextPage
}
