package github

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

var (
	// "Depende de: #12, #13" and "Contrato: #14" in the description.
	dependsLine  = regexp.MustCompile(`(?im)^[ \t]*depende de[ \t]*:(.*)$`)
	contractLine = regexp.MustCompile(`(?im)^[ \t]*contrato[ \t]*:(.*)(?:\r?\n|$)`)
	issueRef     = regexp.MustCompile(`#(\d+)`)
)

// Dependencies implements ports.Tracker: the native blocked_by links, then the
// "Depende de:" and "Contrato:" lines of the description.
func (a *Adapter) Dependencies(ctx context.Context, issueRef string) ([]ports.Dependency, error) {
	is, err := a.getIssue(ctx, issueRef)
	if err != nil {
		return nil, err
	}
	blockers, err := a.nativeBlockers(ctx, is.GetNumber())
	if err != nil {
		return nil, a.fail("list dependencies of issue "+issueRef, err)
	}
	var deps []ports.Dependency
	seen := map[string]bool{}
	add := func(from, kind, source string) {
		if from == issueRef || seen[kind+from] {
			return
		}
		seen[kind+from] = true
		deps = append(deps, ports.Dependency{FromRef: from, ToRef: issueRef, Kind: contracts.EdgeKind(kind), Source: source})
	}
	for _, b := range blockers {
		add(strconv.Itoa(b.GetNumber()), string(contracts.EdgeBlocks), ports.SourceNative)
	}
	body := is.GetBody()
	for _, from := range refsIn(dependsLine, body) {
		add(from, string(contracts.EdgeBlocks), ports.SourceDescription)
	}
	for _, from := range refsIn(contractLine, body) {
		add(from, string(contracts.EdgeContract), ports.SourceDescription)
	}
	return deps, nil
}

// refsIn returns the #n references on the lines matched by re.
func refsIn(re *regexp.Regexp, body string) []string {
	var out []string
	for _, line := range re.FindAllStringSubmatch(body, -1) {
		for _, m := range issueRef.FindAllStringSubmatch(line[1], -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// nativeBlockers lists the issues that block issue, through the issue
// dependencies API.
func (a *Adapter) nativeBlockers(ctx context.Context, issue int) ([]*gh.Issue, error) {
	opts := &gh.ListOptions{PerPage: pageSize}
	var out []*gh.Issue
	err := pages(func(page int) (int, error) {
		opts.Page = page
		batch, resp, err := a.gh.Issues.ListBlockedBy(ctx, a.owner, a.repo, int64(issue), opts)
		out = append(out, batch...)
		return nextPage(resp), err
	})
	return out, err
}

// SetDependencies implements ports.Tracker. Blocking edges become native
// links, added and removed to match; contract edges become the "Contrato:" line.
func (a *Adapter) SetDependencies(ctx context.Context, issueRef string, deps []ports.Dependency) error {
	is, err := a.getIssue(ctx, issueRef)
	if err != nil {
		return err
	}
	var blocks, contractRefs []string
	for _, d := range deps {
		switch {
		case d.ToRef != issueRef || d.FromRef == issueRef:
			return a.fail("set dependencies", fmt.Errorf("dependency %s -> %s of issue %s: %w", d.FromRef, d.ToRef, issueRef, ports.ErrInvalid))
		case d.Kind == contracts.EdgeBlocks:
			blocks = appendUnique(blocks, d.FromRef)
		case d.Kind == contracts.EdgeContract:
			contractRefs = appendUnique(contractRefs, d.FromRef)
		default:
			return a.fail("set dependencies", fmt.Errorf("dependency kind %q: %w", d.Kind, ports.ErrInvalid))
		}
	}
	if err := a.syncBlockers(ctx, is.GetNumber(), blocks); err != nil {
		return err
	}
	return a.syncContractLine(ctx, is, contractRefs)
}

// syncBlockers makes the native blockers of issue exactly the wanted refs.
// Every wanted issue is looked up before anything is written, so an unknown
// one leaves the links as they were.
func (a *Adapter) syncBlockers(ctx context.Context, issue int, wanted []string) error {
	current, err := a.nativeBlockers(ctx, issue)
	if err != nil {
		return a.fail("list dependencies", err)
	}
	have := map[string]*gh.Issue{}
	for _, b := range current {
		have[strconv.Itoa(b.GetNumber())] = b
	}
	var add []*gh.Issue
	for _, ref := range wanted {
		if have[ref] != nil {
			continue
		}
		blocker, err := a.getIssue(ctx, ref)
		if err != nil {
			return err
		}
		add = append(add, blocker)
	}
	for ref, b := range have {
		if !slices.Contains(wanted, ref) {
			if _, _, err := a.gh.Issues.RemoveBlockedBy(ctx, a.owner, a.repo, int64(issue), b.GetID()); err != nil {
				return a.fail("remove dependency "+ref, err)
			}
		}
	}
	for _, b := range add {
		if _, _, err := a.gh.Issues.AddBlockedBy(ctx, a.owner, a.repo, int64(issue), gh.IssueDependencyRequest{IssueID: b.GetID()}); err != nil {
			return a.fail("add dependency "+strconv.Itoa(b.GetNumber()), err)
		}
	}
	return nil
}

// syncContractLine rewrites the "Contrato:" line of the description to list refs.
func (a *Adapter) syncContractLine(ctx context.Context, is *gh.Issue, refs []string) error {
	clean, key := splitMarker(is.GetBody())
	updated := setContractLine(clean, refs)
	if updated == clean {
		return nil
	}
	if key != "" {
		updated = withMarker(updated, key)
	}
	if _, _, err := a.gh.Issues.Update(ctx, a.owner, a.repo, is.GetNumber(), gh.UpdateIssueRequest{Body: &updated}); err != nil {
		return a.fail("update the Contrato line of issue "+strconv.Itoa(is.GetNumber()), err)
	}
	return nil
}

// setContractLine replaces the "Contrato:" lines of body by one listing refs,
// or removes them when refs is empty. It returns body itself when there is
// nothing to change, and it is idempotent.
func setContractLine(body string, refs []string) string {
	if !contractLine.MatchString(body) && len(refs) == 0 {
		return body
	}
	base := strings.TrimRight(contractLine.ReplaceAllString(body, ""), " \t\r\n")
	if len(refs) == 0 {
		return base
	}
	marks := make([]string, len(refs))
	for i, r := range refs {
		marks[i] = "#" + r
	}
	line := "Contrato: " + strings.Join(marks, ", ")
	if base == "" {
		return line
	}
	return base + "\n\n" + line
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
