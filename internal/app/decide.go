package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// The decisions of a person outside the plan (v0.1 item 10b): proposals
// (ADR 0020, without the web) and the exit of a blocked node, without the
// automatic triage of v0.2. They take no milestone: a proposal id and an
// issue name their workspace among those of the project on this machine. The
// person is $USER.

// journalsOf are the workspaces of the project on this machine whose
// journal was read.
func journalsOf(ctx context.Context, h Host, p *config.Project, configDir string) ([]machineWorkspace, error) {
	root, err := dataRoot(h.Env)
	if err != nil {
		return nil, err
	}
	all, err := scanWorkspaces(ctx, root)
	if err != nil {
		return nil, err
	}
	own, _, err := projectWorkspaces(h, p, configDir, all)
	return own, err
}

// readJournal runs fn on the journal of the workspace, opened read-only.
func readJournal(ctx context.Context, w machineWorkspace, fn func(*store.DB) error) error {
	db, err := store.OpenReadOnly(ctx, filepath.Join(w.Dir, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return fn(db)
}

// writeJournal runs fn on the journal of the workspace, opened to write.
func writeJournal(ctx context.Context, w machineWorkspace, fn func(*store.DB) error) error {
	db, err := store.Open(ctx, filepath.Join(w.Dir, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return fn(db)
}

// ListProposals lists the proposals of the workspaces of the project
// (kluzo proposal list).
type ListProposals struct{ Host Host }

// ProposalItem is a proposal in the list.
type ProposalItem struct {
	ID        string   `json:"id"`
	Milestone string   `json:"milestone"`
	Issue     string   `json:"issue"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	Changes   []string `json:"changes"` // the reasons, for a change
	CreatedAt string   `json:"created_at"`
	DecidedBy string   `json:"decided_by,omitempty"`
}

// Execute reads the proposals, the pending ones first.
func (u ListProposals) Execute(ctx context.Context, configDir string) ([]ProposalItem, error) {
	h := u.Host.fill()
	p, err := project(configDir)
	if err != nil {
		return nil, err
	}
	ws, err := journalsOf(ctx, h, p, configDir)
	if err != nil {
		return nil, err
	}
	out := []ProposalItem{}
	for _, w := range ws {
		milestone, _ := p.Config.Branches.MilestoneTitle(w.Branch)
		err := readJournal(ctx, w, func(db *store.DB) error {
			ps, err := db.Proposals(ctx)
			if err != nil {
				return err
			}
			snap, err := db.Snapshot(ctx)
			if err != nil {
				return err
			}
			for _, pr := range ps {
				out = append(out, proposalItem(milestone, snap, pr))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b ProposalItem) int {
		return boolOrder(a.State != contracts.ProposalPending, b.State != contracts.ProposalPending)
	})
	return out, nil
}

// proposalItem is a proposal of a workspace in the list.
func proposalItem(milestone string, snap store.Snapshot, pr store.ProposalRow) ProposalItem {
	item := ProposalItem{ID: pr.ID, Milestone: milestone, Kind: pr.Kind, State: pr.State, CreatedAt: pr.CreatedAt, DecidedBy: pr.DecidedBy, Changes: []string{}}
	if i := slices.IndexFunc(snap.Nodes, func(n store.NodeRow) bool { return n.ID == pr.Node }); i >= 0 {
		item.Issue = snap.Nodes[i].IssueRef
	}
	var body core.ChangeProposal
	if json.Unmarshal(pr.Payload, &body) == nil {
		for _, c := range body.Changes {
			item.Changes = append(item.Changes, c.Reason)
		}
	}
	return item
}

// boolOrder sorts false before true.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// DecideProposal approves or rejects a pending proposal
// (kluzo proposal approve|reject).
type DecideProposal struct{ Host Host }

// DecideProposalInput names the proposal and the decision.
type DecideProposalInput struct {
	ConfigDir string
	ID        string
	Approve   bool
}

// Execute records the decision in the workspace of the proposal.
func (u DecideProposal) Execute(ctx context.Context, in DecideProposalInput) error {
	h := u.Host.fill()
	p, err := project(in.ConfigDir)
	if err != nil {
		return err
	}
	ws, err := journalsOf(ctx, h, p, in.ConfigDir)
	if err != nil {
		return err
	}
	for _, w := range ws {
		var has bool
		if err := readJournal(ctx, w, func(db *store.DB) error {
			ps, err := db.Proposals(ctx)
			has = slices.ContainsFunc(ps, func(pr store.ProposalRow) bool { return pr.ID == in.ID })
			return err
		}); err != nil {
			return err
		}
		if has {
			return writeJournal(ctx, w, func(db *store.DB) error {
				return supervisor.DecideProposal(ctx, db, h.IDs, h.Clock, p, in.ID, in.Approve, getenv(h.Env, "USER"))
			})
		}
	}
	return fmt.Errorf("%w has proposal %s", ErrNoWorkspace, in.ID)
}

// NodeAction is what a person does to a blocked node.
type NodeAction string

// The actions of kluzo node.
const (
	NodeRetry   NodeAction = "retry"
	NodeUnblock NodeAction = "unblock"
	NodeCancel  NodeAction = "cancel"
)

// DecideNode applies kluzo node retry|unblock|cancel to the node of an issue,
// in the active workspace of the project that has it.
type DecideNode struct{ Host Host }

// DecideNodeInput names the issue and the action.
type DecideNodeInput struct {
	ConfigDir string
	Issue     string
	Action    NodeAction
}

// Execute records the decision as an input of the core of the node.
func (u DecideNode) Execute(ctx context.Context, in DecideNodeInput) error {
	h := u.Host.fill()
	who := getenv(h.Env, "USER")
	var input core.Input
	switch in.Action {
	case NodeRetry:
		input = core.Retry{By: who}
	case NodeUnblock:
		input = core.Unblock{By: who}
	case NodeCancel:
		input = core.Cancel{By: who}
	default:
		return fmt.Errorf("unknown node action %q", in.Action)
	}
	p, err := project(in.ConfigDir)
	if err != nil {
		return err
	}
	ws, err := journalsOf(ctx, h, p, in.ConfigDir)
	if err != nil {
		return err
	}
	var found []machineWorkspace
	for _, w := range ws {
		if _, ok := w.Nodes[in.Issue]; ok && w.active() {
			found = append(found, w)
		}
	}
	switch len(found) {
	case 0:
		return fmt.Errorf("%w is active with issue #%s", ErrNoWorkspace, in.Issue)
	case 1:
		return writeJournal(ctx, found[0], func(db *store.DB) error {
			return supervisor.DecideNode(ctx, db, h.IDs, h.Clock, p, in.Issue, input)
		})
	}
	return fmt.Errorf("issue #%s is in %d active workspaces", in.Issue, len(found))
}
