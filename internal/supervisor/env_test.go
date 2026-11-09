package supervisor_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/gitops"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/docker"
	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/store"
	"github.com/kennyjsa/kluzo/internal/supervisor"
)

var epoch = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// env is a workspace in a real temporary git repository, with the in-memory
// tracker, gates and notifier, a scripted runtime and the real MCP server.
type env struct {
	t        *testing.T
	dir      string // holds repo/ and the workspace directory ws/
	repo     string
	db       *store.DB
	clock    *clock.Manual
	tracker  *fakes.Tracker
	gates    *fakes.Gates
	notifier *fakes.Notifier
	runtime  *script
	scm      *repoSCM
	docker   *fakeDocker
	sup      *supervisor.Supervisor
	node     string // id of the node
	issue    string // its issue
}

// gitRun runs plain git, for the setup of the repository and the tampering
// of a scripted session.
func gitRun(dir string, args ...string) (string, error) {
	env := []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + dir}
	o, err := exec.Run(context.Background(), exec.Spec{Bin: "git", Args: args, Dir: dir, Env: env})
	if err == nil && o.Status.Code != 0 {
		err = fmt.Errorf("exit %d", o.Status.Code)
	}
	return o.Stdout + o.Stderr, err
}

// newEnv builds a repository with the default .kluzo/ on the milestone
// branch, a running workspace and one planned node with a budget of US$ 6.
func newEnv(t *testing.T, steps ...step) *env { return newEnvBudget(t, 6_000_000, steps...) }

func newEnvBudget(t *testing.T, budget int64, steps ...step) *env {
	t.Helper()
	return newEnvWith(t, envOpts{budget: budget}, steps...)
}

// envOpts are the choices of newEnvWith.
type envOpts struct {
	budget   int64 // of the node; 0: US$ 6
	noDocker bool  // the machine has no docker
	// dockerDown: the docker CLI is there, its daemon does not answer.
	dockerDown bool
	// fromPlan creates the workspace as kluzo plan does, awaiting approval.
	fromPlan bool
	gates    func(repo, dir string) ports.Gates      // nil: the in-memory fake
	host     func(context.Context) ([]string, error) // nil: a clean host
	// prepare changes the repository before the supervisor loads it.
	prepare func(repo string)
}

func newEnvWith(t *testing.T, o envOpts, steps ...step) *env {
	t.Helper()
	if o.budget == 0 {
		o.budget = 6_000_000
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	makeRepo(t, repo)
	if o.prepare != nil {
		o.prepare(repo)
	}
	scm := newRemote(t, dir)
	budget := o.budget
	db := openDB(t, filepath.Join(dir, "state.db"))
	clk := clock.NewManual(epoch)
	gen := ids.NewGenerator(rand.Reader)
	tracker := fakes.NewTracker(clk)
	issue, err := tracker.CreateIssue(context.Background(), ports.NewIssue{Title: "Agenda API", Body: "Create the agenda endpoints."}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: dir, repo: repo, db: db, clock: clk, tracker: tracker, gates: fakes.NewGates(), notifier: &fakes.Notifier{}, scm: scm, docker: newFakeDocker(t),
		runtime: &script{t: t, repo: repo, steps: steps, refs: map[string]bool{}}, issue: issue.Ref}
	if o.fromPlan {
		e.node = seedPlan(t, db, gen, clk, issue.Ref, budget)
	} else {
		e.node = seed(t, db, gen, issue.Ref, budget)
	}
	if o.noDocker {
		e.docker.bin = filepath.Join(dir, "no-docker")
	}
	if o.dockerDown {
		e.docker.down(t)
	}
	var gates ports.Gates = e.gates
	if o.gates != nil {
		gates = o.gates(repo, dir)
	}
	e.sup = build(t, dir, deps{db: db, clock: clk, ids: gen, tracker: tracker, runtime: e.runtime, gates: gates, notifier: e.notifier, host: o.host,
		scm: scm, docker: e.docker}, issue.Ref)
	return e
}

// makeRepo creates the repository: main with the default .kluzo/ and an
// AGENTS.md, and the milestone branch at the same commit.
func makeRepo(t *testing.T, repo string) {
	t.Helper()
	run := testGit(t)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init", "-q", "-b", "main")
	if _, err := config.Init(filepath.Join(repo, ".kluzo")); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(repo, ".kluzo", "kluzo.yaml")
	raw, _ := os.ReadFile(cfgPath)
	raw = []byte(strings.NewReplacer("__PROJECT_NAME__", "demo", "OWNER/REPO", "acme/demo").Replace(string(raw)))
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", "-A")
	run(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	run(repo, "branch", "milestone/v1")
}

func openDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// deps are the parts of a supervisor a test chooses.
type deps struct {
	db       *store.DB
	clock    clock.Clock
	ids      *ids.Generator
	tracker  ports.Tracker
	runtime  ports.Runtime
	gates    ports.Gates
	notifier ports.Notifier
	host     func(context.Context) ([]string, error) // nil: a clean host
	scm      ports.SCM                               // nil: a repoSCM on dir/origin.git
	docker   *fakeDocker                             // nil: a docker that records its calls
}

// build starts the MCP server and returns the supervisor of the workspace
// whose repository is dir/repo; its work goes under dir/ws.
func build(t *testing.T, dir string, d deps, issue string) *supervisor.Supervisor {
	t.Helper()
	return buildWith(t, dir, d, issue, "")
}

// buildWith is build with every role on model (empty: the roles' own).
func buildWith(t *testing.T, dir string, d deps, issue, model string) *supervisor.Supervisor {
	t.Helper()
	sup, err := supervisor.New(assemble(t, dir, d, issue, model))
	if err != nil {
		t.Fatal(err)
	}
	return sup
}

// assemble returns the dependencies and the configuration of the supervisor
// of build.
func assemble(t *testing.T, dir string, d deps, issue, model string) (supervisor.Deps, supervisor.Config) {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	project, err := config.Load(filepath.Join(repo, ".kluzo"))
	if err != nil {
		t.Fatal(err)
	}
	if model != "" {
		for _, r := range project.Roles {
			r.Model = model
		}
	}
	if d.host == nil {
		d.host = func(context.Context) ([]string, error) { return nil, nil }
	}
	if d.scm == nil {
		d.scm = &repoSCM{SCM: fakes.NewSCM("main"), bare: filepath.Join(dir, "origin.git")}
	}
	if d.docker == nil {
		d.docker = newFakeDocker(t)
	}
	srv := mcp.New(&supervisor.Recorder{DB: d.db, Clock: d.clock, IDs: d.ids}, rand.Reader, "test")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return supervisor.Deps{
		DB: d.db, Clock: d.clock, IDs: d.ids, Tracker: d.tracker, Runtime: d.runtime, MCP: srv,
		Git: gitops.New(gitops.Config{ParentEnv: os.Environ()}), Gates: d.gates, Notifier: d.notifier,
		Nonce: func() (string, error) { return "0123abcd", nil }, HostCredentials: d.host,
		SCM: d.scm, Docker: docker.New(d.docker.bin, nil),
	}, supervisor.Config{
		Project: project, Workspace: "ws_1", Milestone: "v1", Repo: repo, Remote: filepath.Join(dir, "origin.git"), Branch: "milestone/v1", Dir: filepath.Join(dir, "ws"),
		Downloads: filepath.Join(dir, "downloads"), Criteria: map[string][]core.Criterion{issue: {{ID: "AC1", Text: "The agenda lists appointments"}}},
		Idle: 10 * time.Millisecond, Version: "test", ParentEnv: os.Environ(),
	}
}

// testGit returns a runner of plain git for the setup of the repository.
func testGit(t *testing.T) func(dir string, args ...string) {
	return func(dir string, args ...string) {
		t.Helper()
		if out, err := gitRun(dir, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// seed journals an approved workspace with one node and returns the node id.
func seed(t *testing.T, db *store.DB, gen *ids.Generator, issue string, budget int64) string {
	t.Helper()
	ctx := context.Background()
	hdr := func() store.Header {
		id, err := gen.New(ids.Event, epoch)
		if err != nil {
			t.Fatal(err)
		}
		return store.Header{ID: id, TS: epoch}
	}
	node, _ := gen.New(ids.Node, epoch)
	err := db.Tx(ctx, func(x *store.Tx) error {
		body, _ := json.Marshal(contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v1", BaseSHA: "x"})
		if _, err := x.Append(store.Event{Header: hdr(), Kind: contracts.EventWorkspaceCreated, Payload: body}); err != nil {
			return err
		}
		// The creation creates or adopts the milestone branch (git.milestone).
		ef, _ := gen.New(ids.Effect, epoch)
		milestone := store.EffectSpec{ID: ef, Kind: string(core.EffectEnsureMilestone), Key: "milestone", Payload: json.RawMessage(`{"do":{"kind":"git.milestone","key":"milestone"}}`)}
		if _, err := x.TransitionWorkspace(core.TriggerCreateWithPlan, hdr(), milestone); err != nil {
			return err
		}
		// Approved: the supervisor starts it once the milestone branch is ready.
		if _, err := x.TransitionWorkspace(core.TriggerPlanApproved, hdr()); err != nil {
			return err
		}
		body, _ = json.Marshal(contracts.NodeAdded{IssueRef: issue, Title: "Agenda API", Areas: []string{"agenda"},
			Budget: contracts.NodeBudget{Attempts: 2, Minutes: 60, USDMicros: budget}})
		_, err := x.Append(store.Event{Header: hdr(), Kind: contracts.EventNodeAdded, NodeID: node, Payload: body})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// seedPlan creates the workspace with one node as kluzo plan does, through
// supervisor.CreateWorkspace, and returns the node id.
func seedPlan(t *testing.T, db *store.DB, gen *ids.Generator, clk clock.Clock, issue string, budget int64) string {
	t.Helper()
	node, _ := gen.New(ids.Node, epoch)
	err := supervisor.CreateWorkspace(context.Background(), db, gen, clk, supervisor.NewWorkspace{
		Created: contracts.WorkspaceCreated{ID: "ws_1", Branch: "milestone/v1", BaseSHA: "x"},
		Nodes: []supervisor.NewNode{{ID: node, Added: contracts.NodeAdded{IssueRef: issue, Title: "Agenda API", Areas: []string{"agenda"},
			Budget: contracts.NodeBudget{Attempts: 2, Minutes: 60, USDMicros: budget}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// drain runs the supervisor until nothing is left to do, moving the clock
// past every backoff.
func (e *env) drain() {
	e.t.Helper()
	for i := 0; i < 500; i++ {
		busy, err := e.sup.Step(context.Background())
		if err != nil {
			e.t.Fatalf("step %d: %v", i, err)
		}
		if !busy {
			e.clock.Advance(10 * time.Minute)
			if busy, err = e.sup.Step(context.Background()); err != nil {
				e.t.Fatalf("step %d: %v", i, err)
			}
			if !busy {
				return
			}
		}
	}
	e.t.Fatal("the supervisor did not settle")
}

// nodeRow returns the projection of the node.
func (e *env) nodeRow() (store.NodeRow, core.Run) {
	e.t.Helper()
	snap, err := e.db.Snapshot(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, n := range snap.Nodes {
		if n.ID == e.node {
			var r core.Run
			if len(n.Flow) > 0 {
				if err := json.Unmarshal(n.Flow, &r); err != nil {
					e.t.Fatal(err)
				}
			}
			return n, r
		}
	}
	e.t.Fatal("node not found")
	return store.NodeRow{}, core.Run{}
}

// events returns the journal entries of a kind.
func (e *env) events(kind string) []store.Event {
	e.t.Helper()
	evs, err := e.db.Events(context.Background(), 0, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// moves returns the node states the journal recorded, in order.
func (e *env) moves() []string {
	var out []string
	for _, ev := range e.events(contracts.EventNodeTransition) {
		var p contracts.NodeTransition
		_ = json.Unmarshal(ev.Payload, &p)
		out = append(out, p.To)
	}
	return out
}

// comments returns the comments posted on the issue.
func (e *env) comments() []string {
	e.t.Helper()
	cs, _, err := e.tracker.CommentsSince(context.Background(), e.issue, "")
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Body
	}
	return out
}

// step is one scripted harness session.
type step struct {
	role    string
	results []any             // delivered through kluzo_result, in order (the second is a correction)
	files   map[string]string // written in the worktree first
	remove  []string          // deleted from the worktree
	cause   ports.Cause       // the session fails with this cause, without a result
	tamper  bool              // the session creates a branch outside its own
	lock    bool              // the session leaves the index of its worktree locked: the commit fails
	hang    bool              // the session runs until it is cancelled
}

// call is what the scripted runtime was asked.
type call struct {
	role    string
	resume  bool
	counter int
	prompt  string
	budget  int64
}

// script is a ports.Runtime that plays the steps in order, through the real
// MCP endpoint of each session.
type script struct {
	t     *testing.T
	repo  string
	mu    sync.Mutex
	steps []step
	// byRole plays a fixed step per role once steps run out.
	byRole map[string]step
	// announce, when set, is told each session start (the crash test kills
	// the process there).
	announce func()
	// waited, when set, is told when the supervisor collects the result of a
	// session: its events, session.finished included, are in the journal.
	waited func()
	calls  []call
	refs   map[string]bool
}

func (*script) Name() string { return "script" }

func (*script) Check(context.Context) (ports.RuntimeInfo, error) {
	return ports.RuntimeInfo{Version: "1", Authenticated: true}, nil
}

func (s *script) Start(ctx context.Context, spec ports.SessionSpec) (ports.Session, error) {
	return s.begin(ctx, spec, fmt.Sprintf("ref-%s-%d", spec.SessionID, spec.Counter), false)
}

func (s *script) Resume(ctx context.Context, ref string, spec ports.SessionSpec) (ports.Session, error) {
	s.mu.Lock()
	known := s.refs[ref]
	s.mu.Unlock()
	if !known {
		return nil, ports.ErrResumeMismatch
	}
	return s.begin(ctx, spec, ref, true)
}

func (s *script) begin(ctx context.Context, spec ports.SessionSpec, ref string, resume bool) (ports.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{role: spec.Role.Name, resume: resume, counter: spec.Counter, prompt: spec.Prompt, budget: spec.MaxBudgetUSDMicros})
	st, ok := s.byRole[spec.Role.Name]
	switch {
	case len(s.steps) > 0:
		st, s.steps = s.steps[0], s.steps[1:]
	case !ok:
		s.t.Errorf("no step left for a %s session", spec.Role.Name)
		return nil, fmt.Errorf("script exhausted: %w", ports.ErrPermanent)
	}
	if s.announce != nil {
		s.announce()
	}
	if st.role != spec.Role.Name {
		s.t.Errorf("session of %s, the script expected %s", spec.Role.Name, st.role)
	}
	s.refs[ref] = true
	sess := &played{ref: ref, events: make(chan ports.Event, 4), done: make(chan struct{}), cancel: make(chan struct{}), waited: s.waited}
	go sess.play(ctx, s.t, s.repo, spec, st)
	return sess, nil
}

func (s *script) Calls() []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]call(nil), s.calls...)
}

type played struct {
	ref    string
	events chan ports.Event
	done   chan struct{}
	cancel chan struct{}
	once   sync.Once
	result ports.Result
	waited func()
}

func (p *played) play(ctx context.Context, t *testing.T, repo string, spec ports.SessionSpec, st step) {
	defer close(p.done)
	defer close(p.events)
	started, _ := json.Marshal(contracts.SessionStarted{Role: spec.Role.Name, Harness: "script"})
	p.events <- ports.Event{Kind: contracts.EventSessionStarted, Payload: started}
	for _, name := range st.remove {
		if err := os.Remove(filepath.Join(spec.Workdir, name)); err != nil {
			t.Error(err)
		}
	}
	for name, content := range st.files {
		edited, _ := json.Marshal(contracts.FileEdited{Path: name, Added: 1})
		p.events <- ports.Event{Kind: contracts.EventFileEdited, Payload: edited}
		path := filepath.Join(spec.Workdir, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Error(err)
		}
	}
	if st.lock {
		admin := filepath.Join(repo, ".git", "worktrees", filepath.Base(spec.Workdir), "index.lock")
		if err := os.WriteFile(admin, nil, 0o644); err != nil {
			t.Error(err)
		}
	}
	if st.tamper {
		if out, err := gitRun(repo, "branch", "stolen", "main"); err != nil {
			t.Errorf("tamper: %v %s", err, out)
		}
	}
	usage := ports.Usage{CostUSD: 10_000 + spec.PriorUsage.CostUSD}
	p.result = ports.Result{State: ports.StateFinished, CostUSD: 10_000, Cumulative: usage}
	switch {
	case st.hang:
		select {
		case <-p.cancel:
		case <-ctx.Done():
		}
		p.result = ports.Result{State: ports.StateCancelled, CostUSD: 10_000, Cumulative: usage}
	case st.cause != "":
		p.result = ports.Result{State: ports.StateFailed, Cause: st.cause, Detail: "scripted " + string(st.cause), CostUSD: 10_000, Cumulative: usage}
	default:
		deliver(ctx, t, spec, st.results)
		if raw, ok := spec.Accepted(); ok {
			p.result.Raw = raw
		} else {
			p.result = ports.Result{State: ports.StateFailed, Cause: ports.CauseNoResult, CostUSD: 10_000, Cumulative: usage}
		}
	}
	finished, _ := json.Marshal(contracts.SessionFinished{State: p.result.State, Cause: string(p.result.Cause), CostUSDMicros: p.result.CostUSD, Result: p.result.Raw})
	p.events <- ports.Event{Kind: contracts.EventSessionFinished, Payload: finished}
}

// deliver calls kluzo_result with each result, as an agent would.
func deliver(ctx context.Context, t *testing.T, spec ports.SessionSpec, results []any) {
	c := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: spec.MCPEndpoint, HTTPClient: &http.Client{Transport: bearer(spec.MCPToken)}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Errorf("connect to the MCP of the session: %v", err)
		return
	}
	defer func() { _ = cs.Close() }()
	for _, r := range results {
		if _, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: mcp.ToolResult, Arguments: r}); err != nil {
			t.Errorf("kluzo_result: %v", err)
		}
	}
}

type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func (p *played) HarnessRef() string                 { return p.ref }
func (p *played) Events() <-chan ports.Event         { return p.events }
func (p *played) Send(context.Context, string) error { return nil }
func (p *played) Cancel() error                      { p.once.Do(func() { close(p.cancel) }); return nil }
func (p *played) Wait() (ports.Result, error) {
	<-p.done
	if p.waited != nil {
		p.waited()
	}
	return p.result, nil
}

// Results the scripted sessions deliver.

func implemented(responses ...map[string]any) map[string]any {
	if responses == nil {
		responses = []map[string]any{}
	}
	return map[string]any{"v": 1, "status": "done", "summary": "done", "files_changed": []string{"agenda.go"},
		"tests":    map[string]any{"added": 1, "command": "go test ./..."},
		"criteria": []map[string]any{{"id": "AC1", "evidence": []string{"agenda.go:1"}}}, "responses": responses}
}

func reviewed(met bool, previous []map[string]any, findings ...map[string]any) map[string]any {
	if findings == nil {
		findings = []map[string]any{}
	}
	r := map[string]any{"v": 1, "summary": "reviewed", "findings": findings,
		"criteria": []map[string]any{{"id": "AC1", "met": met, "evidence": []string{"agenda.go:1"}}}}
	if previous != nil {
		r["previous"] = previous
	}
	return r
}

func response(id, action string) map[string]any {
	return map[string]any{"id": id, "action": action, "note": "n"}
}
func previous(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "note": "n"}
}

var bug = map[string]any{"severity": "blocking", "category": "correctness", "file": "agenda.go", "line": 1, "message": "off by one", "check": "go test ./..."}

var work = map[string]string{"agenda.go": "package agenda\n"}

func impl(results ...any) step {
	return step{role: core.RoleImplementer, files: work, results: results}
}

func rev(results ...any) step { return step{role: core.RoleReviewer, results: results} }
