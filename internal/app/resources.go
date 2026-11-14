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
	"github.com/kennyjsa/kluzo/internal/platform/paths"
	"github.com/kennyjsa/kluzo/internal/platform/sessionenv"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// The resources Kluzo leaves on this machine (ADR 0025): what kluzo doctor
// reports, kluzo clean removes and kluzo status measures. Only what Kluzo can
// prove it created is considered: a workspace is the project's when its
// journal says so, and a Docker resource is Kluzo's when it names a workspace
// whose journal is on this machine, by the compose project name
// kluzo-<workspace>-<issue> (on a container, or the label compose writes on
// the images it builds) or a compose working directory inside a workspace.

// defaultDiskWarn is retention.disk_warn when it is not set.
const defaultDiskWarn = "20GB"

// machineWorkspace is a workspace directory found on this machine.
type machineWorkspace struct {
	Dir    string
	ID     string // "" without a journal
	Branch string
	State  core.WorkspaceState
	Nodes  map[string]core.NodeState // by issue
	// Err is why the journal could not be read (another schema version, a
	// broken file): the workspace is reported, never cleaned.
	Err error
}

// journaled reports whether the journal of the workspace was read.
func (w machineWorkspace) journaled() bool { return w.Err == nil && w.ID != "" }

// active reports whether the workspace still owns its resources.
func (w machineWorkspace) active() bool { return w.journaled() && w.State.Active() }

// scanWorkspaces reads, without migrating or writing, the journal of every
// workspace under root/workspaces, of every project.
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
		if d.IsDir() {
			w := machineWorkspace{Dir: filepath.Join(root, "workspaces", d.Name()), Nodes: map[string]core.NodeState{}}
			w.Err = w.read(ctx, store.OpenReadOnly)
			out = append(out, w)
		}
	}
	return out, nil
}

// read loads the state of the workspace from its journal, if it has one,
// opened with open.
func (w *machineWorkspace) read(ctx context.Context, open func(context.Context, string) (*store.DB, error)) error {
	path := filepath.Join(w.Dir, "state.db")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	db, err := open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	snap, err := db.Snapshot(ctx)
	if err != nil || snap.Workspace == nil {
		return err
	}
	w.ID, w.Branch, w.State = snap.Workspace.ID, snap.Workspace.Branch, snap.Workspace.State
	for _, n := range snap.Nodes {
		w.Nodes[n.IssueRef] = n.State
	}
	return nil
}

// projectWorkspaces are the workspaces of the project among those of the
// machine: those whose milestone branch names a milestone the project places
// in their directory. A directory without a readable journal is the
// project's only by name; it is returned apart, to be reported.
func projectWorkspaces(h Host, p *config.Project, configDir string, all []machineWorkspace) (own, unsure []machineWorkspace, err error) {
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range all {
		if !w.journaled() {
			if strings.HasPrefix(filepath.Base(w.Dir), projectName(p, abs)+"-") {
				unsure = append(unsure, w)
			}
			continue
		}
		title, ok := p.Config.Branches.MilestoneTitle(w.Branch)
		if !ok {
			continue
		}
		pl, err := place(h.Env, p, configDir, title)
		if err != nil {
			return nil, nil, err
		}
		if pl.Dir == w.Dir {
			own = append(own, w)
		}
	}
	return own, unsure, nil
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
	// Unsure are the directories of the project whose journal could not be
	// read: reported, never cleaned.
	Unsure []string
	// Projects and Images are the Docker resources of workspaces of this
	// machine that are over. Idle adds to Images the images of the compose
	// projects of Kluzo whose workspace has no journal here; none of them
	// belongs to an active workspace, and when a journal of this machine
	// cannot be read (its workspace may be active) only Images are idle.
	Projects []string
	Images   []docker.Image
	Idle     []docker.Image
	// Docker is why the Docker resources could not be read (nil when they were).
	Docker error
	// Disk is what the workspaces and the download cache of the project take,
	// in bytes; DiskWarn the limit of retention.disk_warn.
	Disk, DiskWarn int64
}

// surveyProject looks at the workspaces of the project in the data root of
// this machine and at the Docker resources marked by Kluzo.
func surveyProject(ctx context.Context, h Host, p *config.Project, configDir string) (survey, error) {
	var s survey
	root, err := dataRoot(h.Env)
	if err != nil {
		return s, err
	}
	all, err := scanWorkspaces(ctx, root)
	if err != nil {
		return s, err
	}
	own, unsure, err := projectWorkspaces(h, p, configDir, all)
	if err != nil {
		return s, err
	}
	for _, w := range unsure {
		reason := "no journal"
		if w.Err != nil {
			reason = w.Err.Error()
		}
		s.Unsure = append(s.Unsure, w.Dir+" ("+reason+")")
	}
	for _, w := range own {
		if err := s.checkouts(w); err != nil {
			return s, err
		}
		size, err := dirSize(w.Dir)
		if err != nil {
			return s, err
		}
		s.Disk += size
	}
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return s, err
	}
	cache, err := dirSize(filepath.Join(root, "cache", projectName(p, abs)))
	if err != nil {
		return s, err
	}
	s.Disk += cache
	if s.DiskWarn, err = docker.ParseSize(cmp.Or(p.Config.Retention.DiskWarn, defaultDiskWarn)); err != nil {
		return s, fmt.Errorf("%w: retention.disk_warn: %w", ErrInvalid, err)
	}
	s.Docker = s.docker(ctx, h.Docker, all)
	return s, nil
}

// checkouts sorts the checkouts and leftovers of a workspace of the project.
func (s *survey) checkouts(w machineWorkspace) error {
	l := supervisor.Layout{Dir: w.Dir}
	for _, d := range []string{l.Worktrees(), l.Gates()} {
		entries, err := os.ReadDir(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			path := filepath.Join(d, e.Name())
			switch state, ok := w.Nodes[e.Name()]; {
			case w.active() && ok && state == core.NodeFailed && d == l.Worktrees():
				s.Failed = append(s.Failed, path)
			case !w.active() || !ok || state == core.NodeIntegrated || state == core.NodeCancelled:
				s.Orphans = append(s.Orphans, path)
			}
		}
	}
	if w.active() {
		return nil
	}
	for _, d := range l.Leftovers() {
		if _, err := os.Stat(d); err == nil {
			s.Leftovers = append(s.Leftovers, d)
		}
	}
	return nil
}

// docker finds the compose projects and images of the workspaces of this
// machine that are over. Docker unavailable is reported, not an error.
func (s *survey) docker(ctx context.Context, d *docker.Docker, all []machineWorkspace) error {
	if err := d.Probe(ctx); err != nil {
		return err
	}
	over := map[string]bool{} // id (lower case) → the workspace is over; absent: unknown here
	var overDirs []string
	unreadable := false
	for _, w := range all {
		switch {
		case w.journaled():
			over[strings.ToLower(w.ID)] = !w.active()
			if !w.active() {
				overDirs = append(overDirs, w.Dir)
			}
		case w.Err != nil:
			unreadable = true
		}
	}
	images, err := d.Images(ctx)
	if err != nil {
		return err
	}
	for _, im := range images {
		ws, _, ok := sessionenv.ParseComposeProject(im.Project)
		if !ok {
			continue
		}
		switch done, known := over[ws]; {
		case known && done:
			s.Images = append(s.Images, im)
			s.Idle = append(s.Idle, im)
		case !known && !unreadable:
			s.Idle = append(s.Idle, im)
		}
	}
	projects, err := d.Projects(ctx)
	if err != nil {
		return err
	}
	for _, pr := range projects {
		ws, _, named := sessionenv.ParseComposeProject(pr.Name)
		inOver := false
		for _, dir := range overDirs {
			inOver = inOver || paths.WithinAbs(dir, pr.WorkingDir)
		}
		if (named && over[ws]) || inOver {
			s.Projects = append(s.Projects, pr.Name)
		}
	}
	return nil
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

// above reports whether the disk passed retention.disk_warn.
func (s survey) above() bool { return s.Disk > s.DiskWarn }

// warnings are the findings of the survey for kluzo doctor.
func (s survey) warnings() []string {
	var out []string
	for _, o := range s.Orphans {
		out = append(out, "orphan checkout (no active node in the journal): "+o+"; kluzo clean removes it")
	}
	for _, u := range s.Unsure {
		out = append(out, "workspace directory whose journal cannot be read: "+u+"; kluzo clean leaves it")
	}
	for _, p := range s.Projects {
		out = append(out, "compose project of a workspace that is over: "+p+"; kluzo clean removes it")
	}
	for _, im := range s.Images {
		out = append(out, fmt.Sprintf("image of a workspace that is over: %s (compose project %s, %s); kluzo clean removes it", im.ID, im.Project, HumanSize(im.Size)))
	}
	if s.Docker != nil {
		out = append(out, "docker: "+s.Docker.Error()+"; its resources were not checked")
	}
	if s.above() {
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
