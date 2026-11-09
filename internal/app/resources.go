package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/docker"
	"github.com/kennyjsa/kluzo/internal/store"
)

// The resources Kluzo leaves on this machine (ADR 0025): what kluzo doctor
// reports, kluzo clean removes and kluzo status measures. Only what carries a
// mark of Kluzo is considered: the directories of the workspaces, the label
// kluzo.workspace of the images and the compose projects named
// kluzo-<workspace>-<issue> or working in a workspace directory.

// defaultDiskWarn is retention.disk_warn when it is not set.
const defaultDiskWarn = "20GB"

// machineWorkspace is a workspace found on this machine.
type machineWorkspace struct {
	Dir   string // its directory
	ID    string
	State core.WorkspaceState
	Nodes map[string]core.NodeState // by issue
}

// active reports whether the workspace still owns its resources.
func (w machineWorkspace) active() bool { return w.State.Active() }

// scanWorkspaces reads every workspace under root/workspaces, of every
// project: a directory without a journal has no owner.
func scanWorkspaces(ctx context.Context, root string) ([]machineWorkspace, error) {
	dirs, err := os.ReadDir(filepath.Join(root, "workspaces"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []machineWorkspace
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		w := machineWorkspace{Dir: filepath.Join(root, "workspaces", d.Name()), Nodes: map[string]core.NodeState{}}
		if _, err := os.Stat(filepath.Join(w.Dir, "state.db")); err == nil {
			if err := w.read(ctx); err != nil {
				return nil, fmt.Errorf("workspace %s: %w", w.Dir, err)
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// read loads the state of the workspace from its journal.
func (w *machineWorkspace) read(ctx context.Context) error {
	db, err := store.Open(ctx, filepath.Join(w.Dir, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	snap, err := db.Snapshot(ctx)
	if err != nil || snap.Workspace == nil {
		return err
	}
	w.ID, w.State = snap.Workspace.ID, snap.Workspace.State
	for _, n := range snap.Nodes {
		w.Nodes[n.IssueRef] = n.State
	}
	return nil
}

// survey is what Kluzo left on this machine for a project.
type survey struct {
	// Orphans are the checkouts (worktrees and checkouts of the gates) with
	// no active node in the journal: their workspace is over, or their node
	// is integrated, cancelled or not there.
	Orphans []string
	// Failed are the worktrees a failed node keeps for diagnosis, until the
	// workspace closes or kluzo clean.
	Failed []string
	// Leftovers are the HOMEs and build caches of the workspaces that are over.
	Leftovers []string
	// Projects and Images are the Docker resources marked by Kluzo whose
	// workspace is not active on this machine.
	Projects []string
	Images   []docker.Image
	// Docker is why the Docker resources could not be read (nil when they were).
	Docker error
	// Disk is what the workspaces and the download cache of the project take,
	// in bytes; DiskWarn the limit of retention.disk_warn.
	Disk, DiskWarn int64
}

// checkoutDirs are the directories of a workspace that hold one checkout per
// issue.
var checkoutDirs = []string{"worktrees", "gates"}

// leftoverDirs are the HOMEs and build caches of a workspace
// (docs/contracts/data-model.md).
var leftoverDirs = []string{"home", "cache", "gates-home", "gates-cache"}

// surveyProject looks at the workspaces of the project in the data root of
// this machine and at the Docker resources marked by Kluzo.
func surveyProject(ctx context.Context, h Host, p *config.Project, configDir string) (survey, error) {
	var s survey
	root, err := dataRoot(h.Env)
	if err != nil {
		return s, err
	}
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return s, err
	}
	all, err := scanWorkspaces(ctx, root)
	if err != nil {
		return s, err
	}
	prefix := projectName(p, abs) + "-"
	for _, w := range all {
		if !strings.HasPrefix(filepath.Base(w.Dir), prefix) {
			continue
		}
		if err := s.checkouts(w); err != nil {
			return s, err
		}
		size, err := dirSize(w.Dir)
		if err != nil {
			return s, err
		}
		s.Disk += size
	}
	cache, err := dirSize(filepath.Join(root, "cache", projectName(p, abs)))
	if err != nil {
		return s, err
	}
	s.Disk += cache
	warn := cmp.Or(p.Config.Retention.DiskWarn, defaultDiskWarn)
	if s.DiskWarn, err = docker.ParseSize(warn); err != nil {
		return s, fmt.Errorf("%w: retention.disk_warn: %w", ErrInvalid, err)
	}
	s.Docker = s.docker(ctx, h.Docker, root, all)
	return s, nil
}

// checkouts sorts the checkouts and leftovers of a workspace of the project.
func (s *survey) checkouts(w machineWorkspace) error {
	for _, d := range checkoutDirs {
		entries, err := os.ReadDir(filepath.Join(w.Dir, d))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			path := filepath.Join(w.Dir, d, e.Name())
			switch state, ok := w.Nodes[e.Name()]; {
			case w.active() && ok && state == core.NodeFailed && d == "worktrees":
				s.Failed = append(s.Failed, path)
			case !w.active() || !ok || state == core.NodeIntegrated || state == core.NodeCancelled:
				s.Orphans = append(s.Orphans, path)
			}
		}
	}
	if w.active() {
		return nil
	}
	for _, d := range leftoverDirs {
		if _, err := os.Stat(filepath.Join(w.Dir, d)); err == nil {
			s.Leftovers = append(s.Leftovers, filepath.Join(w.Dir, d))
		}
	}
	return nil
}

// docker finds the compose projects and images marked by Kluzo whose
// workspace is not active on this machine. Docker unavailable is reported,
// not an error.
func (s *survey) docker(ctx context.Context, d *docker.Docker, root string, all []machineWorkspace) error {
	if err := d.Probe(ctx); err != nil {
		return err
	}
	owners := map[string]bool{} // ids of the active workspaces, lower case
	dirs := map[string]bool{}   // directories of the active workspaces
	for _, w := range all {
		if w.active() && w.ID != "" {
			owners[strings.ToLower(w.ID)] = true
			dirs[w.Dir] = true
		}
	}
	images, err := d.Images(ctx)
	if err != nil {
		return err
	}
	for _, im := range images {
		if !owners[strings.ToLower(im.Workspace)] {
			s.Images = append(s.Images, im)
		}
	}
	projects, err := d.Projects(ctx)
	if err != nil {
		return err
	}
	workspaces := filepath.Join(root, "workspaces")
	for _, pr := range projects {
		if !strings.HasPrefix(pr.Name, "kluzo-") && !inDir(workspaces, pr.WorkingDir) {
			continue // not marked by Kluzo
		}
		if !composeOwned(pr, owners, dirs) {
			s.Projects = append(s.Projects, pr.Name)
		}
	}
	return nil
}

// composeOwned reports whether a compose project belongs to an active
// workspace: by its name kluzo-<workspace>-<issue>, or by its working
// directory.
func composeOwned(pr docker.Project, owners, dirs map[string]bool) bool {
	if rest, ok := strings.CutPrefix(pr.Name, "kluzo-"); ok {
		if i := strings.LastIndex(rest, "-"); i > 0 && owners[rest[:i]] {
			return true
		}
	}
	for dir := range dirs {
		if inDir(dir, pr.WorkingDir) {
			return true
		}
	}
	return false
}

// inDir reports whether path is dir or inside it.
func inDir(dir, path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dirSize is the size of the files under dir, in bytes; 0 when it does not
// exist. Links are not followed.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// warnings are the findings of the survey for kluzo doctor.
func (s survey) warnings() []string {
	var out []string
	for _, o := range s.Orphans {
		out = append(out, "orphan checkout (no active node in the journal): "+o+"; kluzo clean removes it")
	}
	for _, p := range s.Projects {
		out = append(out, "compose project marked by Kluzo without an active workspace: "+p+"; kluzo clean removes it")
	}
	for _, im := range s.Images {
		out = append(out, fmt.Sprintf("image marked by Kluzo without an active workspace: %s (workspace %s, %s); kluzo clean removes it", im.ID, im.Workspace, HumanSize(im.Size)))
	}
	if s.Docker != nil {
		out = append(out, "docker: "+s.Docker.Error()+"; its resources were not checked")
	}
	if s.Disk > s.DiskWarn {
		out = append(out, fmt.Sprintf("disk: the workspaces and caches of the project take %s, above retention.disk_warn (%s); kluzo clean frees what is left over", HumanSize(s.Disk), HumanSize(s.DiskWarn)))
	}
	return out
}

// HumanSize prints a size in decimal units, like docker.
func HumanSize(n int64) string {
	units := []string{"B", "kB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1f%s", f, units[i])
}
