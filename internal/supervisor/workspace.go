package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
)

// The operations of a person on a workspace, from outside the loop of the
// supervisor (the CLI): create it from a plan, approve the plan, pause and
// resume. Each is one transaction with the effects of its move, so the loop
// only has to run them.

// journal is a supervisor reduced to what writes the journal: the store, the
// clock and the ids.
func journal(db *store.DB, gen *ids.Generator, clk clock.Clock) *Supervisor {
	return &Supervisor{d: Deps{DB: db, IDs: gen, Clock: clk}}
}

// MoveWorkspace applies a workspace trigger from outside the loop of the
// supervisor (the pause and the resume of a person), with the effects of the
// move, in one transaction.
func MoveWorkspace(ctx context.Context, db *store.DB, gen *ids.Generator, clk clock.Clock, trigger core.WorkspaceTrigger) error {
	return journal(db, gen, clk).moveWorkspace(ctx, trigger)
}

// NewWorkspace is a workspace created from an approved-to-be plan.
type NewWorkspace struct {
	Created contracts.WorkspaceCreated
	Nodes   []NewNode
	// Edges join issue references of Nodes.
	Edges []core.Edge
}

// NewNode is a node of a new workspace.
type NewNode struct {
	ID    string // nd_<ulid>
	Added contracts.NodeAdded
}

// CreateWorkspace journals a workspace created with its plan (kluzo plan):
// workspace.created, the move to awaiting_approval with git.milestone, the
// nodes and the edges, in one transaction.
func CreateWorkspace(ctx context.Context, db *store.DB, gen *ids.Generator, clk clock.Clock, w NewWorkspace) error {
	s := journal(db, gen, clk)
	byRef := map[string]string{}
	for _, n := range w.Nodes {
		byRef[n.Added.IssueRef] = n.ID
	}
	return db.Tx(ctx, func(t *store.Tx) error {
		appendEvent := func(kind, node string, payload any) error {
			h, err := s.header()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(payload) // plain data
			_, err = t.Append(store.Event{Header: h, Kind: kind, NodeID: node, Payload: body})
			return err
		}
		if err := appendEvent(contracts.EventWorkspaceCreated, "", w.Created); err != nil {
			return err
		}
		h, err := s.header()
		if err != nil {
			return err
		}
		if err := s.transitionWorkspace(t, core.TriggerCreateWithPlan, h); err != nil {
			return err
		}
		for _, n := range w.Nodes {
			if err := appendEvent(contracts.EventNodeAdded, n.ID, n.Added); err != nil {
				return err
			}
		}
		for _, e := range w.Edges {
			from, to := byRef[e.From], byRef[e.To]
			if from == "" || to == "" {
				return fmt.Errorf("edge %s → %s: a node is not in the plan", e.From, e.To)
			}
			if err := appendEvent(contracts.EventEdgeAdded, "", contracts.EdgeAdded{From: from, To: to, Kind: string(e.Kind), Source: "plan"}); err != nil {
				return err
			}
		}
		return nil
	})
}

// PlanApproval is the approval of the plan of a workspace by a person.
type PlanApproval struct {
	Approved contracts.PlanApproved
	Path     string // the plan file, relative to the repository
	Content  []byte // the plan as written; approved_by and approved_at are added to it
}

// ApprovePlan journals plan.approved with the commit of the plan on the
// milestone branch (git.plan), and moves the workspace to approved, in one
// transaction (kluzo approve). The loop starts the workspace once the
// milestone branch is ready.
func ApprovePlan(ctx context.Context, db *store.DB, gen *ids.Generator, clk clock.Clock, a PlanApproval) error {
	s := journal(db, gen, clk)
	return db.Tx(ctx, func(t *store.Tx) error {
		h, err := s.header()
		if err != nil {
			return err
		}
		// The committed plan says who approved it and when, as plan.approved does.
		content := config.StampPlan(a.Content, a.Approved.By, h.TS)
		specs, err := s.effectSpecs(h, "", "", []core.Effect{{Kind: core.EffectCommitPlan, Key: "plan", Path: a.Path, Body: string(content),
			Title: fmt.Sprintf("docs(plan): approve plan v%d", a.Approved.PlanVersion)}})
		if err != nil {
			return err
		}
		body, _ := json.Marshal(a.Approved) // plain data
		if _, err := t.Append(store.Event{Header: h, Kind: contracts.EventPlanApproved, Payload: body}, specs...); err != nil {
			return err
		}
		th, err := s.header()
		if err != nil {
			return err
		}
		return s.transitionWorkspace(t, core.TriggerPlanApproved, th)
	})
}

// commitPlan runs git.plan: the approved plan, committed on the milestone
// branch and pushed.
func (s *Supervisor) commitPlan(ctx context.Context, p effectPayload) error {
	if _, err := s.syncMilestone(ctx); err != nil {
		return err
	}
	if _, err := s.d.Git.CommitFile(ctx, s.cfg.Repo, s.cfg.Branch, p.Do.Path, []byte(p.Do.Body), p.Do.Title); err != nil {
		return err
	}
	return s.push(ctx, branchRef(s.cfg.Branch)+":"+branchRef(s.cfg.Branch))
}

// person is a supervisor reduced to what decides on a node from outside the
// loop: the journal, the project and its node flow.
func person(db *store.DB, gen *ids.Generator, clk clock.Clock, project *config.Project) (*Supervisor, error) {
	flow, err := nodeFlow(project)
	if err != nil {
		return nil, err
	}
	s := journal(db, gen, clk)
	s.cfg.Project, s.flow = project, flow
	return s, nil
}

// nodeByIssue reads the node of an issue.
func nodeByIssue(snap store.Snapshot, issue string) (store.NodeRow, error) {
	for _, n := range snap.Nodes {
		if n.IssueRef == issue {
			return n, nil
		}
	}
	return store.NodeRow{}, fmt.Errorf("%w: no node for issue #%s", store.ErrNotFound, issue)
}

// DecideNode applies the decision of a person on the node of an issue
// (kluzo node retry, unblock, cancel), in one transaction with the moves,
// their labels and comments, and the work it queues. A node is cancelled only
// when every node that depends on it is cancelled already.
func DecideNode(ctx context.Context, db *store.DB, gen *ids.Generator, clk clock.Clock, project *config.Project, issue string, in core.Input) error {
	s, err := person(db, gen, clk, project)
	if err != nil {
		return err
	}
	snap, err := db.Snapshot(ctx)
	if err != nil {
		return err
	}
	n, err := nodeByIssue(snap, issue)
	if err != nil {
		return err
	}
	var extra []store.Event
	if c, ok := in.(core.Cancel); ok {
		if active := activeDependents(snap, n.ID); len(active) > 0 {
			return fmt.Errorf("%w: cancel first the nodes that depend on #%s: #%s", core.ErrInvalidTransition, issue, strings.Join(active, ", #"))
		}
		// The proposal the node was blocked for goes with it.
		if extra, err = rejectPending(ctx, db, n.ID, c.By); err != nil {
			return err
		}
	}
	return s.decideRetrying(ctx, n, in, nil, true, extra...)
}

// rejectPending are the proposal.decided that reject the pending proposals of
// a node.
func rejectPending(ctx context.Context, db *store.DB, node, by string) ([]store.Event, error) {
	ps, err := db.Proposals(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Event
	for _, p := range ps {
		if p.Node == node && p.State == contracts.ProposalPending {
			out = append(out, decided(p, contracts.ProposalRejected, by))
		}
	}
	return out, nil
}

// decided is the proposal.decided of a proposal.
func decided(p store.ProposalRow, state, by string) store.Event {
	body, _ := json.Marshal(contracts.ProposalDecided{ProposalID: p.ID, Kind: p.Kind, State: state, By: by}) // plain data
	return store.Event{Kind: contracts.EventProposalDecided, Payload: body}
}

// slotFree refuses to leave the node in state to while another node holds
// what it needs: the work slot (max_parallel is 1 in v0.1) to run, review or
// gate, or the merge queue to integrate. The scheduler would never have given
// it; the person tries again when the other node leaves. It runs in the
// transaction of the decision, so a dispatch of the loop cannot slip in.
func slotFree(nodes []store.NodeSlot, node string, to core.NodeState) error {
	work := []core.NodeState{core.NodeRunning, core.NodeInReview, core.NodeGating}
	var holds []core.NodeState
	switch {
	case slices.Contains(work, to):
		holds = work
	case to == core.NodeIntegrating:
		holds = []core.NodeState{core.NodeIntegrating}
	default:
		return nil
	}
	for _, o := range nodes {
		if o.ID != node && slices.Contains(holds, o.State) {
			return fmt.Errorf("%w: #%s is %s; try again when it leaves", core.ErrInvalidTransition, o.Issue, o.State)
		}
	}
	return nil
}

// activeDependents are the issues of the nodes that depend on the node and
// are not final (integrated, failed or cancelled).
func activeDependents(snap store.Snapshot, node string) []string {
	byID := map[string]store.NodeRow{}
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	var out []string
	for _, e := range snap.Edges {
		if d := byID[e.To]; e.From == node && !d.State.Final() && !slices.Contains(out, d.IssueRef) {
			out = append(out, d.IssueRef)
		}
	}
	return out
}

// DecideProposal records the decision of a person on a pending proposal
// (kluzo proposal approve|reject): proposal.decided, and the decision of the
// node blocked for it, in one transaction. An approved change becomes an
// exception of the check for its node, by path and content.
func DecideProposal(ctx context.Context, db *store.DB, gen *ids.Generator, clk clock.Clock, project *config.Project, id string, approved bool, by string) error {
	s, err := person(db, gen, clk, project)
	if err != nil {
		return err
	}
	ps, err := db.Proposals(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ps, func(p store.ProposalRow) bool { return p.ID == id })
	switch {
	case i < 0:
		return fmt.Errorf("%w: proposal %s", store.ErrNotFound, id)
	case ps[i].State != contracts.ProposalPending:
		return fmt.Errorf("%w: proposal %s is %s already", core.ErrInvalidTransition, id, ps[i].State)
	case ps[i].Kind != contracts.ProposalChange:
		return fmt.Errorf("proposals of kind %s are not decided in v0.1", ps[i].Kind)
	}
	p := ps[i]
	var body core.ChangeProposal
	if err := json.Unmarshal(p.Payload, &body); err != nil {
		return fmt.Errorf("proposal %s: %w", id, err)
	}
	snap, err := db.Snapshot(ctx)
	if err != nil {
		return err
	}
	if !snap.Workspace.State.Active() {
		return fmt.Errorf("%w: the workspace is %s", core.ErrInvalidTransition, snap.Workspace.State)
	}
	n, err := nodeRow(snap, p.Node)
	if err != nil {
		return err
	}
	state := contracts.ProposalRejected
	if approved {
		state = contracts.ProposalApproved
	}
	return s.decideRetrying(ctx, n, core.ProposalDecided{Approved: approved, By: by, Changes: body.Changes}, nil, true, decided(p, state, by))
}

// nodeRow reads a node of the snapshot by id.
func nodeRow(snap store.Snapshot, id string) (store.NodeRow, error) {
	for _, n := range snap.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return store.NodeRow{}, fmt.Errorf("%w: node %s", store.ErrNotFound, id)
}
