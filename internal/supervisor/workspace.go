package supervisor

import (
	"context"
	"encoding/json"
	"fmt"

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
	Content  []byte // what is committed
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
		specs, err := s.effectSpecs(h, "", "", []core.Effect{{Kind: core.EffectCommitPlan, Key: "plan", Path: a.Path, Body: string(a.Content),
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
