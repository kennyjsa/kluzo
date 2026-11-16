package supervisor_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
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

// nodeBranch is the branch of the node of the env.
const nodeBranch = "refs/heads/issue/1-agenda-api"

// pushNodeBranch puts a branch of the node on the remote, as the push before
// a gate of the CI does.
func (e *env) pushNodeBranch() {
	e.t.Helper()
	e.git(e.repo, "push", "-q", e.bare(), "+main:"+nodeBranch)
}

// remoteNodeBranch reports whether the remote has the branch of the node.
func (e *env) remoteNodeBranch() bool {
	e.t.Helper()
	return e.git(e.bare(), "for-each-ref", nodeBranch) != ""
}

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
	e.pushNodeBranch() // as for a gate of the CI
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
	if e.remoteNodeBranch() {
		t.Error("the remote branch of the integrated node was kept")
	}
	calls := e.docker.Calls()
	for _, want := range []string{"compose --project-name kluzo-ws_1-1 down --volumes --rmi local --remove-orphans",
		"image ls --quiet --no-trunc --filter label=com.docker.compose.project=kluzo-ws_1-1"} {
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
	// The human merges; Kluzo observes it and cleans the workspace up, with
	// the node branches left on the remote.
	e.pushNodeBranch()
	if err := scm.Merge(snap.Workspace.PR.Ref); err != nil {
		t.Fatal(err)
	}
	// Images of the projects of this workspace go, by the label compose
	// writes; those of another workspace or project stay.
	images := "sha256:a\tkluzo-ws_1-1\t10\nsha256:b\tkluzo-ws_1-9\t10\nsha256:c\tkluzo-ws_2-1\t10\nsha256:d\ttaw-dev\t10\n"
	if err := os.WriteFile(e.docker.images, []byte(images), 0o644); err != nil {
		t.Fatal(err)
	}
	e.drain()
	snap, _ = e.db.Snapshot(context.Background())
	if snap.Workspace.State != core.WorkspaceMerged {
		t.Fatalf("workspace %s after the human merge", snap.Workspace.State)
	}
	calls = e.docker.Calls()
	for _, want := range []string{"image ls --quiet --no-trunc --filter label=com.docker.compose.project", "image rm --force sha256:a sha256:b", "image prune --force", "builder prune --force --keep-storage 10GB"} {
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
	if e.remoteNodeBranch() {
		t.Error("the merged workspace kept a node branch on the remote")
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
	var e *env
	seen := map[string]string{} // gate → the node branch on the remote when it ran
	e = newEnvWith(t, envOpts{
		prepare: func(repo string) {
			onMilestone(t, repo, map[string]string{".kluzo/kluzo.yaml": withGates(t, repo, "gates:\n  - name: build\n    run: \"true\"\n    fast: true\n  - name: test\n    source: tracker\n    required_checks: [ci]")})
		},
		gates: func(string, string) ports.Gates {
			return gatesFunc(func(ctx context.Context, run ports.GateRun) (ports.GateResult, error) {
				seen[run.Gate] = e.git(e.bare(), "for-each-ref", "--format=%(objectname)", nodeBranch)
				return e.gates.Run(ctx, run)
			})
		},
	}, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	runs := e.gates.Runs()
	if len(runs) != 2 || !runs[1].Tracker {
		t.Fatalf("gate runs = %+v", runs)
	}
	if seen["test"] != runs[1].SHA {
		t.Errorf("node branch on the remote = %q, the gate read %s", seen["test"], runs[1].SHA)
	}
	if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
		t.Errorf("node %s", n.State)
	}
	if e.remoteNodeBranch() {
		t.Error("the remote branch of the integrated node was kept")
	}
}

// gatesFunc is a ports.Gates from a function.
type gatesFunc func(context.Context, ports.GateRun) (ports.GateResult, error)

func (f gatesFunc) Run(ctx context.Context, run ports.GateRun) (ports.GateResult, error) {
	return f(ctx, run)
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
	// Diverged branches fail git.milestone for good: the workspace pauses with
	// a notification and dispatches nothing; a person reconciles the branch
	// and resumes, which queues git.milestone again, and only then does the
	// node run.
	t.Run("diverged: paused until a person reconciles and resumes", func(t *testing.T) {
		e := newEnv(t, impl(implemented()), rev(reviewed(true, nil)))
		onMilestone(t, e.repo, map[string]string{"local.txt": "x\n"})
		remote := e.pushToMilestone(map[string]string{"remote.txt": "y\n"})
		e.drain()
		failed := e.events(contracts.EventEffectFailed)
		var p contracts.EffectFailed
		if len(failed) > 0 {
			_ = json.Unmarshal(failed[0].Payload, &p)
		}
		if p.Kind != string(core.EffectEnsureMilestone) || p.Attempts != 1 || !strings.Contains(p.LastError, "diverged") {
			t.Errorf("effect.failed = %+v", p)
		}
		snap, _ := e.db.Snapshot(context.Background())
		if snap.Workspace.State != core.WorkspacePaused || snap.Workspace.Milestone != core.MilestoneFailed {
			t.Fatalf("workspace %s, milestone %s", snap.Workspace.State, snap.Workspace.Milestone)
		}
		if n, _ := e.nodeRow(); n.State != core.NodePlanned || len(e.moves()) != 0 {
			t.Errorf("node %s, moves %v: dispatched without the milestone branch", n.State, e.moves())
		}
		_, sent := e.snapshot()
		if !slices.ContainsFunc(sent, func(n ports.Notification) bool { return strings.Contains(n.Title, "Workspace paused") }) {
			t.Errorf("notifications %+v", sent)
		}
		// The person keeps the remote branch and resumes.
		e.git(e.repo, "checkout", "-q", "main")
		e.git(e.repo, "update-ref", "refs/heads/milestone/v1", remote)
		if err := supervisor.MoveWorkspace(context.Background(), e.db, ids.NewGenerator(rand.Reader), e.clock, core.TriggerResume); err != nil {
			t.Fatal(err)
		}
		if snap, _ = e.db.Snapshot(context.Background()); snap.Workspace.State != core.WorkspaceRunning || snap.Workspace.Milestone != core.MilestonePending {
			t.Fatalf("after the resume: workspace %s, milestone %s", snap.Workspace.State, snap.Workspace.Milestone)
		}
		e.drain()
		if n, _ := e.nodeRow(); n.State != core.NodeIntegrated {
			t.Fatalf("node %s after the resume, moves %v", n.State, e.moves())
		}
		if !strings.Contains(e.git(e.bare(), "log", "--format=%s", "milestone/v1"), "a person's change") {
			t.Error("the reconciled branch was not kept")
		}
	})
}

// Without Docker on the machine, or with its daemon unreachable, the cleanup
// of Docker is skipped and the rest of it runs (docs/contracts/data-model.md).
func TestCleanupWithoutDocker(t *testing.T) {
	for _, o := range []envOpts{{noDocker: true}, {dockerDown: true}} {
		e := newEnvWith(t, o, impl(implemented()), rev(reviewed(true, nil)))
		e.drain()
		if n, _ := e.nodeRow(); n.State != core.NodeIntegrated || len(e.events(contracts.EventEffectFailed)) != 0 {
			t.Fatalf("%+v: node %s, failed %d", o, n.State, len(e.events(contracts.EventEffectFailed)))
		}
		if _, err := os.Stat(filepath.Join(e.dir, "ws", "worktrees", "1")); !os.IsNotExist(err) {
			t.Errorf("%+v: the worktree is still there", o)
		}
		for _, c := range e.docker.Calls() {
			if !strings.HasPrefix(c, "version ") {
				t.Errorf("%+v: docker %s ran without a daemon", o, c)
			}
		}
	}
}

// cancelWorkspace cancels the node, whose branch is on the remote, cleans
// it up, then cancels the workspace.
func (e *env) cancelWorkspace() {
	e.t.Helper()
	e.pushNodeBranch()
	ctx := context.Background()
	gen := ids.NewGenerator(rand.Reader)
	if err := e.db.Tx(ctx, func(x *store.Tx) error {
		ev, _ := gen.New(ids.Event, e.clock.Now())
		ef, _ := gen.New(ids.Effect, e.clock.Now())
		body, _ := json.Marshal(map[string]any{"node": e.node, "issue": e.issue, "do": core.Effect{Kind: core.EffectCleanupNode, Key: "cleanup"}})
		_, err := x.TransitionNode(e.node, core.NodeEvent{Trigger: core.TriggerCancel}, core.Limits{MaxAttempts: 2}, store.Header{ID: ev, TS: e.clock.Now()},
			store.EffectSpec{ID: ef, Kind: string(core.EffectCleanupNode), Key: "cleanup", Payload: body})
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeCancelled || !e.remoteNodeBranch() {
		e.t.Fatalf("node %s; remote branch kept %v", n.State, e.remoteNodeBranch())
	}
	if err := supervisor.MoveWorkspace(ctx, e.db, gen, e.clock, core.TriggerCancelAll); err != nil {
		e.t.Fatal(err)
	}
}

// kept counts the notifications of the node branches a cancelled workspace
// kept on the remote.
func (e *env) kept() int {
	_, sent := e.snapshot()
	n := 0
	for _, s := range sent {
		if strings.Contains(s.Title, "cancelled") && strings.Contains(s.Body, "issue/1-agenda-api") {
			n++
		}
	}
	return n
}

// A cancelled node keeps its branch on the remote, the only copy of its work;
// the cancelled workspace keeps it too and lists it in a notification
// (docs/contracts/data-model.md, cleanup rules).
func TestCancelledKeepsRemoteBranches(t *testing.T) {
	e := newEnv(t)
	e.cancelWorkspace()
	e.drain()
	if !e.remoteNodeBranch() || len(e.events(contracts.EventEffectFailed)) != 0 {
		t.Fatalf("remote branch kept %v; failed %d", e.remoteNodeBranch(), len(e.events(contracts.EventEffectFailed)))
	}
	if n := e.kept(); n != 1 {
		_, sent := e.snapshot()
		t.Errorf("%d notifications of the kept branches: %+v", n, sent)
	}
}

// The notification of the kept branches is a notify effect queued in the
// transaction that completes cleanup.workspace: a crash in the middle of the
// cleanup sends nothing, the cleanup runs again, and the notification goes
// out once (Q63).
func TestCancelledCleanupNotifiesOnceAfterACrash(t *testing.T) {
	e := newEnv(t)
	e.cancelWorkspace()
	// A Kluzo that dies while it lists the branches kept on the remote.
	ctx, crash := context.WithCancel(context.Background())
	d, c := assemble(t, e.dir, deps{db: e.db, clock: e.clock, ids: ids.NewGenerator(rand.Reader), tracker: e.tracker, runtime: e.runtime,
		gates: e.gates, notifier: e.notifier, scm: e.scm, docker: e.docker}, e.issue, "")
	d.GitToken = func(context.Context) (string, error) { crash(); return "", nil }
	dying, err := supervisor.New(d, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dying.Step(ctx); err == nil {
		t.Fatal("the cleanup survived the crash")
	}
	pending := func(kind core.EffectKind) int {
		fs, err := e.db.PendingEffects(context.Background(), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range fs {
			if f.Kind == string(kind) {
				n++
			}
		}
		return n
	}
	if e.kept() != 0 || pending(core.EffectCleanupWorkspace) != 1 || pending(core.EffectNotify) != 0 {
		t.Fatalf("after the crash: %d notifications, cleanup pending %d, notify pending %d", e.kept(), pending(core.EffectCleanupWorkspace), pending(core.EffectNotify))
	}
	// Started again: the cleanup completes and queues the notification, which
	// goes out on its own, later.
	if _, err := e.sup.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.kept() != 0 || pending(core.EffectCleanupWorkspace) != 0 || pending(core.EffectNotify) != 1 {
		t.Fatalf("after the cleanup: %d notifications, cleanup pending %d, notify pending %d", e.kept(), pending(core.EffectCleanupWorkspace), pending(core.EffectNotify))
	}
	e.drain()
	if n := e.kept(); n != 1 || !e.remoteNodeBranch() {
		t.Errorf("%d notifications of the kept branches; remote branch kept %v", n, e.remoteNodeBranch())
	}
}
