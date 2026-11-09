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
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
)

// ErrUnavailable means there is no docker CLI on the machine: the cleanup of
// Docker is skipped with a warning.
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
	args := []string{"image", "ls", "--quiet", "--no-trunc"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	out, err := d.run(ctx, args...)
	if err != nil {
		return err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil
	}
	_, err = d.run(ctx, append([]string{"image", "rm", "--force"}, ids...)...)
	return err
}

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
