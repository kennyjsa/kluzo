package github

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v75/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

var (
	// "Depende de: #12, #13" and "Contrato: #14" in the description.
	dependsLine  = regexp.MustCompile(`(?im)^[ \t]*depende de[ \t]*:(.*)$`)
	contractLine = regexp.MustCompile(`(?im)^[ \t]*contrato[ \t]*:(.*)(?:\r?\n|$)`)
	issueRef     = regexp.MustCompile(`#(\d+)`)
)

// dependenciesPath is the REST path of an issue's native "blocked by" links.
func (a *Adapter) dependenciesPath(issue int) string {
	return fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/blocked_by", a.owner, a.repo, issue)
}

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
		deps = append(deps, ports.Dependency{FromRef: from, ToRef: issueRef, Kind: ports.EdgeKind(kind), Source: source})
	}
	for _, b := range blockers {
		add(strconv.Itoa(b.GetNumber()), string(ports.EdgeBlocks), ports.SourceNative)
	}
	body := is.GetBody()
	for _, from := range refsIn(dependsLine, body) {
		add(from, string(ports.EdgeBlocks), ports.SourceDescription)
	}
	for _, from := range refsIn(contractLine, body) {
		add(from, string(ports.EdgeContract), ports.SourceDescription)
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
// dependencies API (not wrapped by go-github).
func (a *Adapter) nativeBlockers(ctx context.Context, issue int) ([]*gh.Issue, error) {
	var out []*gh.Issue
	err := pages(func(page int) (int, error) {
		req, err := a.gh.NewRequest(http.MethodGet, fmt.Sprintf("%s?per_page=%d&page=%d", a.dependenciesPath(issue), pageSize, page), nil)
		if err != nil {
			return 0, err
		}
		var batch []*gh.Issue
		resp, err := a.gh.Do(ctx, req, &batch)
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
	var blocks, contracts []string
	for _, d := range deps {
		switch {
		case d.ToRef != issueRef || d.FromRef == issueRef:
			return a.fail("set dependencies", fmt.Errorf("dependency %s -> %s of issue %s: %w", d.FromRef, d.ToRef, issueRef, ports.ErrInvalid))
		case d.Kind == ports.EdgeBlocks:
			blocks = appendUnique(blocks, d.FromRef)
		case d.Kind == ports.EdgeContract:
			contracts = appendUnique(contracts, d.FromRef)
		default:
			return a.fail("set dependencies", fmt.Errorf("dependency kind %q: %w", d.Kind, ports.ErrInvalid))
		}
	}
	if err := a.syncBlockers(ctx, is.GetNumber(), blocks); err != nil {
		return err
	}
	return a.syncContractLine(ctx, is, contracts)
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
			if err := a.writeBlocker(ctx, http.MethodDelete, a.dependenciesPath(issue)+"/"+strconv.FormatInt(b.GetID(), 10), nil); err != nil {
				return a.fail("remove dependency "+ref, err)
			}
		}
	}
	for _, b := range add {
		body := map[string]int64{"issue_id": b.GetID()}
		if err := a.writeBlocker(ctx, http.MethodPost, a.dependenciesPath(issue), body); err != nil {
			return a.fail("add dependency "+strconv.Itoa(b.GetNumber()), err)
		}
	}
	return nil
}

func (a *Adapter) writeBlocker(ctx context.Context, method, path string, body any) error {
	req, err := a.gh.NewRequest(method, path, body)
	if err != nil {
		return err
	}
	_, err = a.gh.Do(ctx, req, nil)
	return err
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
	if _, _, err := a.gh.Issues.Edit(ctx, a.owner, a.repo, is.GetNumber(), &gh.IssueRequest{Body: &updated}); err != nil {
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
