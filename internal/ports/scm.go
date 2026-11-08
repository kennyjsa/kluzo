package ports

import "context"

// SCM manages remote branches and pull requests. Merging into the main branch
// is human: this port only observes it (ADR 0003).
type SCM interface {
	// EnsureBranch creates the branch at fromRef (a branch name or a commit
	// sha) if it does not exist, and returns its head. An existing branch is
	// returned as it is.
	EnsureBranch(ctx context.Context, name, fromRef string) (sha string, err error)
	BranchHead(ctx context.Context, name string) (sha string, err error)

	// OpenPR opens the PR for in.Head, or returns the one already open for the
	// same head and base. It fails with ErrInvalid if the branches do not differ.
	OpenPR(ctx context.Context, in NewPR, idemKey string) (PR, error)
	UpdatePR(ctx context.Context, ref string, body string) error
	PR(ctx context.Context, ref string) (PR, error)

	// IsMerged reports whether the PR was merged; it never merges.
	IsMerged(ctx context.Context, prRef string) (bool, error)
}

// NewPR describes a pull request to open.
type NewPR struct{ Title, Body, Head, Base string }

// PR is a pull request. Body is the text without hidden markers.
type PR struct {
	Ref, Title, Body string
	Head, Base       string // branch names
	HeadSHA          string
	State            string // "open" | "closed"
	Merged           bool
	URL              string
}
