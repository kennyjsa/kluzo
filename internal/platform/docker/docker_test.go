package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDocker writes a docker CLI that records its arguments, one call per
// line, and answers ps and image ls with the given output.
func fakeDocker(t *testing.T, ps, images string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncase \"$1 $2\" in\n" +
		"'ps --all') printf '%s' '" + ps + "' ;;\n" +
		"'image ls') printf '%s' '" + images + "' ;;\n" +
		"'compose --project-name') if [ \"$3\" = broken ]; then echo boom >&2; exit 1; fi ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func read(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestCleanup(t *testing.T) {
	ctx := context.Background()
	bin, calls := fakeDocker(t, "kluzo-ws_1-7\t/w/worktrees/7\nkluzo-ws_1-7\t/w/worktrees/7\ntaw-dev\t/w/worktrees/7/app\n", "sha256:a\nsha256:b\n")
	d := New(bin, nil)
	ps, err := d.Projects(ctx)
	if err != nil || len(ps) != 2 || ps[0] != (Project{"kluzo-ws_1-7", "/w/worktrees/7"}) || ps[1].Name != "taw-dev" {
		t.Fatalf("Projects = %+v, %v", ps, err)
	}
	for _, step := range []error{
		d.DownProject(ctx, "taw-dev"),
		d.RemoveImages(ctx, "kluzo.workspace=ws_1", "kluzo.node=nd_7"),
		d.PruneDangling(ctx),
		d.PruneBuildCache(ctx, "10GB"),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	want := []string{
		"ps --all --filter label=com.docker.compose.project --format " + projectLabels,
		"compose --project-name taw-dev down --volumes --rmi local --remove-orphans",
		"image ls --quiet --no-trunc --filter label=kluzo.workspace=ws_1 --filter label=kluzo.node=nd_7",
		"image rm --force sha256:a sha256:b",
		"image prune --force",
		"builder prune --force --keep-storage 10GB",
	}
	if got := read(t, calls); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := d.DownProject(ctx, "broken"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failed compose = %v", err)
	}
	if err := d.RemoveImages(ctx); err == nil {
		t.Error("images without a label were removed")
	}
}

func TestNoImagesNoRemoval(t *testing.T) {
	bin, calls := fakeDocker(t, "", "")
	if err := New(bin, nil).RemoveImages(context.Background(), "kluzo.workspace=ws_1"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, calls); len(got) != 1 {
		t.Errorf("calls = %v", got)
	}
}

func TestUnavailable(t *testing.T) {
	for _, bin := range []string{filepath.Join(t.TempDir(), "docker"), "kluzo-no-such-docker"} {
		if _, err := New(bin, nil).Projects(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v", bin, err)
		}
	}
}

// Probe: a daemon that answers is available; docker version without the
// server part (the daemon is unreachable) is ErrUnavailable, like a missing
// CLI.
func TestProbe(t *testing.T) {
	ctx := context.Background()
	script := func(body string) string {
		bin := filepath.Join(t.TempDir(), "docker")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	up := script("[ \"$1 $2 $3\" = 'version --format {{.Server.Version}}' ] && echo 27.3.1\n")
	if err := New(up, nil).Probe(ctx); err != nil {
		t.Errorf("daemon up: %v", err)
	}
	down := script("echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?' >&2\nexit 1\n")
	if err := New(down, nil).Probe(ctx); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "Cannot connect") {
		t.Errorf("daemon down: %v", err)
	}
	empty := script("exit 0\n") // a client that prints no server version
	if err := New(empty, nil).Probe(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no server part: %v", err)
	}
	if err := New(filepath.Join(t.TempDir(), "docker"), nil).Probe(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no CLI: %v", err)
	}
}
