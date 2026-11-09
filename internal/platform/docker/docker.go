// Package docker removes the Docker resources Kluzo can prove it created
// (ADR 0025): compose projects by name or by a working directory inside the
// workspace, images by label, dangling images and the build cache over a
// limit. It runs the docker CLI through platform/exec and never removes
// anything without one of those marks.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
)

// ErrUnavailable means there is no docker CLI on the machine, or its daemon
// cannot be reached: the cleanup of Docker is skipped with a warning.
var ErrUnavailable = errors.New("docker is not available")

// Docker runs the docker CLI.
type Docker struct {
	bin string
	env []string
}

// New returns the runner of bin ("docker" when empty, looked up in the PATH
// of Kluzo). env is the environment of Kluzo: the CLI needs HOME for its
// contexts and DOCKER_* to find the daemon.
func New(bin string, env []string) *Docker {
	if bin == "" {
		bin = "docker"
	}
	return &Docker{bin: bin, env: env}
}

func (d *Docker) run(ctx context.Context, args ...string) (string, error) {
	o, err := exec.Run(ctx, exec.Spec{Bin: d.bin, Args: args, Env: d.env})
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err == nil && o.Status.Code != 0 {
		err = fmt.Errorf("docker %s: exit %d: %s", strings.Join(args, " "), o.Status.Code, strings.TrimSpace(o.Stderr))
	}
	return o.Stdout, err
}

// Probe checks that the daemon answers: docker version without the server
// part (an unreachable daemon) is ErrUnavailable, like a missing CLI
// (docs/contracts/data-model.md, cleanup rules).
func (d *Docker) Probe(ctx context.Context) error {
	out, err := d.run(ctx, "version", "--format", "{{.Server.Version}}")
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	switch {
	case err != nil:
		return fmt.Errorf("%w: the daemon does not answer: %w", ErrUnavailable, err)
	case strings.TrimSpace(out) == "":
		return fmt.Errorf("%w: docker version has no server part", ErrUnavailable)
	}
	return nil
}

// Project is a compose project: its name and the working directory compose
// recorded on its containers.
type Project struct{ Name, WorkingDir string }

// projectLabels are the compose labels of a container.
const projectLabels = `{{.Label "com.docker.compose.project"}}` + "\t" + `{{.Label "com.docker.compose.project.working_dir"}}`

// Projects lists the compose projects that have containers, running or not.
func (d *Docker) Projects(ctx context.Context) ([]Project, error) {
	out, err := d.run(ctx, "ps", "--all", "--filter", "label=com.docker.compose.project", "--format", projectLabels)
	if err != nil {
		return nil, err
	}
	var ps []Project
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, dir, _ := strings.Cut(line, "\t")
		if name == "" || slices.ContainsFunc(ps, func(p Project) bool { return p.Name == name }) {
			continue
		}
		ps = append(ps, Project{Name: name, WorkingDir: dir})
	}
	return ps, nil
}

// DownProject removes a compose project with its volumes and the images it
// built: docker compose down -v --rmi local. A project with nothing left is
// a no-op.
func (d *Docker) DownProject(ctx context.Context, name string) error {
	_, err := d.run(ctx, "compose", "--project-name", name, "down", "--volumes", "--rmi", "local", "--remove-orphans")
	return err
}

// RemoveImages removes the images that carry every label (key=value).
func (d *Docker) RemoveImages(ctx context.Context, labels ...string) error {
	if len(labels) == 0 {
		return errors.New("docker: removing images needs at least one label")
	}
	ids, err := d.imageIDs(ctx, labels...)
	if err != nil {
		return err
	}
	return d.RemoveImageIDs(ctx, ids...)
}

// imageIDs lists the ids of the images that carry every label (key=value, or
// a key alone).
func (d *Docker) imageIDs(ctx context.Context, labels ...string) ([]string, error) {
	args := []string{"image", "ls", "--quiet", "--no-trunc"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	out, err := d.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, id := range strings.Fields(out) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// RemoveImageIDs removes the images with these ids; none is a no-op.
func (d *Docker) RemoveImageIDs(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := d.run(ctx, append([]string{"image", "rm", "--force"}, ids...)...)
	return err
}

// LabelWorkspace is the label of the images the gates build: the workspace
// that built them (ADR 0025).
const LabelWorkspace = "kluzo.workspace"

// Image is an image Kluzo marked: its id, the workspace of its label and its
// size in bytes.
type Image struct {
	ID, Workspace string
	Size          int64
}

// Images lists the images that carry the label kluzo.workspace.
func (d *Docker) Images(ctx context.Context) ([]Image, error) {
	ids, err := d.imageIDs(ctx, LabelWorkspace)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	out, err := d.run(ctx, append([]string{"image", "inspect", "--format", `{{.Id}}{{"\t"}}{{index .Config.Labels "` + LabelWorkspace + `"}}{{"\t"}}{{.Size}}`}, ids...)...)
	if err != nil {
		return nil, err
	}
	var images []Image
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		images = append(images, Image{ID: f[0], Workspace: f[1], Size: size})
	}
	return images, nil
}

// BuildCache is the size of the build cache, in bytes.
func (d *Docker) BuildCache(ctx context.Context) (int64, error) {
	out, err := d.run(ctx, "system", "df", "--format", `{{.Type}}{{"\t"}}{{.Size}}`)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if kind, size, ok := strings.Cut(line, "\t"); ok && kind == "Build Cache" {
			return ParseSize(size)
		}
	}
	return 0, nil
}

// ParseSize reads a size as docker prints it (decimal units: 0B, 12.3kB,
// 1.5GB) or as kluzo.yaml writes it (20GB), in bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i <= 0 {
		return 0, fmt.Errorf("size %q has no number or no unit", s)
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: %w", s, err)
	}
	unit, ok := sizeUnits[strings.ToUpper(s[i:])]
	if !ok {
		return 0, fmt.Errorf("size %q: unknown unit %q", s, s[i:])
	}
	return int64(n * unit), nil
}

// sizeUnits are the decimal units of docker and kluzo.yaml.
var sizeUnits = map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}

// PruneDangling removes the dangling images (<none>, untagged and unused).
func (d *Docker) PruneDangling(ctx context.Context) error {
	_, err := d.run(ctx, "image", "prune", "--force")
	return err
}

// PruneBuildCache reduces the build cache to keep (a size like 10GB), keeping
// the most recent entries.
func (d *Docker) PruneBuildCache(ctx context.Context, keep string) error {
	_, err := d.run(ctx, "builder", "prune", "--force", "--keep-storage", keep)
	return err
}
