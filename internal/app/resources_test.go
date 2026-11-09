package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/docker"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
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
	// A workspace of the project that is over, with its journal.
	h := e.host.fill()
	p, err := project(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := place(h.Env, p, e.dir, "v0.0")
	if err != nil {
		t.Fatal(err)
	}
	gone, _ := h.IDs.New(ids.Workspace, h.Clock.Now())
	db, err := store.Open(ctx, old.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.CreateWorkspace(ctx, db, h.IDs, h.Clock, supervisor.NewWorkspace{Created: contracts.WorkspaceCreated{ID: gone, Branch: "milestone/v0.0", BaseSHA: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.MoveWorkspace(ctx, db, h.IDs, h.Clock, core.TriggerCancelAll); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	oldTree := mkdir(old.Dir, "worktrees", "5")
	oldHome := mkdir(old.Dir, "home")
	// Another project whose name starts with this one's: its workspace for v1
	// sits in <project>-web-v1, which this project would name for "web-v1".
	other := filepath.Join(filepath.Dir(wsDir), filepath.Base(wsDir)[:len(filepath.Base(wsDir))-len("v0.1")]+"web-v1")
	odb, err := store.Open(ctx, filepath.Join(other, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.CreateWorkspace(ctx, odb, h.IDs, h.Clock, supervisor.NewWorkspace{Created: contracts.WorkspaceCreated{ID: "ws_other", Branch: "milestone/v1", BaseSHA: "x"}}); err != nil {
		t.Fatal(err)
	}
	_ = odb.Close()
	otherTree := mkdir(other, "worktrees", "4")
	unsure, _ := place(h.Env, p, e.dir, "v0.9")
	unsureTree := mkdir(unsure.Dir, "worktrees", "1")                          // no journal: never cleaned
	mkdir(e.data, "kluzo", "workspaces", "other-project-v1", "worktrees", "1") // not this project
	d, dockerCalls := fakeDocker(t,
		"sha256:a\t"+active+"\t1000\nsha256:b\t"+gone+"\t3000\nsha256:c\tws_elsewhere\t500\n",
		"kluzo-"+strings.ToLower(active)+"-1\t\nkluzo-"+strings.ToLower(gone)+"-3\t/x\nkluzo-site\t/home/u/kluzo-site\ntaw-dev\t"+oldTree+"\nother\t/elsewhere\n")
	host := e.host
	host.Docker = d

	doctor := greenDoctor(t, host.Env, func(context.Context, string, string) ([]string, error) { return nil, nil })
	doctor.Host.Env, doctor.Host.Docker = host.Env, d
	out, err := doctor.Execute(ctx, DoctorInput{Dir: e.dir})
	if err != nil || !out.OK() {
		t.Fatalf("doctor: %+v %v", out, err)
	}
	all := strings.Join(out.Warnings, "\n")
	for _, want := range []string{orphan, oldTree, "kluzo-" + strings.ToLower(gone) + "-3", "taw-dev", "sha256:b", unsure.Dir} {
		if !strings.Contains(all, want) {
			t.Errorf("doctor misses %s:\n%s", want, all)
		}
	}
	for _, not := range []string{kept + ";", otherTree, "sha256:a", "sha256:c", "kluzo-site", "other", "disk:"} {
		if strings.Contains(all, not) {
			t.Errorf("doctor reports %s:\n%s", not, all)
		}
	}

	dry, err := Clean{Host: host}.Execute(ctx, CleanInput{ConfigDir: e.dir})
	if err != nil || dry.Removed || len(dry.Checkouts) != 2 || len(dry.Leftovers) != 1 || len(dry.Projects) != 2 || len(dry.Images) != 1 || len(dry.Unsure) != 1 || dry.Dangling {
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
	marked, err := Clean{Host: host}.Execute(ctx, CleanInput{ConfigDir: e.dir, Images: true})
	if err != nil || len(marked.Images) != 3 || !marked.Dangling {
		t.Fatalf("dry run with --images: %+v %v", marked, err)
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
	for _, p := range []string{kept, unsureTree, otherTree} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	got := strings.Join(calls(t, dockerCalls), "\n")
	for _, want := range []string{"compose --project-name kluzo-" + strings.ToLower(gone) + "-3 down", "compose --project-name taw-dev down", "image rm --force sha256:b"} {
		if !strings.Contains(got, want) {
			t.Errorf("clean did not run %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"sha256:a", "sha256:c", "kluzo-site", "image prune"} {
		if strings.Contains(got, "image rm --force "+not) || strings.Contains(got, "project-name "+not) || (not == "image prune" && strings.Contains(got, not)) {
			t.Errorf("clean touched %s:\n%s", not, got)
		}
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
