package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/store"
)

// The milestone branch and the closing (ADR 0003, F22 in v0.1): the branch is
// created or adopted when the workspace is created; with every node
// integrated, the PR milestone → main is opened and the workspace is ready to
// merge; the merge on main is human, and Kluzo only observes it.

// defaultPollInterval is how often the merge on main is observed when
// tracker.poll_interval is not set.
const defaultPollInterval = 30 * time.Second

// mainBranch is the main branch of the project.
func (s *Supervisor) mainBranch() string {
	if b := s.cfg.Project.Config.Branches.Main; b != "" {
		return b
	}
	return "main"
}

// transitionWorkspace moves the workspace in t with the effects of the move.
func (s *Supervisor) transitionWorkspace(t *store.Tx, trigger core.WorkspaceTrigger, h store.Header) error {
	from, err := t.WorkspaceState()
	if err != nil {
		return err
	}
	to, err := core.NextWorkspace(from, trigger)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	specs, err := s.effectSpecs(h, "", "", core.WorkspaceEffects(from, to))
	if err != nil {
		return err
	}
	_, err = t.TransitionWorkspace(trigger, h, specs...)
	return err
}

// moveWorkspace applies a workspace trigger in its own transaction.
func (s *Supervisor) moveWorkspace(ctx context.Context, trigger core.WorkspaceTrigger) error {
	h, err := s.header()
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(t *store.Tx) error { return s.transitionWorkspace(t, trigger, h) })
}

// ensureMilestone creates or adopts the milestone branch, locally and on the
// remote: a branch on the remote is adopted (fetched, or the local one
// fast-forwarded); a branch only local is pushed; a branch on neither side is
// created on the remote from main, through the SCM, and fetched.
func (s *Supervisor) ensureMilestone(ctx context.Context) error {
	b := s.cfg.Branch
	_, err := s.d.SCM.BranchHead(ctx, b)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		local, err := s.d.Git.HasBranch(ctx, s.cfg.Repo, b)
		if err != nil {
			return err
		}
		if !local {
			if _, err := s.d.SCM.EnsureBranch(ctx, b, s.mainBranch()); err != nil {
				return err
			}
		}
	case err != nil:
		return err
	}
	if _, err := s.syncMilestone(ctx); err != nil {
		return err
	}
	return s.push(ctx, branchRef(b)+":"+branchRef(b))
}

// openPR runs the closing of v0.1: the PR milestone → main with a simple body
// (no E2E and no report yet), pr.opened, the workspace ready to merge and a
// notification, in one transaction with the completion of the effect.
func (s *Supervisor) openPR(ctx context.Context, f store.Effect) error {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil {
		return err
	}
	if snap.Workspace.State != core.WorkspaceClosing {
		return s.d.DB.Tx(ctx, func(t *store.Tx) error { return completed(f)(t, s.d.Clock.Now()) })
	}
	b := s.cfg.Branch
	if _, err := s.syncMilestone(ctx); err != nil {
		return err
	}
	if err := s.push(ctx, branchRef(b)+":"+branchRef(b)); err != nil {
		return err
	}
	pr, err := s.d.SCM.OpenPR(ctx, ports.NewPR{Title: "Milestone " + s.cfg.Milestone, Body: prBody(s.cfg.Milestone, snap.Nodes), Head: b, Base: s.mainBranch()}, f.IdemKey)
	if err != nil {
		return err
	}
	h, err := s.header()
	if err != nil {
		return err
	}
	opened, _ := json.Marshal(contracts.PROpened{Ref: pr.Ref, URL: pr.URL}) // plain data
	notify, err := s.effectSpecs(h, "", "", []core.Effect{{Kind: core.EffectNotify, Key: "notify", Title: "Milestone " + s.cfg.Milestone + " ready to merge",
		Body: "PR " + pr.URL + ": review it and merge it into " + s.mainBranch() + "; the merge is yours.", Priority: ports.PriorityHigh}})
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		if _, err := t.Append(store.Event{Header: h, Kind: contracts.EventPROpened, Payload: opened}, notify...); err != nil {
			return err
		}
		th, err := s.header()
		if err != nil {
			return err
		}
		if err := s.transitionWorkspace(t, core.TriggerClosingDone, th); err != nil {
			return err
		}
		return completed(f)(t, th.TS)
	})
}

// prBody is the description of the PR milestone → main in v0.1: the nodes
// integrated, and that the merge is human.
func prBody(milestone string, nodes []store.NodeRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Milestone %s, delivered by Kluzo.\n\n", milestone)
	for _, n := range nodes {
		if n.State == core.NodeIntegrated {
			fmt.Fprintf(&b, "- #%s %s (merge %s)\n", n.IssueRef, n.Title, short(n.MergedSHA))
		}
	}
	b.WriteString("\nThe merge into the main branch is yours. A merge commit keeps each node revertible (ADR 0009).\n")
	return b.String()
}

func short(sha string) string { return sha[:min(len(sha), 12)] }

// watchMerge observes, at most once per poll interval, whether the PR
// milestone → main was merged, and then moves the workspace to merged. A
// failure to ask is logged and asked again at the next interval.
func (s *Supervisor) watchMerge(ctx context.Context, w *store.WorkspaceRow) (bool, error) {
	interval := s.cfg.Project.Config.Tracker.PollInterval
	if interval == 0 {
		interval = defaultPollInterval
	}
	now := s.d.Clock.Now()
	if w.PR.Ref == "" || (!s.polled.IsZero() && now.Sub(s.polled) < interval) {
		return false, nil
	}
	s.polled = now
	merged, err := s.d.SCM.IsMerged(ctx, w.PR.Ref)
	if err != nil {
		s.d.Log.Warn("could not read the PR milestone → main", "pr", w.PR.Ref, "err", err)
		return false, nil
	}
	if !merged {
		return false, nil
	}
	return true, s.moveWorkspace(ctx, core.TriggerMergedToMain)
}
