package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/store"
)

// The merge queue (ADR 0003, ADR 0009): one node at a time, the rebase of the
// node branch on the milestone tip, the freshness gate and the merge commit
// with trailers, then the push of the milestone branch with the credentials
// of the bot.

// remote is the repository of the project with the credentials of the bot.
func (s *Supervisor) remote(ctx context.Context) (gitops.Remote, error) {
	r := gitops.Remote{URL: s.cfg.Remote}
	if s.d.GitToken == nil {
		return r, nil
	}
	var err error
	r.Token, err = s.d.GitToken(ctx)
	return r, err
}

// push sends refspecs to the remote of the project.
func (s *Supervisor) push(ctx context.Context, refspecs ...string) error {
	r, err := s.remote(ctx)
	if err != nil {
		return err
	}
	return s.d.Git.Push(ctx, s.cfg.Repo, r, refspecs...)
}

// syncMilestone reconciles the local milestone branch with the remote one
// (a person may have pushed to it) and returns its tip.
func (s *Supervisor) syncMilestone(ctx context.Context) (string, error) {
	r, err := s.remote(ctx)
	if err != nil {
		return "", err
	}
	return s.d.Git.SyncBranch(ctx, s.cfg.Repo, r, s.cfg.Branch)
}

// branchRef is the full name of a local branch, for a refspec.
func branchRef(name string) string { return "refs/heads/" + name }

// rebase runs the rebase effect: the node branch, in its worktree, on the
// tip of the milestone, stored as Rebased with merge.rebased. A conflict is
// aborted and goes back to the implementer as the integration cause.
func (s *Supervisor) rebase(ctx context.Context, f store.Effect, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	if n.State != core.NodeIntegrating {
		return s.decide(ctx, n, core.Rebased{}, completed(f)) // stale: dropped by the core
	}
	tip, err := s.syncMilestone(ctx)
	if err != nil {
		return err
	}
	wt := s.worktree(n)
	// The worktree is there unless a person removed it; the branch keeps the work.
	if _, err := s.d.Git.AddWorktree(ctx, gitops.WorktreeSpec{Repo: s.cfg.Repo, Path: wt, Branch: s.branch(n), Base: s.cfg.Branch}); err != nil {
		return err
	}
	head, conflict, err := s.d.Git.Rebase(ctx, s.cfg.Repo, wt, tip)
	if err != nil {
		return err
	}
	if conflict != nil {
		return s.decide(ctx, n, core.Rebased{Tip: tip, Conflict: &core.IntegrationFailure{ConflictFiles: conflict.Files, Markers: conflict.Markers}}, completed(f))
	}
	body, _ := json.Marshal(contracts.MergeRebased{BaseSHA: tip, HeadSHA: head}) // plain data
	return s.decide(ctx, n, core.Rebased{Tip: tip, Head: head}, completed(f), store.Event{Kind: contracts.EventMergeRebased, Payload: body})
}

// merge runs the merge effect: the merge commit on the milestone branch, the
// push of the branch, and Merged with merge.done. A run again after a crash
// finds the merge already made and only pushes.
func (s *Supervisor) merge(ctx context.Context, f store.Effect, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	r, err := run(n)
	if err != nil {
		return err
	}
	if n.State != core.NodeIntegrating {
		return s.decide(ctx, n, core.Merged{}, completed(f)) // stale: dropped by the core
	}
	// A person may have pushed to the milestone since the rebase: the node is
	// rebased again on the new tip, never merged on a stale one.
	tip, err := s.syncMilestone(ctx)
	if err != nil {
		return err
	}
	if tip != r.Tip && !s.d.Git.Contains(ctx, s.cfg.Repo, tip, r.Head) {
		return s.decide(ctx, n, core.TipMoved{}, completed(f))
	}
	sha, err := s.d.Git.Merge(ctx, gitops.MergeSpec{
		Repo: s.cfg.Repo, Branch: s.cfg.Branch, Tip: r.Tip, Head: r.Head,
		Message: gitops.MergeMessage(n.IssueRef, n.Title, n.ID, s.cfg.Workspace, n.Attempt),
	})
	if err != nil {
		return err
	}
	if err := s.push(ctx, branchRef(s.cfg.Branch)+":"+branchRef(s.cfg.Branch)); err != nil {
		// Refused (the remote moved in between): undo the local merge, so the
		// next run syncs, finds the new tip and rebases again.
		if sha != tip {
			if rerr := s.d.Git.ResetBranch(ctx, s.cfg.Repo, s.cfg.Branch, tip, sha); rerr != nil {
				return errors.Join(err, rerr)
			}
		}
		return err
	}
	body, _ := json.Marshal(contracts.MergeDone{MergeSHA: sha}) // plain data
	return s.decide(ctx, n, core.Merged{SHA: sha}, completed(f), store.Event{Kind: contracts.EventMergeDone, Payload: body})
}

// changedSince lists the files the milestone changed from the commit the
// review judged to the tip, for an implementer whose freshness gates failed.
func (s *Supervisor) changedSince(ctx context.Context, reviewed, tip string) (string, error) {
	files, err := s.d.Git.Diff(ctx, s.cfg.Repo, reviewed, tip)
	if err != nil {
		return "", err
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Path
	}
	return strings.Join(names, "\n"), nil
}

// areaCheck compares the diff of the attempt with the areas of the node before
// it enters the merge queue (ADR 0011), with the areas of kluzo.yaml frozen at
// the base of the attempt: an area the diff touched and the node did not
// declare is journaled as area.deviation, which adds it to the node.
func (s *Supervisor) areaCheck(ctx context.Context, n store.NodeRow, r core.Run) ([]store.Event, error) {
	frozen, err := s.frozen(ctx, r.Base)
	if err != nil {
		return nil, err
	}
	files, err := s.d.Git.Diff(ctx, s.cfg.Repo, r.Base, r.Head)
	if err != nil {
		return nil, err
	}
	areas := frozen.Areas
	byArea := map[string][]string{}
	for _, f := range files {
		hit := false
		for _, name := range slices.Sorted(maps.Keys(areas)) {
			if slices.ContainsFunc(areas[name], func(g string) bool { return guard.Match(g, f.Path) }) {
				byArea[name], hit = append(byArea[name], f.Path), true
			}
		}
		if !hit {
			byArea[core.SharedArea] = append(byArea[core.SharedArea], f.Path)
		}
	}
	touched := slices.Sorted(maps.Keys(byArea))
	extra := core.AreaDeviation(n.Areas, touched)
	if len(extra) == 0 {
		return nil, nil
	}
	var outside []string
	for _, a := range extra {
		outside = append(outside, byArea[a]...)
	}
	body, _ := json.Marshal(contracts.AreaDeviation{Declared: n.Areas, Touched: touched, Files: outside}) // plain data
	return []store.Event{{Kind: contracts.EventAreaDeviation, Payload: body}}, nil
}
