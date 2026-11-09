package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// The decisions of a person outside the plan (v0.1 item 10b): proposals
// (ADR 0020, without the web) and the exit of a blocked node, without the
// automatic triage of v0.2. They take no milestone: a proposal id and an
// issue name their workspace among those of the project on this machine.

// projectWorkspace is an open journal of a workspace of the project.
type projectWorkspace struct {
	name string // the directory, without the project prefix: the milestone
	db   *store.DB
	snap store.Snapshot
}

// workspacesOf opens the journals of the workspaces of the project; close
// closes them all.
func workspacesOf(ctx context.Context, h Host, p *config.Project, configDir string) (ws []projectWorkspace, close func(), err error) {
	close = func() {
		for _, w := range ws {
			_ = w.db.Close()
		}
	}
	root, err := dataRoot(h.Env)
	if err != nil {
		return nil, close, err
	}
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return nil, close, err
	}
	prefix := projectName(p, abs) + "-"
	dirs, err := filepath.Glob(filepath.Join(root, "workspaces", prefix+"*", "state.db"))
	if err != nil {
		return nil, close, err
	}
	for _, path := range dirs {
		db, err := store.Open(ctx, path)
		if err != nil {
			return ws, close, err
		}
		snap, err := db.Snapshot(ctx)
		if err != nil || snap.Workspace == nil {
			_ = db.Close()
			if err != nil {
				return ws, close, err
			}
			continue
		}
		ws = append(ws, projectWorkspace{name: strings.TrimPrefix(filepath.Base(filepath.Dir(path)), prefix), db: db, snap: snap})
	}
	return ws, close, nil
}

// by is who decides: the name given, or $USER.
func by(h Host, name string) string {
	if name != "" {
		return name
	}
	return getenv(h.Env, "USER")
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
	ws, closeAll, err := workspacesOf(ctx, h, p, configDir)
	defer closeAll()
	if err != nil {
		return nil, err
	}
	out := []ProposalItem{}
	for _, w := range ws {
		ps, err := w.db.Proposals(ctx)
		if err != nil {
			return nil, err
		}
		for _, pr := range ps {
			item := ProposalItem{ID: pr.ID, Milestone: w.name, Kind: pr.Kind, State: pr.State, CreatedAt: pr.CreatedAt, DecidedBy: pr.DecidedBy, Changes: []string{}}
			if i := slices.IndexFunc(w.snap.Nodes, func(n store.NodeRow) bool { return n.ID == pr.Node }); i >= 0 {
				item.Issue = w.snap.Nodes[i].IssueRef
			}
			var body struct {
				Changes []core.ProposalChange `json:"changes"`
			}
			if json.Unmarshal(pr.Payload, &body) == nil {
				for _, c := range body.Changes {
					item.Changes = append(item.Changes, c.Reason)
				}
			}
			out = append(out, item)
		}
	}
	slices.SortStableFunc(out, func(a, b ProposalItem) int {
		return boolOrder(a.State != contracts.ProposalPending, b.State != contracts.ProposalPending)
	})
	return out, nil
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
	By        string // empty: $USER
}

// Execute records the decision in the workspace of the proposal.
func (u DecideProposal) Execute(ctx context.Context, in DecideProposalInput) error {
	h := u.Host.fill()
	p, err := project(in.ConfigDir)
	if err != nil {
		return err
	}
	ws, closeAll, err := workspacesOf(ctx, h, p, in.ConfigDir)
	defer closeAll()
	if err != nil {
		return err
	}
	for _, w := range ws {
		ps, err := w.db.Proposals(ctx)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(ps, func(pr store.ProposalRow) bool { return pr.ID == in.ID }) {
			return supervisor.DecideProposal(ctx, w.db, h.IDs, h.Clock, p, in.ID, in.Approve, by(h, in.By))
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
	By        string // empty: $USER
}

// Execute records the decision as an input of the core of the node.
func (u DecideNode) Execute(ctx context.Context, in DecideNodeInput) error {
	h := u.Host.fill()
	who := by(h, in.By)
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
	ws, closeAll, err := workspacesOf(ctx, h, p, in.ConfigDir)
	defer closeAll()
	if err != nil {
		return err
	}
	var found []projectWorkspace
	for _, w := range ws {
		if w.snap.Workspace.State.Active() && slices.ContainsFunc(w.snap.Nodes, func(n store.NodeRow) bool { return n.IssueRef == in.Issue }) {
			found = append(found, w)
		}
	}
	switch len(found) {
	case 0:
		return fmt.Errorf("%w is active with issue #%s", ErrNoWorkspace, in.Issue)
	case 1:
		return supervisor.DecideNode(ctx, found[0].db, h.IDs, h.Clock, p, in.Issue, input)
	}
	return fmt.Errorf("issue #%s is in %d active workspaces", in.Issue, len(found))
}
