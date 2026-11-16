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
		d.RemoveImages(ctx, LabelProject+"=kluzo-ws_1-7"),
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
		"image ls --quiet --no-trunc --filter label=com.docker.compose.project=kluzo-ws_1-7",
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
	if err := New(bin, nil).RemoveImages(context.Background(), LabelProject+"=kluzo-ws_1-7"); err != nil {
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

// The images compose projects built, with their project and size, and the size of the
// build cache, for kluzo status, doctor and clean (ADR 0025).
func TestImagesAndBuildCache(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncase \"$1 $2\" in\n" +
		"'image ls') printf 'sha256:a\\nsha256:b\\nsha256:a\\n' ;;\n" +
		"'image inspect') printf 'sha256:a\\tkluzo-ws_1-7\\t1500\\nsha256:b\\ttaw-dev\\t2500\\n' ;;\n" +
		"'system df') printf 'Images\\t3GB\\nBuild Cache\\t1.5GB\\n' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := New(bin, nil)
	images, err := d.Images(context.Background())
	if err != nil || len(images) != 2 || images[0] != (Image{"sha256:a", "kluzo-ws_1-7", 1500}) || images[1] != (Image{"sha256:b", "taw-dev", 2500}) {
		t.Fatalf("Images = %+v, %v", images, err)
	}
	if size, err := d.BuildCache(context.Background()); err != nil || size != 1_500_000_000 {
		t.Errorf("BuildCache = %d, %v", size, err)
	}
	if err := d.RemoveImageIDs(context.Background()); err != nil {
		t.Error(err)
	}
	if got := read(t, calls); len(got) != 3 || !strings.HasPrefix(got[1], "image inspect --format {{.Id}}") || !strings.HasSuffix(got[1], "{{.Size}} sha256:a sha256:b") || got[0] != "image ls --quiet --no-trunc --filter label=com.docker.compose.project" {
		t.Errorf("calls %q", got)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "12.3kB": 12_300, "20GB": 20_000_000_000, "1.5TB": 1_500_000_000_000, "500MB": 500_000_000} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "GB", "12", "12XB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) took it", in)
		}
	}
}
