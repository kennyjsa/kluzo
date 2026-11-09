package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/kennyjsa/kluzo/internal/gitops"
)

// Clean removes, on demand, what Kluzo left on this machine for a project
// (kluzo clean, ADR 0025): orphan checkouts, the worktrees failed nodes keep
// for diagnosis, the HOMEs and build caches of the workspaces that are over,
// and the Docker resources of the workspaces that are over; with Images, every
// image marked by Kluzo and the dangling images (kluzo clean --images). By
// default it only lists them (--dry-run); state.db, the logs, the download
// caches and the directories whose journal cannot be read stay.
type Clean struct{ Host Host }

// CleanInput selects the project and whether to remove (--yes).
type CleanInput struct {
	ConfigDir string
	Yes       bool
	Images    bool
}

// CleanOutput is what was found, and removed when Removed is set.
type CleanOutput struct {
	Checkouts []string // orphan checkouts and worktrees of failed nodes
	Leftovers []string
	Projects  []string
	Images    []string
	Dangling  bool     // the dangling images too (--images)
	Unsure    []string // directories left alone: their journal cannot be read
	Docker    string   // why Docker was not looked at
	Disk      int64    // in bytes, before the cleanup
	Removed   bool
}

// Execute surveys the project and, with Yes, removes what it found.
func (u Clean) Execute(ctx context.Context, in CleanInput) (CleanOutput, error) {
	h := u.Host.fill()
	p, err := project(in.ConfigDir)
	if err != nil {
		return CleanOutput{}, err
	}
	s, err := surveyProject(ctx, h, p, in.ConfigDir)
	if err != nil {
		return CleanOutput{}, err
	}
	out := CleanOutput{Checkouts: append(s.Orphans, s.Failed...), Leftovers: s.Leftovers, Projects: s.Projects, Unsure: s.Unsure, Disk: s.Disk}
	images := s.Images
	if in.Images {
		images = s.Marked
	}
	for _, im := range images {
		out.Images = append(out.Images, im.ID)
	}
	if s.Docker != nil {
		out.Docker = s.Docker.Error()
	} else {
		out.Dangling = in.Images
	}
	if !in.Yes {
		return out, nil
	}
	abs, err := filepath.Abs(in.ConfigDir)
	if err != nil {
		return out, err
	}
	git := gitops.New(gitops.Config{ParentEnv: h.Env})
	for _, c := range out.Checkouts {
		if err := git.RemoveCheckout(ctx, filepath.Dir(abs), c); err != nil {
			return out, err
		}
	}
	for _, d := range out.Leftovers {
		if err := os.RemoveAll(d); err != nil {
			return out, err
		}
	}
	for _, name := range out.Projects {
		if err := h.Docker.DownProject(ctx, name); err != nil {
			return out, err
		}
	}
	if err := h.Docker.RemoveImageIDs(ctx, out.Images...); err != nil {
		return out, err
	}
	if out.Dangling {
		if err := h.Docker.PruneDangling(ctx); err != nil {
			return out, err
		}
	}
	out.Removed = true
	return out, nil
}
