package ports

import (
	"context"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// StateLabelPrefix starts every label the Kluzo owns (docs/contracts/state-machine.md).
const StateLabelPrefix = "orch/"

// Tracker reads milestones and issues and mirrors the Kluzo state into them.
// Every write is idempotent: the idemKey (or the content itself) makes a repeat
// a no-op.
type Tracker interface {
	Milestone(ctx context.Context, ref string) (Milestone, error)
	// MilestonesByTitle lists the milestones, open or closed, whose title is
	// exactly title, oldest first; none is an empty list, not an error.
	MilestonesByTitle(ctx context.Context, title string) ([]Milestone, error)
	MilestoneIssues(ctx context.Context, ref string) ([]Issue, error)
	Issue(ctx context.Context, ref string) (Issue, error)

	// Dependencies returns what blocks the issue: the tracker's native links
	// plus the "Depende de:" / "Contrato:" lines of the description. Every
	// Dependency has ToRef == issueRef.
	Dependencies(ctx context.Context, issueRef string) ([]Dependency, error)
	// SetDependencies makes deps the dependencies of the issue: blocking edges
	// become native links and contract edges the "Contrato:" line of the
	// description (rewritten, or removed when there are none). "Depende de:"
	// lines are only read, never written.
	SetDependencies(ctx context.Context, issueRef string, deps []Dependency) error

	CreateIssue(ctx context.Context, in NewIssue, idemKey string) (Issue, error)

	// SetStateLabel replaces the orch/* label of the issue by label (at most
	// one orch/* per issue). It fails with ErrInvalid if label is not orch/*.
	SetStateLabel(ctx context.Context, issueRef string, label string) error
	// AddLabels adds labels that are not orch/* (those go through SetStateLabel).
	AddLabels(ctx context.Context, issueRef string, labels []string) error

	// Comment posts body once per idemKey: a repeat returns the first comment.
	Comment(ctx context.Context, issueRef string, body string, idemKey string) (CommentRef, error)
	// CommentsSince returns the comments after the cursor, oldest first, and
	// the new cursor. An empty cursor returns every comment.
	CommentsSince(ctx context.Context, issueRef string, cursor string) ([]Comment, string, error)

	// Changes returns the label changes of the milestone's issues after the
	// cursor, oldest first, and the new cursor. An empty cursor returns no
	// changes and a cursor positioned at the present, so polling starts "now".
	Changes(ctx context.Context, milestoneRef string, cursor string) ([]Change, string, error)

	// Whoami returns the login Kluzo acts as in the tracker (the bot of an App
	// or the user of a token). It is the Actor of the changes Kluzo causes, so
	// the supervisor can revert orch/* labels applied by anyone else.
	Whoami(ctx context.Context) (string, error)
}

// Milestone is a tracker milestone.
type Milestone struct {
	Ref, Title, Description string
	State                   string // "open" | "closed"
	URL                     string
}

// Issue states.
const (
	IssueOpen   = "open"
	IssueClosed = "closed"
)

// Issue is a tracker issue. Body is the text a human wrote: markers the
// adapter hides in it are removed.
type Issue struct {
	Ref, Title, Body, State string // State: IssueOpen | IssueClosed
	Labels                  []string
	MilestoneRef            string
	URL                     string
}

// NewIssue describes an issue to create.
type NewIssue struct {
	Title, Body  string
	Labels       []string
	MilestoneRef string
}

// Dependency says FromRef must come before ToRef.
type Dependency struct {
	FromRef, ToRef string
	Kind           contracts.EdgeKind // EdgeBlocks | EdgeContract
	Source         string             // "native" | "description"
}

// Where a Dependency was read from.
const (
	SourceNative      = "native"
	SourceDescription = "description"
)

// CommentRef identifies a posted comment.
type CommentRef struct{ ID, URL string }

// Comment is an issue comment; Body is the text without hidden markers.
type Comment struct {
	ID, Author, Body string
	CreatedAt        time.Time
}

// Change kinds.
const (
	ChangeLabeled   = "labeled"
	ChangeUnlabeled = "unlabeled"
)

// Change is one label event on an issue of the milestone.
type Change struct {
	Kind     string // ChangeLabeled | ChangeUnlabeled
	IssueRef string
	Label    string
	Actor    string // who did it; the Kluzo reverts orch/* labels set by others
	At       time.Time
}
