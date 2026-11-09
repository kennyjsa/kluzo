package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/platform/docker"
)

// fakeDocker is a docker CLI whose daemon answers, with the images (lines
// "id\tworkspace\tsize") and the compose projects (lines "name\tdir") given;
// it records its calls in the returned file.
func fakeDocker(t *testing.T, images, projects string) (*docker.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(images), "\n") {
		if id, _, ok := strings.Cut(l, "\t"); ok {
			ids = append(ids, id)
		}
	}
	file := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncase \"$1 $2\" in\n" +
		"'version --format') echo 27.3.1 ;;\n" +
		"'image ls') cat " + file("ids", strings.Join(ids, "\n")) + " ;;\n" +
		"'image inspect') cat " + file("images", images) + " ;;\n" +
		"'ps --all') cat " + file("ps", projects) + " ;;\n" +
		"'system df') printf 'Build Cache\\t2GB\\n' ;;\nesac\n"
	bin := file("docker", script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return docker.New(bin, nil), calls
}

// calls are the docker command lines run.
func calls(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// What Kluzo left on the machine (ADR 0025): kluzo doctor reports it, kluzo
// clean lists it by default and removes it with --yes; kluzo status measures
// the disk of the workspace.
func TestDoctorCleanAndStatusSeeWhatKluzoLeft(t *testing.T) {
	ctx := context.Background()
	e := newWorkspaceEnv(t)
	if _, err := (PlanWorkspace{Host: e.host}).Execute(ctx, PlanWorkspaceInput{ConfigDir: e.dir, Milestone: "v0.1"}); err != nil {
		t.Fatal(err)
	}
	o, err := open(ctx, e.host.fill(), e.dir, "v0.1")
	if err != nil {
		t.Fatal(err)
	}
	active, wsDir := o.snap.Workspace.ID, o.place.Dir
	_ = o.db.Close()
	mkdir := func(parts ...string) string {
		dir := filepath.Join(parts...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	kept := mkdir(wsDir, "worktrees", "1")   // node 1 is planned: not an orphan
	orphan := mkdir(wsDir, "worktrees", "9") // no node 9
	gone := filepath.Join(filepath.Dir(wsDir), filepath.Base(wsDir)[:len(filepath.Base(wsDir))-len("v0.1")]+"v0.0")
	oldTree := mkdir(gone, "worktrees", "5") // a workspace without a journal is over
	oldHome := mkdir(gone, "home")
	mkdir(e.data, "kluzo", "workspaces", "other-project-v1", "worktrees", "1") // not this project
	d, dockerCalls := fakeDocker(t,
		"sha256:a\t"+active+"\t1000\nsha256:b\tws_gone\t3000\n",
		"kluzo-"+strings.ToLower(active)+"-1\t\nkluzo-ws_gone-3\t/x\ntaw-dev\t"+oldTree+"\nother\t/elsewhere\n")
	host := e.host
	host.Docker = d

	doctor := greenDoctor(t, host.Env, func(context.Context, string, string) ([]string, error) { return nil, nil })
	doctor.Host.Env, doctor.Host.Docker = host.Env, d
	out, err := doctor.Execute(ctx, DoctorInput{Dir: e.dir})
	if err != nil || !out.OK() {
		t.Fatalf("doctor: %+v %v", out, err)
	}
	all := strings.Join(out.Warnings, "\n")
	for _, want := range []string{orphan, oldTree, "kluzo-ws_gone-3", "taw-dev", "sha256:b"} {
		if !strings.Contains(all, want) {
			t.Errorf("doctor misses %s:\n%s", want, all)
		}
	}
	for _, not := range []string{kept + ";", "sha256:a", "other", "disk:"} {
		if strings.Contains(all, not) {
			t.Errorf("doctor reports %s:\n%s", not, all)
		}
	}

	dry, err := Clean{Host: host}.Execute(ctx, CleanInput{ConfigDir: e.dir})
	if err != nil || dry.Removed || len(dry.Checkouts) != 2 || len(dry.Leftovers) != 1 || len(dry.Projects) != 2 || len(dry.Images) != 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	for _, c := range calls(t, dockerCalls) {
		if strings.HasPrefix(c, "compose") || strings.HasPrefix(c, "image rm") {
			t.Errorf("the dry run removed: %s", c)
		}
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("the dry run removed the orphan: %v", err)
	}
	done, err := Clean{Host: host}.Execute(ctx, CleanInput{ConfigDir: e.dir, Yes: true})
	if err != nil || !done.Removed {
		t.Fatalf("clean: %+v %v", done, err)
	}
	for _, p := range []string{orphan, oldTree, oldHome} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there", p)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the worktree of a planned node was removed: %v", err)
	}
	got := strings.Join(calls(t, dockerCalls), "\n")
	for _, want := range []string{"compose --project-name kluzo-ws_gone-3 down", "compose --project-name taw-dev down", "image rm --force sha256:b"} {
		if !strings.Contains(got, want) {
			t.Errorf("clean did not run %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sha256:a") && strings.Contains(got, "image rm --force sha256:a") {
		t.Error("clean removed an image of an active workspace")
	}

	st, err := Status{Host: host}.Execute(ctx, StatusInput{ConfigDir: e.dir, Milestone: "v0.1"})
	if err != nil || st.Disk.Workspace < 2048 || st.Disk.Images != 1000 || st.Disk.BuildCache != 2_000_000_000 || st.Disk.Docker != "" {
		t.Errorf("status disk = %+v, %v", st.Disk, err)
	}
	// Above retention.disk_warn the doctor warns.
	cfg := filepath.Join(e.dir, "kluzo.yaml")
	raw, _ := os.ReadFile(cfg)
	if err := os.WriteFile(cfg, append(raw, []byte("\nretention:\n  disk_warn: 1MB\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "big"), make([]byte, 2_000_000), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = doctor.Execute(ctx, DoctorInput{Dir: e.dir})
	if err != nil || !strings.Contains(strings.Join(out.Warnings, "\n"), "above retention.disk_warn (1.0MB)") {
		t.Errorf("doctor above the limit: %+v %v", out.Warnings, err)
	}
}
