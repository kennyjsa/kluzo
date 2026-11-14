// Package fakes holds in-memory implementations of the ports with the behavior
// of the contract: idempotent writes, one orch/* label per issue, cursors. They
// pass the same suite as the real adapters (ports/contracttest).
package fakes

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Actor is the user name the fake tracker records on the events it causes.
const Actor = "kluzo-bot"

// Tracker is an in-memory ports.Tracker.
type Tracker struct {
	mu         sync.Mutex
	clock      clock.Clock
	milestones map[string]ports.Milestone
	issues     map[string]*issue
	nextIssue  int
	nextID     int // comments and events share one counter, like GitHub ids
	events     []event
}

type issue struct {
	ports.Issue
	createKey  string
	blockedBy  []string // refs, in the order they were set
	contractBy []string
	comments   []comment
	idemKeys   map[string]ports.CommentRef
}

type comment struct {
	id   int
	body string
	at   time.Time
}

type event struct {
	id        int
	milestone string
	change    ports.Change
}

var _ ports.Tracker = (*Tracker)(nil)

// NewTracker returns an empty tracker that stamps its events with clk.
func NewTracker(clk clock.Clock) *Tracker {
	return &Tracker{clock: clk, milestones: map[string]ports.Milestone{}, issues: map[string]*issue{}}
}

// AddMilestone creates an open milestone and returns its ref.
func (t *Tracker) AddMilestone(title, description string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref := strconv.Itoa(len(t.milestones) + 1)
	t.milestones[ref] = ports.Milestone{Ref: ref, Title: title, Description: description, State: "open", URL: "fake://milestone/" + ref}
	return ref
}

// Whoami implements ports.Tracker.
func (t *Tracker) Whoami(context.Context) (string, error) { return Actor, nil }

// Milestone implements ports.Tracker.
func (t *Tracker) Milestone(_ context.Context, ref string) (ports.Milestone, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.milestones[ref]
	if !ok {
		return m, fmt.Errorf("milestone %s: %w", ref, ports.ErrNotFound)
	}
	return m, nil
}

// MilestonesByTitle implements ports.Tracker.
func (t *Tracker) MilestonesByTitle(_ context.Context, title string) ([]ports.Milestone, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []ports.Milestone{}
	for i := 1; i <= len(t.milestones); i++ {
		if m := t.milestones[strconv.Itoa(i)]; m.Title == title {
			out = append(out, m)
		}
	}
	return out, nil
}

// MilestoneIssues implements ports.Tracker.
func (t *Tracker) MilestoneIssues(_ context.Context, ref string) ([]ports.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.milestones[ref]; !ok {
		return nil, fmt.Errorf("milestone %s: %w", ref, ports.ErrNotFound)
	}
	var out []ports.Issue
	for _, is := range t.sortedIssues() {
		if is.MilestoneRef == ref {
			out = append(out, is.snapshot())
		}
	}
	return out, nil
}

// Issue implements ports.Tracker.
func (t *Tracker) Issue(_ context.Context, ref string) (ports.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(ref)
	if err != nil {
		return ports.Issue{}, err
	}
	return is.snapshot(), nil
}

// Dependencies implements ports.Tracker.
func (t *Tracker) Dependencies(_ context.Context, issueRef string) ([]ports.Dependency, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(issueRef)
	if err != nil {
		return nil, err
	}
	var deps []ports.Dependency
	for _, from := range is.blockedBy {
		deps = append(deps, ports.Dependency{FromRef: from, ToRef: issueRef, Kind: contracts.EdgeBlocks, Source: ports.SourceNative})
	}
	for _, from := range is.contractBy {
		deps = append(deps, ports.Dependency{FromRef: from, ToRef: issueRef, Kind: contracts.EdgeContract, Source: ports.SourceDescription})
	}
	return deps, nil
}

// SetDependencies implements ports.Tracker.
func (t *Tracker) SetDependencies(_ context.Context, issueRef string, deps []ports.Dependency) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(issueRef)
	if err != nil {
		return err
	}
	var blocks, contractRefs []string
	for _, d := range deps {
		switch {
		case d.ToRef != issueRef || d.FromRef == issueRef:
			return fmt.Errorf("dependency %s -> %s of issue %s: %w", d.FromRef, d.ToRef, issueRef, ports.ErrInvalid)
		case d.Kind != contracts.EdgeBlocks && d.Kind != contracts.EdgeContract:
			return fmt.Errorf("dependency kind %q: %w", d.Kind, ports.ErrInvalid)
		}
		if _, err := t.find(d.FromRef); err != nil {
			return err
		}
		if d.Kind == contracts.EdgeBlocks {
			blocks = appendUnique(blocks, d.FromRef)
		} else {
			contractRefs = appendUnique(contractRefs, d.FromRef)
		}
	}
	is.blockedBy, is.contractBy = blocks, contractRefs
	return nil
}

// CreateIssue implements ports.Tracker.
func (t *Tracker) CreateIssue(_ context.Context, in ports.NewIssue, idemKey string) (ports.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if idemKey == "" {
		return ports.Issue{}, fmt.Errorf("create issue: empty idempotency key: %w", ports.ErrInvalid)
	}
	for _, is := range t.sortedIssues() {
		if is.createKey == idemKey {
			return is.snapshot(), nil
		}
	}
	if in.MilestoneRef != "" {
		if _, ok := t.milestones[in.MilestoneRef]; !ok {
			return ports.Issue{}, fmt.Errorf("milestone %s: %w", in.MilestoneRef, ports.ErrNotFound)
		}
	}
	for _, l := range in.Labels {
		if strings.HasPrefix(l, ports.StateLabelPrefix) {
			return ports.Issue{}, fmt.Errorf("label %q at creation: %w", l, ports.ErrInvalid)
		}
	}
	t.nextIssue++
	ref := strconv.Itoa(t.nextIssue)
	is := &issue{
		Issue:    ports.Issue{Ref: ref, Title: in.Title, Body: in.Body, State: "open", Labels: slices.Clone(in.Labels), MilestoneRef: in.MilestoneRef, URL: "fake://issue/" + ref},
		idemKeys: map[string]ports.CommentRef{},
	}
	is.createKey = idemKey
	t.issues[ref] = is
	return is.snapshot(), nil
}

// SetStateLabel implements ports.Tracker.
func (t *Tracker) SetStateLabel(_ context.Context, issueRef, label string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !strings.HasPrefix(label, ports.StateLabelPrefix) {
		return fmt.Errorf("state label %q must start with %s: %w", label, ports.StateLabelPrefix, ports.ErrInvalid)
	}
	is, err := t.find(issueRef)
	if err != nil {
		return err
	}
	for _, old := range slices.Clone(is.Labels) {
		if strings.HasPrefix(old, ports.StateLabelPrefix) && old != label {
			t.removeLabel(is, old)
		}
	}
	t.addLabel(is, label)
	return nil
}

// AddLabels implements ports.Tracker.
func (t *Tracker) AddLabels(_ context.Context, issueRef string, labels []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(issueRef)
	if err != nil {
		return err
	}
	for _, l := range labels {
		if strings.HasPrefix(l, ports.StateLabelPrefix) {
			return fmt.Errorf("label %q is an orch/* label, use SetStateLabel: %w", l, ports.ErrInvalid)
		}
	}
	for _, l := range labels {
		t.addLabel(is, l)
	}
	return nil
}

// Comment implements ports.Tracker.
func (t *Tracker) Comment(_ context.Context, issueRef, body, idemKey string) (ports.CommentRef, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(issueRef)
	if err != nil {
		return ports.CommentRef{}, err
	}
	if idemKey == "" {
		return ports.CommentRef{}, fmt.Errorf("comment: empty idempotency key: %w", ports.ErrInvalid)
	}
	if ref, ok := is.idemKeys[idemKey]; ok {
		return ref, nil
	}
	t.nextID++
	is.comments = append(is.comments, comment{id: t.nextID, body: body, at: t.clock.Now()})
	ref := ports.CommentRef{ID: strconv.Itoa(t.nextID), URL: fmt.Sprintf("fake://issue/%s#comment-%d", issueRef, t.nextID)}
	is.idemKeys[idemKey] = ref
	return ref, nil
}

// CommentsSince implements ports.Tracker.
func (t *Tracker) CommentsSince(_ context.Context, issueRef, cursor string) ([]ports.Comment, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	is, err := t.find(issueRef)
	if err != nil {
		return nil, "", err
	}
	after, err := parseCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	next := after
	var out []ports.Comment
	for _, c := range is.comments {
		if c.id > after {
			out = append(out, ports.Comment{ID: strconv.Itoa(c.id), Author: Actor, Body: c.body, CreatedAt: c.at})
			next = c.id
		}
	}
	return out, strconv.Itoa(next), nil
}

// Changes implements ports.Tracker.
func (t *Tracker) Changes(_ context.Context, milestoneRef, cursor string) ([]ports.Change, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.milestones[milestoneRef]; !ok {
		return nil, "", fmt.Errorf("milestone %s: %w", milestoneRef, ports.ErrNotFound)
	}
	head := 0
	if len(t.events) > 0 {
		head = t.events[len(t.events)-1].id
	}
	if cursor == "" {
		return nil, strconv.Itoa(head), nil
	}
	after, err := parseCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	var out []ports.Change
	for _, e := range t.events {
		if e.id > after && e.milestone == milestoneRef {
			out = append(out, e.change)
		}
	}
	return out, strconv.Itoa(head), nil
}

// snapshot returns the public Issue, without sharing the label slice.
func (is *issue) snapshot() ports.Issue {
	out := is.Issue
	out.Labels = slices.Clone(is.Labels)
	return out
}

func (t *Tracker) find(ref string) (*issue, error) {
	is, ok := t.issues[ref]
	if !ok {
		return nil, fmt.Errorf("issue %s: %w", ref, ports.ErrNotFound)
	}
	return is, nil
}

func (t *Tracker) sortedIssues() []*issue {
	out := make([]*issue, 0, len(t.issues))
	for i := 1; i <= t.nextIssue; i++ {
		out = append(out, t.issues[strconv.Itoa(i)])
	}
	return out
}

func (t *Tracker) addLabel(is *issue, label string) {
	if slices.Contains(is.Labels, label) {
		return
	}
	is.Labels = append(is.Labels, label)
	t.record(is, ports.ChangeLabeled, label)
}

func (t *Tracker) removeLabel(is *issue, label string) {
	is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return l == label })
	t.record(is, ports.ChangeUnlabeled, label)
}

func (t *Tracker) record(is *issue, kind, label string) {
	t.nextID++
	t.events = append(t.events, event{
		id: t.nextID, milestone: is.MilestoneRef,
		change: ports.Change{Kind: kind, IssueRef: is.Ref, Label: label, Actor: Actor, At: t.clock.Now()},
	})
}

func parseCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(c)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cursor %q: %w", c, ports.ErrInvalid)
	}
	return n, nil
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
