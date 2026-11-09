package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// The lists of issues and milestones go through GraphQL. The REST lists
// (GET /repos/{owner}/{repo}/issues and /milestones, with or without filters)
// lag about a second behind the writes: an issue just created is missing from
// them, so the idempotency of CreateIssue failed and created a second issue,
// and MilestoneIssues missed the issues just created. GraphQL answers what a
// write just did, like the GET of a single issue.

// graphql runs a query and decodes its data into out. An error of the query
// (GitHub answers 200 with "errors") becomes an error; NOT_FOUND wraps
// ports.ErrNotFound.
func (a *Adapter) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	// GitHub Enterprise serves REST under /api/v3/ and GraphQL at /api/graphql.
	endpoint := "graphql"
	if strings.HasSuffix(a.gh.BaseURL(), "/api/v3/") {
		endpoint = "../graphql"
	}
	req, err := a.gh.NewRequest(ctx, http.MethodPost, endpoint, map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if _, err := a.gh.Do(req, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		e := resp.Errors[0]
		if e.Type == "NOT_FOUND" {
			return fmt.Errorf("graphql: %s: %w", e.Message, ports.ErrNotFound)
		}
		return fmt.Errorf("graphql: %s", e.Message)
	}
	return json.Unmarshal(resp.Data, out)
}

// page is a connection of GraphQL: its nodes and where the next page starts.
type page[T any] struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []T `json:"nodes"`
}

// connection reads every page of a connection; at picks it from the data of
// each answer (nil: the parent does not exist).
func connection[T, D any](ctx context.Context, a *Adapter, query string, vars map[string]any, at func(*D) *page[T]) ([]T, error) {
	var out []T
	for after := ""; ; {
		vars["after"] = nil
		if after != "" {
			vars["after"] = after
		}
		var data D
		if err := a.graphql(ctx, query, vars, &data); err != nil {
			return nil, err
		}
		p := at(&data)
		if p == nil {
			return nil, ports.ErrNotFound
		}
		out = append(out, p.Nodes...)
		if !p.PageInfo.HasNextPage {
			return out, nil
		}
		after = p.PageInfo.EndCursor
	}
}

const issueFields = `pageInfo { hasNextPage endCursor }
nodes { number title body state url labels(first: 100) { nodes { name } } milestone { number } }`

const milestoneIssuesQuery = `query($owner: String!, $name: String!, $milestone: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    milestone(number: $milestone) {
      issues(first: 100, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) { ` + issueFields + ` }
    }
  }
}`

const repoIssuesQuery = `query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(first: 100, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) { ` + issueFields + ` }
  }
}`

// gqlIssue is an issue as GraphQL returns it.
type gqlIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"` // OPEN | CLOSED
	URL    string `json:"url"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Milestone *struct {
		Number int `json:"number"`
	} `json:"milestone"`
}

// rest is the issue in the shape of the REST API, which toIssue reads.
func (n gqlIssue) rest() *gh.Issue {
	is := &gh.Issue{Number: &n.Number, Title: &n.Title, Body: &n.Body, State: new(strings.ToLower(n.State)), HTMLURL: &n.URL}
	for _, l := range n.Labels.Nodes {
		is.Labels = append(is.Labels, &gh.Label{Name: l.Name})
	}
	if n.Milestone != nil {
		is.Milestone = &gh.Milestone{Number: &n.Milestone.Number}
	}
	return is
}

// listIssues returns the issues (not PRs) of a milestone, or of the whole
// repository when milestone is empty, oldest first. Bodies keep their markers.
func (a *Adapter) listIssues(ctx context.Context, milestone string) ([]*gh.Issue, error) {
	vars := map[string]any{"owner": a.owner, "name": a.repo}
	var nodes []gqlIssue
	var err error
	if milestone == "" {
		type data struct {
			Repository struct {
				Issues page[gqlIssue] `json:"issues"`
			} `json:"repository"`
		}
		nodes, err = connection(ctx, a, repoIssuesQuery, vars, func(d *data) *page[gqlIssue] { return &d.Repository.Issues })
	} else {
		n, nerr := number("milestone", milestone)
		if nerr != nil {
			return nil, nerr
		}
		vars["milestone"] = n
		type data struct {
			Repository struct {
				Milestone *struct {
					Issues page[gqlIssue] `json:"issues"`
				} `json:"milestone"`
			} `json:"repository"`
		}
		nodes, err = connection(ctx, a, milestoneIssuesQuery, vars, func(d *data) *page[gqlIssue] {
			if d.Repository.Milestone == nil {
				return nil
			}
			return &d.Repository.Milestone.Issues
		})
	}
	if err != nil {
		return nil, err
	}
	out := make([]*gh.Issue, len(nodes))
	for i, n := range nodes {
		out[i] = n.rest()
	}
	return out, nil
}

const milestonesQuery = `query($owner: String!, $name: String!, $title: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    milestones(first: 100, after: $after, query: $title, states: [OPEN, CLOSED], orderBy: {field: NUMBER, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes { number title description state url }
    }
  }
}`

// MilestonesByTitle implements ports.Tracker: the milestones, open or closed,
// whose title is exactly title (the query of GitHub matches parts of it).
func (a *Adapter) MilestonesByTitle(ctx context.Context, title string) ([]ports.Milestone, error) {
	type node struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Description string `json:"description"`
		State       string `json:"state"`
		URL         string `json:"url"`
	}
	type data struct {
		Repository struct {
			Milestones page[node] `json:"milestones"`
		} `json:"repository"`
	}
	nodes, err := connection(ctx, a, milestonesQuery, map[string]any{"owner": a.owner, "name": a.repo, "title": title},
		func(d *data) *page[node] { return &d.Repository.Milestones })
	if err != nil {
		return nil, a.fail("list milestones titled "+title, err)
	}
	out := []ports.Milestone{}
	for _, m := range nodes {
		if m.Title == title {
			out = append(out, ports.Milestone{Ref: strconv.Itoa(m.Number), Title: m.Title, Description: m.Description, State: strings.ToLower(m.State), URL: m.URL})
		}
	}
	return out, nil
}
