package supervisor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// git runs plain git in dir and returns its trimmed output.
func (e *env) git(dir string, args ...string) string {
	e.t.Helper()
	out, err := gitRun(dir, args...)
	if err != nil {
		e.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func (e *env) bare() string { return filepath.Join(e.dir, "origin.git") }

// pushToMilestone commits files to the milestone branch of the remote from
// another clone, as a person would, and returns the commit.
func (e *env) pushToMilestone(files map[string]string) string {
	e.t.Helper()
	clone := filepath.Join(e.t.TempDir(), "clone")
	e.git(filepath.Dir(clone), "clone", "-q", "-b", "milestone/v1", e.bare(), clone)
	for name, content := range files {
		path := filepath.Join(clone, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	e.git(clone, "add", "-A")
	e.git(clone, "-c", "user.name=p", "-c", "user.email=p@p", "commit", "-q", "-m", "a person's change")
	e.git(clone, "push", "-q", "origin", "milestone/v1")
	return e.git(clone, "rev-parse", "HEAD")
}

func (e *env) snapshot() (*fakes.SCM, []ports.Notification) {
	return e.scm.SCM, e.notifier.Sent()
}

// The node goes through the merge queue for real: rebase on the tip, merge
// commit with the trailers of ADR 0009 on the milestone branch, pushed with
// the bot's remote; the node is cleaned up (worktree, branch, checkout of the
// gates, compose project and images); the workspace opens the PR
// milestone → main and waits; the human merge moves it to merged and cleans
// it up. Kluzo never writes main.
func TestIntegrationAndClosing(t *testing.T) {
	e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
	mainBefore := e.git(e.bare(), "rev-parse", "main")
	e.drain()
	n, r := e.nodeRow()
	if n.State != core.NodeIntegrated || n.MergedSHA == "" {
		t.Fatalf("node %s, moves %v", n.State, e.moves())
	}
	if tip := e.git(e.repo, "rev-parse", "milestone/v1"); tip != n.MergedSHA || e.git(e.bare(), "rev-parse", "milestone/v1") != tip {
		t.Errorf("milestone local %s, remote %s, merge %s", tip, e.git(e.bare(), "rev-parse", "milestone/v1"), n.MergedSHA)
	}
	parents := strings.Fields(e.git(e.repo, "rev-list", "--parents", "-n", "1", n.MergedSHA))
	msg := e.git(e.repo, "log", "-1", "--format=%B", n.MergedSHA)
	if !slices.Equal(parents, []string{n.MergedSHA, r.Base, r.Head}) || !strings.HasPrefix(msg, "Merge #1: Agenda API") ||
		!strings.Contains(msg, "Kluzo-Node: "+n.ID+"\nKluzo-Workspace: ws_1\nKluzo-Attempt: 1") {
		t.Errorf("merge parents %v, message %q", parents, msg)
	}
	for kind, want := range map[string]int{contracts.EventMergeRebased: 1, contracts.EventMergeFreshness: 1, contracts.EventMergeDone: 1, contracts.EventAreaDeviation: 0} {
		if got := len(e.events(kind)); got != want {
			t.Errorf("%s = %d, want %d", kind, got, want)
		}
	}
	var fresh contracts.MergeFreshness
	_ = json.Unmarshal(e.events(contracts.EventMergeFreshness)[0].Payload, &fresh)
	if fresh.Rerun || fresh.ReviewedSHA != r.Base || fresh.TipSHA != r.Base {
		t.Errorf("merge.freshness = %+v", fresh)
	}
	// gate_runs, projected from gate.started/gate.finished.
	snap, err := e.db.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.GateRuns) != 2 || snap.GateRuns[0].Gate != "build" || snap.GateRuns[1].Gate != "test" ||
		snap.GateRuns[1].State != ports.GatePassed || snap.GateRuns[1].NodeID != n.ID || snap.GateRuns[1].FinishedAt == "" {
		t.Errorf("gate runs = %+v", snap.GateRuns)
	}
	// The node is cleaned up.
	ws := filepath.Join(e.dir, "ws")
	for _, gone := range []string{filepath.Join(ws, "worktrees", "1"), filepath.Join(ws, "gates", "1")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	if out, _ := gitRun(e.repo, "branch", "--list", "issue/*"); strings.TrimSpace(out) != "" {
		t.Errorf("node branch kept: %q", out)
	}
	calls := e.docker.Calls()
	for _, want := range []string{"compose --project-name kluzo-ws_1-1 down --volumes --rmi local --remove-orphans",
		"image ls --quiet --no-trunc --filter label=kluzo.workspace=ws_1 --filter label=kluzo.node=" + n.ID} {
		if !slices.Contains(calls, want) {
			t.Errorf("docker calls %q miss %q", calls, want)
		}
	}
	// The closing of v0.1: the PR milestone → main, and a notification.
	if snap.Workspace.State != core.WorkspaceReadyToMerge || snap.Workspace.PR.Ref == "" {
		t.Fatalf("workspace %s, PR %+v", snap.Workspace.State, snap.Workspace.PR)
	}
	scm, sent := e.snapshot()
	pr, err := scm.PR(context.Background(), snap.Workspace.PR.Ref)
	if err != nil || pr.Head != "milestone/v1" || pr.Base != "main" || pr.Merged || !strings.Contains(pr.Body, "#1 Agenda API") {
		t.Errorf("PR = %+v, %v", pr, err)
	}
	if last := sent[len(sent)-1]; !strings.Contains(last.Title, "ready to merge") || !strings.Contains(last.Body, pr.URL) {
		t.Errorf("last notification = %+v", last)
	}
	if e.git(e.bare(), "rev-parse", "main") != mainBefore {
		t.Error("Kluzo wrote main")
	}
	// The human merges; Kluzo observes it and cleans the workspace up.
	if err := scm.Merge(snap.Workspace.PR.Ref); err != nil {
		t.Fatal(err)
	}
	e.drain()
	snap, _ = e.db.Snapshot(context.Background())
	if snap.Workspace.State != core.WorkspaceMerged {
		t.Fatalf("workspace %s after the human merge", snap.Workspace.State)
	}
	calls = e.docker.Calls()
	for _, want := range []string{"image ls --quiet --no-trunc --filter label=kluzo.workspace=ws_1", "image prune --force", "builder prune --force --keep-storage 10GB"} {
		if !slices.Contains(calls, want) {
			t.Errorf("docker calls %q miss %q", calls, want)
		}
	}
	for _, d := range []string{"home", "cache", "gates-home", "gates-cache", "worktrees", "gates"} {
		if _, err := os.Stat(filepath.Join(ws, d)); !os.IsNotExist(err) {
			t.Errorf("ws/%s is still there", d)
		}
	}
	if _, err := os.Stat(filepath.Join(e.dir, "state.db")); err != nil {
		t.Errorf("state.db: %v", err)
	}
	if failed := e.events(contracts.EventEffectFailed); len(failed) != 0 {
		t.Errorf("effects failed: %d", len(failed))
	}
}

// pushingGates are the in-memory gates of the env; before the i-th run of
// gate, a person pushes pushes[i] to the milestone (nil: no push).
type pushingGates struct {
	e      **env
	gate   string
	pushes []map[string]string
	pushed *string // the last commit pushed
	runs   int
}

func (g *pushingGates) Run(ctx context.Context, run ports.GateRun) (ports.GateResult, error) {
	if run.Gate == g.gate {
		if g.runs < len(g.pushes) && g.pushes[g.runs] != nil {
			*g.pushed = (*g.e).pushToMilestone(g.pushes[g.runs])
		}
		g.runs++
	}
	return (*g.e).gates.Run(ctx, run)
}

// newEnvPushing is newEnv with a push to the milestone before the first run
// of the full gates.
func newEnvPushing(t *testing.T, files map[string]string, pushed *string, steps ...step) *env {
	return newEnvPushes(t, []map[string]string{files}, pushed, steps...)
}

func newEnvPushes(t *testing.T, pushes []map[string]string, pushed *string, steps ...step) *env {
	var e *env
	opts := envOpts{gates: func(string, string) ports.Gates {
		return &pushingGates{e: &e, gate: "test", pushes: pushes, pushed: pushed}
	}}
	e = newEnvWith(t, opts, steps...)
	return e
}

// A push to the milestone after the rebase (here, while the freshness gates
// run) is caught before the merge: the node is rebased again on the new tip
// and judged again, never merged on a stale tip.
func TestTipMovedBeforeTheMergeRebasesAgain(t *testing.T) {
	var pushed string
	e := newEnvPushes(t, []map[string]string{{"A.md": "a\n"}, {"B.md": "b\n"}}, &pushed, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	n, _ := e.nodeRow()
	if n.State != core.NodeIntegrated || len(e.events(contracts.EventMergeRebased)) != 2 || len(e.events(contracts.EventEffectFailed)) != 0 {
		t.Fatalf("node %s, merge.rebased %d, failed %d", n.State, len(e.events(contracts.EventMergeRebased)), len(e.events(contracts.EventEffectFailed)))
	}
	if parents := strings.Fields(e.git(e.repo, "rev-list", "--parents", "-n", "1", n.MergedSHA)); len(parents) != 3 || parents[1] != pushed {
		t.Errorf("merge parents = %v, want the last push %s first", parents, pushed)
	}
	if e.git(e.bare(), "rev-parse", "milestone/v1") != n.MergedSHA {
		t.Error("the merge was not pushed")
	}
}

// The milestone moved after the review (a person pushed to it): the rebase
// puts the node on the new tip and the full gates run again on it before the
// merge (F13: freshness gate).
func TestFreshnessGateRunsAgainAfterTheMilestoneMoved(t *testing.T) {
	var pushed string
	e := newEnvPushing(t, map[string]string{"NOTES.md": "notes\n"}, &pushed, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	n, r := e.nodeRow()
	if n.State != core.NodeIntegrated {
		t.Fatalf("node %s, moves %v", n.State, e.moves())
	}
	runs := e.gates.Runs()
	if len(runs) != 3 || runs[2].Gate != "test" || runs[2].SHA != r.Head || runs[1].SHA == r.Head {
		t.Errorf("gate runs = %+v, head %s", runs, r.Head)
	}
	var fresh contracts.MergeFreshness
	_ = json.Unmarshal(e.events(contracts.EventMergeFreshness)[0].Payload, &fresh)
	if !fresh.Rerun || fresh.TipSHA != pushed || fresh.ReviewedSHA != r.Base {
		t.Errorf("merge.freshness = %+v, pushed %s", fresh, pushed)
	}
	if parents := strings.Fields(e.git(e.repo, "rev-list", "--parents", "-n", "1", n.MergedSHA)); len(parents) != 3 || parents[1] != pushed {
		t.Errorf("merge parents = %v, want the pushed tip first", parents)
	}
	if e.git(e.bare(), "rev-parse", "milestone/v1") != n.MergedSHA {
		t.Error("the merge was not pushed")
	}
}

// Red freshness gates send the node back to the implementer, in a new
// attempt, with what changed in the milestone.
func TestRedFreshnessGateGoesBackToTheImplementer(t *testing.T) {
	var pushed string
	e := newEnvPushing(t, map[string]string{"NOTES.md": "notes\n"}, &pushed,
		impl(implemented()), rev(reviewed(true, nil)), impl(implemented()), rev(reviewed(true, nil)))
	e.gates.Queue("test", ports.GateResult{State: ports.GatePassed}, ports.GateResult{State: ports.GateFailed})
	e.drain()
	n, _ := e.nodeRow()
	calls := e.runtime.Calls()
	if n.State != core.NodeIntegrated || n.Attempt != 2 || len(calls) != 4 {
		t.Fatalf("node %s attempt %d, %d calls, moves %v", n.State, n.Attempt, len(calls), e.moves())
	}
	if p := calls[2].prompt; !strings.Contains(p, "### Integration failure") || !strings.Contains(p, "The milestone changed since the review") || !strings.Contains(p, "NOTES.md") {
		t.Errorf("prompt of attempt 2:\n%s", p)
	}
	if !slices.Contains(e.moves(), "integrating") || e.moves()[6] != "running" {
		t.Errorf("moves = %v", e.moves())
	}
}

// A rebase conflict goes back to the implementer as the integration cause; the
// next attempt starts from the new tip and integrates.
func TestRebaseConflictGoesBackToTheImplementer(t *testing.T) {
	var pushed string
	e := newEnvPushing(t, map[string]string{"agenda.go": "package agenda // from the milestone\n"}, &pushed,
		impl(implemented()), rev(reviewed(true, nil)), impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	n, r := e.nodeRow()
	calls := e.runtime.Calls()
	if n.State != core.NodeIntegrated || n.Attempt != 2 || len(calls) != 4 {
		t.Fatalf("node %s attempt %d, %d calls, moves %v", n.State, n.Attempt, len(calls), e.moves())
	}
	if p := calls[2].prompt; !strings.Contains(p, "Rebase conflict in") || !strings.Contains(p, "agenda.go") || !strings.Contains(p, "<<<<<<<") {
		t.Errorf("prompt of attempt 2:\n%s", p)
	}
	if r.Base != pushed || len(e.events(contracts.EventMergeRebased)) != 1 {
		t.Errorf("attempt 2 base %s, pushed %s, merge.rebased %d", r.Base, pushed, len(e.events(contracts.EventMergeRebased)))
	}
	if got := e.git(e.repo, "show", n.MergedSHA+":agenda.go"); got != "package agenda" {
		t.Errorf("agenda.go at the merge = %q", got)
	}
}

// The diff touched an area the node did not declare: area.deviation, and the
// area is added to the node (ADR 0011).
func TestAreaDeviation(t *testing.T) {
	e := newEnvWith(t, envOpts{prepare: func(repo string) {
		raw, _ := os.ReadFile(filepath.Join(repo, ".kluzo", "kluzo.yaml"))
		onMilestone(t, repo, map[string]string{".kluzo/kluzo.yaml": string(raw) + "\nareas:\n  agenda: [\"agenda/**\"]\n  auth: [\"auth/**\"]\n"})
	}}, step{role: core.RoleImplementer, results: []any{implemented()}, files: map[string]string{"agenda/list.go": "package agenda\n", "auth/token.go": "package auth\n", "agenda.go": "package agenda\n"}},
		rev(reviewed(true, nil)))
	e.drain()
	n, _ := e.nodeRow()
	devs := e.events(contracts.EventAreaDeviation)
	if n.State != core.NodeIntegrated || len(devs) != 1 || !slices.Equal(n.Areas, []string{"agenda", "auth"}) {
		t.Fatalf("node %s, areas %v, deviations %d", n.State, n.Areas, len(devs))
	}
	var d contracts.AreaDeviation
	_ = json.Unmarshal(devs[0].Payload, &d)
	if !slices.Equal(d.Declared, []string{"agenda"}) || !slices.Equal(d.Touched, []string{"agenda", "auth", "shared"}) || !slices.Equal(d.Files, []string{"auth/token.go"}) {
		t.Errorf("area.deviation = %+v", d)
	}
}

// A gate that reads the CI of the tracker gets the node branch pushed first,
// with the bot's remote.
func TestNodeBranchPushedBeforeATrackerGate(t *testing.T) {
	e := newEnvWith(t, envOpts{prepare: func(repo string) {
		onMilestone(t, repo, map[string]string{".kluzo/kluzo.yaml": withGates(t, repo, "gates:\n  - name: build\n    run: \"true\"\n    fast: true\n  - name: test\n    source: tracker\n    required_checks: [ci]")})
	}}, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	runs := e.gates.Runs()
	if len(runs) != 2 || !runs[1].Tracker {
		t.Fatalf("gate runs = %+v", runs)
	}
	if got := e.git(e.bare(), "rev-parse", "issue/1-agenda-api"); got != runs[1].SHA {
		t.Errorf("node branch on the remote = %s, the gate read %s", got, runs[1].SHA)
	}
	if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
		t.Errorf("node %s", n.State)
	}
}

// The milestone branch is created or adopted on both sides when the workspace
// is created.
func TestMilestoneBranchCreatedOrAdopted(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(e *env)
	}{
		{"on the remote only: adopted", func(e *env) { e.git(e.repo, "branch", "-D", "milestone/v1") }},
		{"local only: pushed", func(e *env) { e.git(e.bare(), "branch", "-D", "milestone/v1") }},
		{"on neither side: created from main", func(e *env) {
			e.git(e.repo, "branch", "-D", "milestone/v1")
			e.git(e.bare(), "branch", "-D", "milestone/v1")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
			tt.setup(e)
			e.drain()
			if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
				t.Fatalf("node %s, moves %v, failed %d", n.State, e.moves(), len(e.events(contracts.EventEffectFailed)))
			}
			if local, remote := e.git(e.repo, "rev-parse", "milestone/v1"), e.git(e.bare(), "rev-parse", "milestone/v1"); local != remote {
				t.Errorf("local %s, remote %s", local, remote)
			}
		})
	}
	// Diverged branches stop at the first effect that needs the remote, for a
	// person to reconcile; the node does not integrate.
	t.Run("diverged: a person decides", func(t *testing.T) {
		e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
		onMilestone(t, e.repo, map[string]string{"local.txt": "x\n"})
		e.pushToMilestone(map[string]string{"remote.txt": "y\n"})
		e.drain()
		failed := e.events(contracts.EventEffectFailed)
		var p contracts.EffectFailed
		if len(failed) > 0 {
			_ = json.Unmarshal(failed[0].Payload, &p)
		}
		if p.Kind != string(core.EffectEnsureMilestone) || p.Attempts != 1 || !strings.Contains(p.LastError, "diverged") {
			t.Errorf("effect.failed = %+v", p)
		}
		if n, _ := e.nodeRow(); n.State != core.NodeIntegrating || n.MergedSHA != "" {
			t.Errorf("node %s, merged at %q", n.State, n.MergedSHA)
		}
	})
}

// Without Docker on the machine, the cleanup of Docker is skipped and the rest
// of it runs.
func TestCleanupWithoutDocker(t *testing.T) {
	e := newEnvWith(t, envOpts{noDocker: true}, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeIntegrated || len(e.events(contracts.EventEffectFailed)) != 0 {
		t.Fatalf("node %s, failed %d", n.State, len(e.events(contracts.EventEffectFailed)))
	}
	if _, err := os.Stat(filepath.Join(e.dir, "ws", "worktrees", "1")); !os.IsNotExist(err) {
		t.Error("the worktree is still there")
	}
}
