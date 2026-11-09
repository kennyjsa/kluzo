package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/docker"
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
	"github.com/kennyjsa/kluzo/internal/store"
)

// The cleanup by lifecycle (ADR 0025), as effects of the outbox: it removes
// only what Kluzo can prove it created, and every step may run again.

// defaultBuildCache is retention.docker_build_cache when it is not set.
const defaultBuildCache = "10GB"

// gatesCheckout is the clean checkout the gates of a node run in.
func (s *Supervisor) gatesCheckout(n store.NodeRow) string {
	return filepath.Join(s.cfg.Dir, "gates", n.IssueRef)
}

// removeCheckouts removes the local git state of a node: its worktree, its
// local branch and the checkout of its gates.
func (s *Supervisor) removeCheckouts(ctx context.Context, n store.NodeRow) error {
	if err := s.d.Git.RemoveWorktree(ctx, s.cfg.Repo, s.worktree(n), s.branch(n)); err != nil {
		return err
	}
	return s.d.Git.RemoveCheckout(ctx, s.cfg.Repo, s.gatesCheckout(n))
}

// cleanupNode removes what an integrated or cancelled node leaves: its
// worktree and local branch, the checkout of its gates, its compose project
// (by name, or by a working directory in those checkouts) with volumes and
// local images, and the images its gates built. The branch of an integrated
// node is deleted on the remote too (its work is in the milestone); a
// cancelled node keeps it, the only copy of its work. Logs and journal stay.
func (s *Supervisor) cleanupNode(ctx context.Context, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	if err := s.removeCheckouts(ctx, n); err != nil {
		return err
	}
	if n.State == core.NodeIntegrated {
		if err := s.deleteRemoteBranches(ctx, n); err != nil {
			return err
		}
	}
	dirs := []string{s.worktree(n), s.gatesCheckout(n)}
	return s.docker(ctx, func(d *docker.Docker) error {
		if err := s.downProjects(ctx, d, []string{sessionenv.ComposeProject(s.cfg.Workspace, n.IssueRef)}, dirs); err != nil {
			return err
		}
		return d.RemoveImages(ctx, "kluzo.workspace="+s.cfg.Workspace, "kluzo.node="+n.ID)
	})
}

// cleanupWorkspace runs cleanup.workspace f: when the workspace is merged or
// cancelled it removes every worktree and node branch, the checkouts of the
// gates, the synthetic HOMEs and the build caches of the workspace, its
// compose projects and images, the dangling images and the Docker build cache
// above retention.docker_build_cache. A merged workspace deletes the node
// branches left on the remote; a cancelled one keeps them and lists them in a
// notify effect, queued in the transaction that completes f, so a crash never
// sends it twice. state.db, the logs and the download caches of the project
// stay.
func (s *Supervisor) cleanupWorkspace(ctx context.Context, f store.Effect) error {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil {
		return err
	}
	var names []string
	for _, n := range snap.Nodes {
		if err := s.removeCheckouts(ctx, n); err != nil {
			return err
		}
		names = append(names, sessionenv.ComposeProject(s.cfg.Workspace, n.IssueRef))
	}
	for _, d := range []string{"worktrees", "gates", sessionsPlace.home, sessionsPlace.builds, gatesPlace.home, gatesPlace.builds} {
		if err := os.RemoveAll(filepath.Join(s.cfg.Dir, d)); err != nil {
			return err
		}
	}
	if err := s.dockerWorkspace(ctx, names); err != nil {
		return err
	}
	var kept []string
	switch snap.Workspace.State {
	case core.WorkspaceMerged:
		err = s.deleteRemoteBranches(ctx, snap.Nodes...)
	case core.WorkspaceCancelled:
		kept, err = s.keptBranches(ctx, snap.Nodes)
	}
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	var notify []store.EffectSpec
	if len(kept) > 0 {
		h := store.Header{TS: now}
		if notify, err = s.effectSpecs(h, "", "", []core.Effect{{Kind: core.EffectNotify, Key: "kept",
			Title: "Milestone " + s.cfg.Milestone + " cancelled: node branches kept",
			Body:  "These branches stay on the remote, with the work of the cancelled nodes: " + strings.Join(kept, ", ") + ". Delete them when you no longer need them."}}); err != nil {
			return err
		}
	}
	return s.d.DB.Tx(ctx, func(t *store.Tx) error {
		if err := t.FollowEffect(f.ID, now, notify...); err != nil {
			return err
		}
		return completed(f)(t, now)
	})
}

// keptBranches are the node branches a cancelled workspace leaves on the
// remote: they hold the only copy of the work.
func (s *Supervisor) keptBranches(ctx context.Context, nodes []store.NodeRow) ([]string, error) {
	r, err := s.remote(ctx)
	if err != nil {
		return nil, err
	}
	return s.d.Git.RemoteBranches(ctx, s.cfg.Repo, r, s.branches(nodes)...)
}

// branches are the branch names of the nodes.
func (s *Supervisor) branches(nodes []store.NodeRow) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = s.branch(n)
	}
	return out
}

// deleteRemoteBranches deletes the branches of the nodes on the remote, with
// the credentials of the bot; a branch already absent counts as deleted.
func (s *Supervisor) deleteRemoteBranches(ctx context.Context, nodes ...store.NodeRow) error {
	r, err := s.remote(ctx)
	if err != nil {
		return err
	}
	return s.d.Git.DeleteRemoteBranches(ctx, s.cfg.Repo, r, s.branches(nodes)...)
}

// dockerWorkspace removes the Docker resources of the workspace: its compose
// projects and images, the dangling images and the build cache above
// retention.docker_build_cache.
func (s *Supervisor) dockerWorkspace(ctx context.Context, names []string) error {
	return s.docker(ctx, func(d *docker.Docker) error {
		if err := s.downProjects(ctx, d, names, []string{s.cfg.Dir}); err != nil {
			return err
		}
		if err := d.RemoveImages(ctx, "kluzo.workspace="+s.cfg.Workspace); err != nil {
			return err
		}
		if err := d.PruneDangling(ctx); err != nil {
			return err
		}
		limit := s.cfg.Project.Config.Retention.DockerBuildCache
		switch limit {
		case "off":
			return nil
		case "":
			limit = defaultBuildCache
		}
		return d.PruneBuildCache(ctx, limit)
	})
}

// downProjects removes the compose projects named, and those whose working
// directory is inside one of dirs (a project may name itself, ADR 0025).
func (s *Supervisor) downProjects(ctx context.Context, d *docker.Docker, names, dirs []string) error {
	projects, err := d.Projects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		for _, dir := range dirs {
			if _, ok := inside(dir, p.WorkingDir); ok && filepath.IsAbs(p.WorkingDir) && !slices.Contains(names, p.Name) {
				names = append(names, p.Name)
			}
		}
	}
	for _, name := range names {
		if err := d.DownProject(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// docker runs a cleanup of Docker, skipped with a warning when the machine
// has no Docker CLI or its daemon does not answer.
func (s *Supervisor) docker(ctx context.Context, fn func(*docker.Docker) error) error {
	err := s.d.Docker.Probe(ctx)
	if err == nil {
		err = fn(s.d.Docker)
	}
	if errors.Is(err, docker.ErrUnavailable) {
		s.d.Log.Warn("docker is not available: the cleanup of Docker is skipped", "workspace", s.cfg.Workspace, "err", err)
		return nil
	}
	return err
}
