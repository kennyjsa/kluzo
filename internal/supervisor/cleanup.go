package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

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

// cleanupNode removes what an integrated or cancelled node leaves: its
// worktree and local branch, the checkout of its gates, its compose project
// (by name, or by a working directory in those checkouts) with volumes and
// local images, and the images its gates built. Logs and journal stay.
func (s *Supervisor) cleanupNode(ctx context.Context, p effectPayload) error {
	n, err := s.node(ctx, p.Node)
	if err != nil {
		return err
	}
	if err := s.d.Git.RemoveWorktree(ctx, s.cfg.Repo, s.worktree(n), s.branch(n)); err != nil {
		return err
	}
	dirs := []string{s.worktree(n), s.gatesCheckout(n)}
	if err := s.d.Git.RemoveCheckout(ctx, s.cfg.Repo, dirs[1]); err != nil {
		return err
	}
	return s.docker(func(d *docker.Docker) error {
		if err := s.downProjects(ctx, d, []string{sessionenv.ComposeProject(s.cfg.Workspace, n.IssueRef)}, dirs); err != nil {
			return err
		}
		return d.RemoveImages(ctx, "kluzo.workspace="+s.cfg.Workspace, "kluzo.node="+n.ID)
	})
}

// cleanupWorkspace removes, when the workspace is merged or cancelled, every
// worktree and node branch, the checkouts of the gates, the synthetic HOMEs and
// the build caches of the workspace, its compose projects and images, the
// dangling images and the Docker build cache above
// retention.docker_build_cache. state.db, the logs and the download caches of
// the project stay.
func (s *Supervisor) cleanupWorkspace(ctx context.Context) error {
	snap, err := s.d.DB.Snapshot(ctx)
	if err != nil {
		return err
	}
	var names []string
	for _, n := range snap.Nodes {
		if err := s.d.Git.RemoveWorktree(ctx, s.cfg.Repo, s.worktree(n), s.branch(n)); err != nil {
			return err
		}
		if err := s.d.Git.RemoveCheckout(ctx, s.cfg.Repo, s.gatesCheckout(n)); err != nil {
			return err
		}
		names = append(names, sessionenv.ComposeProject(s.cfg.Workspace, n.IssueRef))
	}
	for _, d := range []string{"worktrees", "gates", sessionsPlace.home, sessionsPlace.builds, gatesPlace.home, gatesPlace.builds} {
		if err := os.RemoveAll(filepath.Join(s.cfg.Dir, d)); err != nil {
			return err
		}
	}
	return s.docker(func(d *docker.Docker) error {
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
// has no Docker.
func (s *Supervisor) docker(fn func(*docker.Docker) error) error {
	err := fn(s.d.Docker)
	if errors.Is(err, docker.ErrUnavailable) {
		s.d.Log.Warn("docker is not available: the cleanup of Docker is skipped", "workspace", s.cfg.Workspace, "err", err)
		return nil
	}
	return err
}
