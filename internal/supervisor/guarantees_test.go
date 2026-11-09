package supervisor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gates"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// onMilestone commits files on the milestone branch, as a merge of another
// node would.
func onMilestone(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	run := testGit(t)
	run(repo, "checkout", "-q", "milestone/v1")
	for name, content := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(repo, "add", "-A")
	run(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "milestone change")
	run(repo, "checkout", "-q", "main")
}

// withGates rewrites the gates of kluzo.yaml.
func withGates(t *testing.T, repo, gatesYAML string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".kluzo", "kluzo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start, end := strings.Index(s, "gates:\n"), strings.Index(s, "milestone_gates:")
	return s[:start] + gatesYAML + "\n" + s[end:]
}

// recording wraps the real gates and calls before ahead of each run.
type recording struct {
	ports.Gates
	mu     sync.Mutex
	runs   []ports.GateRun
	before func(ports.GateRun)
}

func (r *recording) Run(ctx context.Context, run ports.GateRun) (ports.GateResult, error) {
	r.mu.Lock()
	r.runs = append(r.runs, run)
	before := r.before
	r.mu.Unlock()
	if before != nil {
		before(run)
	}
	return r.Gates.Run(ctx, run)
}

// The gates run their commands from kluzo.yaml at the milestone tip of the
// dispatch, frozen even when the milestone changes later, with only the
// environment they allow, and the logs in logs/gates.
func TestGatesAreRealAndFrozen(t *testing.T) {
	t.Setenv("DB_URL_TEST", "db")
	t.Setenv("LEAK_X", "secret")
	var rec *recording
	frozenOnce := sync.Once{}
	e := newEnvWith(t, envOpts{
		prepare: func(repo string) {
			onMilestone(t, repo, map[string]string{".gitignore": "*.flag\n", ".kluzo/kluzo.yaml": withGates(t, repo, `gates:
  - name: build
    run: test -f agenda.go && test ! -e gate.flag
    fast: true
  - name: test
    run: test "$DB_URL_TEST" = db && test -z "$LEAK_X" && echo "$COMPOSE_PROJECT_NAME" | grep -q '^kluzo-'
    env_allow: [DB_URL_TEST]`)})
		},
		gates: func(repo, dir string) ports.Gates {
			rec = &recording{Gates: gates.New(gates.Config{LogDir: filepath.Join(dir, "ws", "logs", "gates")})}
			// Once the node runs, the milestone changes its gates: the node
			// keeps the ones of its dispatch.
			rec.before = func(ports.GateRun) {
				frozenOnce.Do(func() {
					onMilestone(t, repo, map[string]string{".kluzo/kluzo.yaml": withGates(t, repo, "gates:\n  - name: build\n    run: exit 1\n    fast: true\n  - name: test\n    run: exit 1")})
				})
			}
			return rec
		},
	}, step{role: core.RoleImplementer, results: []any{implemented()},
		// An ignored file the agent leaves in its worktree never reaches the
		// checkout the gates run in.
		files: map[string]string{"agenda.go": "package agenda\n", "gate.flag": "x"}}, rev(reviewed(true, nil)))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeQueued {
		t.Fatalf("node = %s, moves %v", n.State, e.moves())
	}
	if rec.runs[0].Workdir == filepath.Join(e.dir, "ws", "worktrees", e.issue) {
		t.Errorf("the gates ran in the worktree of the node")
	}
	if len(rec.runs) != 2 || rec.runs[1].Command == "exit 1" || slices.Contains(rec.runs[1].Env, "LEAK_X=secret") || !slices.Contains(rec.runs[1].Env, "DB_URL_TEST=db") {
		t.Errorf("runs = %+v", rec.runs)
	}
	for _, ev := range e.events(contracts.EventGateFinished) {
		var g contracts.GateRun
		_ = json.Unmarshal(ev.Payload, &g)
		if g.State != ports.GatePassed || !strings.Contains(g.LogRef, filepath.Join("ws", "logs", "gates")) {
			t.Errorf("gate.finished = %+v", g)
		}
		if _, err := os.Stat(g.LogRef); err != nil {
			t.Errorf("log of %s: %v", g.Gate, err)
		}
	}
}

// A red gate goes back to the implementer with its structured failures,
// from go test -json.
func TestGateFailureReachesThePromptStructured(t *testing.T) {
	failing := `{"Action":"output","Package":"ex/agenda","Test":"TestOverlap","Output":"    conflict_test.go:41: got nil, want ErrConflict\n"}
{"Action":"fail","Package":"ex/agenda","Test":"TestOverlap"}
`
	e := newEnvWith(t, envOpts{
		prepare: func(repo string) {
			onMilestone(t, repo, map[string]string{"fail.json": failing, ".kluzo/kluzo.yaml": withGates(t, repo, `gates:
  - name: build
    run: "true"
    fast: true
  - name: test
    run: test -f fixed.txt || { cat fail.json; exit 1; }`)})
		},
		gates: func(_, dir string) ports.Gates {
			return gates.New(gates.Config{LogDir: filepath.Join(dir, "ws", "logs", "gates")})
		},
	}, impl(implemented()), rev(reviewed(true, nil)),
		step{role: core.RoleImplementer, files: map[string]string{"agenda.go": "package agenda\n", "fixed.txt": "x"}, results: []any{implemented()}},
		rev(reviewed(true, nil)))
	e.drain()
	calls := e.runtime.Calls()
	if n, _ := e.nodeRow(); n.State != core.NodeQueued || n.Attempt != 2 || len(calls) != 4 {
		t.Fatalf("node %+v, %d calls", n, len(calls))
	}
	for _, want := range []string{"Gate `test`", "exit code 1", "- TestOverlap (conflict_test.go:41): got nil, want ErrConflict"} {
		if !strings.Contains(calls[2].prompt, want) {
			t.Errorf("the prompt of attempt 2 misses %q:\n%s", want, calls[2].prompt)
		}
	}
}

// Each guarantee of the check after the round (ADR 0020, item 2), through
// the supervisor, with a real repository.
func TestGuaranteesAfterTheRound(t *testing.T) {
	test := "package agenda\n\nimport \"testing\"\n\nfunc TestList(t *testing.T) {}\n"
	prepare := func(repo string) { onMilestone(t, repo, map[string]string{"agenda_test.go": test}) }
	withFiles := func(files map[string]string, remove ...string) step {
		return step{role: core.RoleImplementer, files: files, remove: remove, results: []any{implemented()}}
	}
	for _, tt := range []struct {
		name  string
		steps []step
		check func(t *testing.T, e *env)
	}{
		{"a protected path becomes a proposal", []step{withFiles(map[string]string{"agenda.go": "package agenda\n", ".kluzo/kluzo.yaml": "version: 1\n"})}, proposal(".kluzo/kluzo.yaml is a protected path")},
		{"a deleted test becomes a proposal", []step{withFiles(work, "agenda_test.go")}, proposal("agenda_test.go is a test file and was deleted")},
		{"a disabled test becomes a proposal", []step{withFiles(map[string]string{"agenda.go": "package agenda\n",
			"agenda_test.go": strings.Replace(test, "{}", "{\n\tt.Skip(\"later\")\n}", 1)})}, proposal("a test was disabled with t.Skip(")},
		{"a new suppression is an automatic blocking finding, fixed in the next round", []step{
			withFiles(map[string]string{"agenda.go": "package agenda\n\nvar x = 1 //nolint:unused\n"}),
			rev(reviewed(true, nil)),
			{role: core.RoleImplementer, files: map[string]string{"agenda.go": "package agenda\n"}, results: []any{implemented(response("F1", "fixed"))}},
			// F1 is resolved by the check before this review: nothing to judge.
			rev(reviewed(true, []map[string]any{})),
		}, func(t *testing.T, e *env) {
			n, r := e.nodeRow()
			if n.State != core.NodeQueued || len(r.Findings.List) != 1 || !r.Findings.List[0].Auto || r.Findings.List[0].Status != core.FindingResolved {
				t.Errorf("node %s, findings %+v", n.State, r.Findings.List)
			}
			calls := e.runtime.Calls()
			if len(calls) != 4 || !strings.Contains(calls[2].prompt, "new lint suppression") {
				t.Errorf("the implementer was not sent back for the suppression: %d calls", len(calls))
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnvWith(t, envOpts{prepare: prepare}, tt.steps...)
			e.drain()
			tt.check(t, e)
		})
	}
}

// proposal checks a node blocked for a proposal, with proposal.created and the
// reason in the comment.
func proposal(reason string) func(t *testing.T, e *env) {
	return func(t *testing.T, e *env) {
		t.Helper()
		n, _ := e.nodeRow()
		if n.State != core.NodeBlocked || n.BlockedReason != core.BlockedProposal {
			t.Fatalf("node %s/%s, moves %v", n.State, n.BlockedReason, e.moves())
		}
		if len(e.events(contracts.EventProposalCreated)) != 1 || len(e.gates.Runs()) != 0 {
			t.Errorf("proposals %d, gates %d", len(e.events(contracts.EventProposalCreated)), len(e.gates.Runs()))
		}
		if c := e.comments(); !strings.Contains(c[len(c)-1], reason) {
			t.Errorf("last comment %q misses %q", c[len(c)-1], reason)
		}
	}
}

// Credentials of the host stop every session: the workspace pauses until
// they are gone or security.allow_host_credentials is set.
func TestHostCredentialsRefuseSessions(t *testing.T) {
	host := func(context.Context) ([]string, error) {
		return []string{"gh is authenticated (/home/u/.config/gh/hosts.yml)"}, nil
	}
	e := newEnvWith(t, envOpts{host: host}, impl(implemented()))
	e.drain()
	n, _ := e.nodeRow()
	snap, _ := e.db.Snapshot(context.Background())
	if n.State != core.NodeBlocked || n.BlockedReason != core.BlockedPaused || snap.Workspace.State != core.WorkspacePaused || len(e.runtime.Calls()) != 0 {
		t.Errorf("node %s/%s, workspace %s, %d sessions", n.State, n.BlockedReason, snap.Workspace.State, len(e.runtime.Calls()))
	}
	if c := e.comments(); !strings.Contains(c[len(c)-1], "gh is authenticated") {
		t.Errorf("last comment = %q", c[len(c)-1])
	}

	allow := func(repo string) {
		path := filepath.Join(repo, ".kluzo", "kluzo.yaml")
		raw, _ := os.ReadFile(path)
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "allow_host_credentials: false", "allow_host_credentials: true", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e = newEnvWith(t, envOpts{host: host, prepare: allow}, impl(implemented()), rev(reviewed(true, nil)))
	e.drain()
	if n, _ := e.nodeRow(); n.State != core.NodeQueued {
		t.Errorf("with allow_host_credentials: node %s", n.State)
	}
}
