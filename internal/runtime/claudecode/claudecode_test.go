package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/ports"
)

const goodFlags = "--input-format --output-format --verbose --restricted --strict-mcp-config --disable-slash-commands " +
	"--session-id --resume --model --effort --max-budget-usd --exclude-dynamic-system-prompt-sections " +
	"--tools --allowedTools --permission-mode --permission-prompts --mcp-config --settings --include-hook-events"

type counter struct {
	mu sync.Mutex
	n  byte
}

func (c *counter) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	for i := range p {
		p[i] = c.n + byte(i)
	}
	return len(p), nil
}

type recorder struct {
	mu     sync.Mutex
	events []mcp.CallEvent
	denied []mcp.DenyEvent
}

func (r *recorder) Denied(_ context.Context, e mcp.DenyEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = append(r.denied, e)
}

func (r *recorder) Call(_ context.Context, e mcp.CallEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// env is a runtime wired to a fake claude and a real MCP server.
type env struct {
	t       *testing.T
	rt      *Runtime
	srv     *mcp.Server
	rec     *recorder
	dir     string
	workdir string
	n       int
}

// fakeBin writes the wrapper script that runs the test binary as the claude CLI.
func fakeBin(t *testing.T, vars map[string]string) string {
	t.Helper()
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport GORACE=atexit_sleep_ms=0 KLUZO_FAKE_CLAUDE=1 KLUZO_FAKE_TESTDATA='" + testdata + "'\n"
	for k, v := range vars {
		script += fmt.Sprintf("export %s='%s'\n", k, v)
	}
	script += "exec '" + exe + "' \"$@\"\n"
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func newEnv(t *testing.T, tweak ...func(*Config)) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Bin:        fakeBin(t, map[string]string{"KLUZO_FAKE_FLAGS": goodFlags}),
		Home:       filepath.Join(dir, "home"),
		ConfigDir:  filepath.Join(dir, "config"),
		LogDir:     filepath.Join(dir, "logs"),
		ParentEnv:  []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "SSH_AUTH_SOCK=/tmp/agent", "KLUZO_GH_TOKEN=ghp_hostsecret0000000000000000", "DATABASE_URL_TEST=postgres://test"},
		Credential: Credential{Name: "ANTHROPIC_API_KEY", Value: "sk-ant-test-credential-1234"},
		Clock:      clock.NewManual(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)),
		KillGrace:  300 * time.Millisecond,
	}
	for _, f := range tweak {
		f(&cfg)
	}
	rec := &recorder{}
	srv := mcp.New(rec, &counter{}, "test")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	workdir := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, rt: New(cfg), srv: srv, rec: rec, dir: dir, workdir: workdir}
}

// spec opens an MCP session and returns the SessionSpec that plays the fixture.
func (e *env) spec(fixture string, vars map[string]string) ports.SessionSpec {
	e.t.Helper()
	e.n++
	id := fmt.Sprintf("ss_%d", e.n)
	schema, _ := contracts.OutputSchema("report-result")
	ms, err := e.srv.Open(mcp.SessionSpec{
		ID: id, Workspace: "ws1", Node: "nd1", Role: "reporter", Tools: []string{mcp.ToolResult}, Schema: schema,
		Guard: guard.Policy{Workdir: e.workdir, ProtectedPaths: []string{".kluzo/**"}, SecretPaths: []string{".env"}},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(ms.Close)
	env := map[string]string{"KLUZO_FAKE_FIXTURE": fixture}
	for k, v := range vars {
		env[k] = v
	}
	return ports.SessionSpec{
		SessionID: id, Attempt: 1, Round: 2,
		Role:   ports.RoleConfig{Name: "implementer", Model: "sonnet", Effort: "high", Tools: []string{"read", "edit", "bash"}, MaxTurns: 80},
		System: "SYSTEM PROMPT", Prompt: "TASK PROMPT",
		Segments: ports.SegmentHashes{Protocol: "p1", Role: "r1", Rules: "k1"},
		Workdir:  e.workdir, MCPEndpoint: ms.URL(), MCPToken: ms.Token(), MCPTools: []string{mcp.ToolResult},
		Accepted: ms.Accepted, HookEndpoint: ms.HookURL(), Hooked: ms.Hooked, Env: env, MaxBudgetUSDMicros: 1_900_000,
	}
}

// play runs a session to the end and returns its result and events.
func (e *env) play(spec ports.SessionSpec) (ports.Result, []ports.Event) {
	e.t.Helper()
	s, err := e.rt.Start(context.Background(), spec)
	if err != nil {
		e.t.Fatalf("Start: %v", err)
	}
	return e.finish(s)
}

func (e *env) finish(s ports.Session) (ports.Result, []ports.Event) {
	e.t.Helper()
	var evs []ports.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range s.Events() {
			evs = append(evs, ev)
		}
	}()
	res, err := s.Wait()
	if err != nil {
		e.t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		e.t.Fatal("the event channel did not close")
	}
	return res, evs
}

func kinds(evs []ports.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Kind
	}
	return out
}

func payload[T any](t *testing.T, ev ports.Event) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(ev.Payload, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func find(evs []ports.Event, kind string) []ports.Event {
	var out []ports.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestSuccessNormalizesTheStream(t *testing.T) {
	e := newEnv(t)
	res, evs := e.play(e.spec("success", nil))
	if res.State != ports.StateFinished || res.Cause != "" {
		t.Fatalf("result = %+v", res)
	}
	var raw map[string]any
	if err := json.Unmarshal(res.Raw, &raw); err != nil || raw["markdown"] != "## Report" {
		t.Errorf("Raw = %s", res.Raw)
	}
	want := []string{
		"session.started", "session.message", "tool.call", "file.edited", "tool.result",
		"tool.call", "command.run", "tool.result", "tool.call", "tool.result", "session.message", "cost", "session.finished",
	}
	if got := kinds(evs); !slices.Equal(got, want) {
		t.Fatalf("events = %v\nwant     %v", got, want)
	}
	st := payload[contracts.SessionStarted](t, evs[0])
	if st.Role != "implementer" || st.Harness != "claude-code" || st.Attempt != 1 || st.Round != 2 || st.HarnessVersion != "2.1.319" || st.PermissionMode != "dontAsk" || st.Model != "claude-sonnet-4" {
		t.Errorf("session.started = %+v", st)
	}
	if fe := payload[contracts.FileEdited](t, find(evs, "file.edited")[0]); fe != (contracts.FileEdited{Path: "internal/a.go", Added: 4, Removed: 2}) {
		t.Errorf("file.edited = %+v", fe)
	}
	if cr := payload[contracts.CommandRun](t, find(evs, "command.run")[0]); cr.Command != "go test ./..." {
		t.Errorf("command.run = %+v", cr)
	}
	results := find(evs, "tool.result")
	if r := payload[contracts.ToolResult](t, results[1]); r.Tool != "Bash" || r.OK || r.OutputSummary != "FAIL: boom" {
		t.Errorf("tool.result = %+v", r)
	}
	// Cost comes from modelUsage, summed over the models: 0.10 + 0.02.
	if c := payload[contracts.Cost](t, find(evs, "cost")[0]); c != (contracts.Cost{InputTokens: 120, OutputTokens: 60, CostUSDMicros: 120_000}) {
		t.Errorf("cost = %+v", c)
	}
	if res.CostUSD != 120_000 || res.Cumulative != (ports.Usage{InputTokens: 120, OutputTokens: 60, CostUSD: 120_000}) || res.Turns != 4 || res.Version != "2.1.319" {
		t.Errorf("result totals = %+v", res)
	}
	fin := payload[contracts.SessionFinished](t, evs[len(evs)-1])
	if fin.State != "finished" || fin.CostUSDMicros != 120_000 {
		t.Errorf("session.finished = %+v", fin)
	}
}

func TestClassification(t *testing.T) {
	tests := []struct {
		fixture string
		state   string
		cause   ports.Cause
		retry   bool
		detail  string
	}{
		{"success", ports.StateFinished, "", false, ""},
		{"success_no_result", ports.StateFailed, ports.CauseNoResult, false, "without an accepted kluzo_result"},
		{"success_is_error_refusal", ports.StateFailed, ports.CauseRefusal, false, "refused"},
		{"success_is_error_api", ports.StateFailed, ports.CauseRuntime, true, "API Error: 500"},
		{"error_max_turns", ports.StateFailed, ports.CauseMaxTurns, false, "error_max_turns"},
		{"error_max_budget_usd", ports.StateFailed, ports.CauseBudget, false, "error_max_budget_usd"},
		{"auth_401", ports.StateFailed, ports.CauseAuth, false, "Invalid API key"},
		{"rate_limit_429", ports.StateFailed, ports.CauseRateLimit, true, "rate limited"},
		{"error_during_execution", ports.StateFailed, ports.CauseRuntime, true, "tool crashed"},
		{"died", ports.StateFailed, ports.CauseRuntime, true, "exit code 139"},
		{"auth_stderr", ports.StateFailed, ports.CauseAuth, false, "Please run /login"},
		{"accepted_then_error", ports.StateFinished, "", false, ""}, // an accepted result wins over a later error
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := newEnv(t)
			res, _ := e.play(e.spec(tt.fixture, nil))
			if res.State != tt.state || res.Cause != tt.cause || res.Cause.Retryable() != tt.retry || !strings.Contains(res.Detail, tt.detail) {
				t.Errorf("result = %+v, want %s/%q retry=%v detail~%q", res, tt.state, tt.cause, tt.retry, tt.detail)
			}
		})
	}
}

func TestDetailIsRedacted(t *testing.T) {
	e := newEnv(t)
	res, _ := e.play(e.spec("died", nil))
	if strings.Contains(res.Detail, "secret-token-123456") {
		t.Errorf("detail leaks a token: %q", res.Detail)
	}
}

// Safety failures abort the session at the init message (ADR 0024, items 5 and 13).
func TestInitChecks(t *testing.T) {
	tests := []struct{ fixture, detail string }{
		{"unexpected_tool", "unexpected [WebFetch]"},
		{"mcp_missing", "mcp__kluzo__kluzo_result"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := newEnv(t)
			start := time.Now()
			res, evs := e.play(e.spec(tt.fixture, nil))
			if res.State != ports.StateFailed || res.Cause != ports.CauseGuard || !strings.Contains(res.Detail, tt.detail) {
				t.Errorf("result = %+v", res)
			}
			if time.Since(start) > 5*time.Second {
				t.Error("the hung process was not stopped")
			}
			if kinds(evs)[0] != "session.started" {
				t.Errorf("events = %v", kinds(evs))
			}
		})
	}
}

func TestUnknownEventsAreOnlyCounted(t *testing.T) {
	e := newEnv(t)
	res, evs := e.play(e.spec("unknown_types", nil))
	if res.State != ports.StateFinished {
		t.Fatalf("result = %+v", res)
	}
	want := map[string]int{"active_goal": 1, "system/status": 1, "autocompact_state": 1, "invalid-json": 1, "assistant/server_tool_use": 1,
		"system/hook_started-unmatched": 1} // the kluzo_result call has no tool_result in this fixture
	if fmt.Sprint(res.Unknown) != fmt.Sprint(want) {
		t.Errorf("unknown = %v, want %v", res.Unknown, want)
	}
	for _, ev := range evs {
		if strings.Contains(string(ev.Payload), "active_goal") {
			t.Errorf("an unknown type became an event: %s", ev.Payload)
		}
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "logs", "ss_1.jsonl"))
	if err != nil || !strings.Contains(string(raw), "active_goal") || !strings.Contains(string(raw), "this is not json") {
		t.Errorf("the raw log must keep everything: %v\n%s", err, raw)
	}
}

func TestTurnLimitIsCountedInTheStream(t *testing.T) {
	e := newEnv(t)
	spec := e.spec("too_many_turns", nil)
	spec.Role.MaxTurns = 3
	start := time.Now()
	res, _ := e.play(spec)
	if res.State != ports.StateFailed || res.Cause != ports.CauseMaxTurns || res.Turns < 4 {
		t.Errorf("result = %+v", res)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the session kept running after the limit")
	}
}

func TestInvocationEnvironmentAndFiles(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.EnvAllow = []string{"DATABASE_URL_TEST"} })
	rec := filepath.Join(t.TempDir(), "record.json")
	spec := e.spec("success", map[string]string{"KLUZO_FAKE_RECORD": rec, "GOMODCACHE": "/cache/go/mod", "ANTHROPIC_API_KEY": "from-project-env"})
	spec.Verify = []string{"go test ./..."}
	spec.Role.Tools = []string{"read", "edit", "test"}
	if res, _ := e.play(spec); res.State != ports.StateFinished {
		t.Fatalf("result = %+v", res)
	}
	var got struct {
		Argv []string
		Env  []string
		Cwd  string
		MCP  struct{ Path, Mode, Content string } `json:"--mcp-config"`
		Set  struct{ Path, Mode, Content string } `json:"--settings"`
		Sys  struct{ Path, Mode, Content string } `json:"--append-system-prompt-file"`
	}
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	// The invocation of ADR 0024, item 1, flag by flag.
	id := harnessUUID(spec.SessionID, 0)
	want := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--restricted", "--strict-mcp-config", "--disable-slash-commands", "--session-id", id,
		"--model", "sonnet", "--effort", "high", "--max-turns", "80", "--max-budget-usd", "1.900000",
		"--append-system-prompt-file", got.Sys.Path, "--exclude-dynamic-system-prompt-sections",
		"--tools", "Read,Glob,Grep,Edit,Write,NotebookEdit,Bash",
		"--allowedTools", "Read,Glob,Grep,Edit,Write,NotebookEdit,Bash(go test ./...),mcp__kluzo__kluzo_result",
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--mcp-config", got.MCP.Path, "--settings", got.Set.Path, "--include-hook-events",
	}
	if !slices.Equal(got.Argv, want) {
		t.Errorf("argv =\n%v\nwant\n%v", got.Argv, want)
	}

	// The token is in the 0600 files, never in the command line; the files live outside the worktree and are gone afterwards.
	if strings.Contains(strings.Join(got.Argv, " "), spec.MCPToken) {
		t.Error("the MCP token is in argv")
	}
	if got.Sys.Content != spec.System {
		t.Errorf("system prompt file = %q, want %q", got.Sys.Content, spec.System)
	}
	for _, f := range []struct{ name, mode, path string }{{"mcp", got.MCP.Mode, got.MCP.Path}, {"settings", got.Set.Mode, got.Set.Path}, {"system prompt", got.Sys.Mode, got.Sys.Path}} {
		if f.mode != "-rw-------" {
			t.Errorf("%s file mode = %s, want 0600", f.name, f.mode)
		}
		if strings.HasPrefix(f.path, e.workdir) {
			t.Errorf("%s file %s is inside the worktree", f.name, f.path)
		}
		if _, err := os.Stat(f.path); !os.IsNotExist(err) {
			t.Errorf("%s file %s was not removed at the end of the session", f.name, f.path)
		}
	}
	var mcpDoc struct {
		Servers map[string]struct {
			Type, URL string
			Headers   map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got.MCP.Content), &mcpDoc); err != nil {
		t.Fatal(err)
	}
	if k := mcpDoc.Servers["kluzo"]; k.Type != "http" || k.URL != spec.MCPEndpoint || k.Headers["Authorization"] != "Bearer "+spec.MCPToken {
		t.Errorf("mcp config = %+v", mcpDoc)
	}

	if want, _ := filepath.EvalSymlinks(e.workdir); got.Cwd != want && got.Cwd != e.workdir {
		t.Errorf("cwd = %s, want the worktree %s", got.Cwd, e.workdir)
	}
	wantEnv := []string{
		"ANTHROPIC_API_KEY=sk-ant-test-credential-1234", // the configured credential beats the project's variable
		"DATABASE_URL_TEST=postgres://test", "DISABLE_AUTOUPDATER=1", "GIT_TERMINAL_PROMPT=0",
		"GOMODCACHE=/cache/go/mod", "HOME=" + filepath.Join(e.dir, "home"),
	}
	for _, kv := range wantEnv {
		if !slices.Contains(got.Env, kv) {
			t.Errorf("environment lacks %q", kv)
		}
	}
	for _, kv := range got.Env {
		for _, leak := range []string{"SSH_AUTH_SOCK", "KLUZO_GH_TOKEN", "from-project-env"} {
			if strings.Contains(kv, leak) {
				t.Errorf("environment leaks %q", kv)
			}
		}
	}
}

func TestToolMapping(t *testing.T) {
	tests := []struct {
		name      string
		canonical []string
		verify    []string
		available string
		allowed   string
	}{
		{"reviewer", []string{"read"}, nil, "Read,Glob,Grep", "Read,Glob,Grep,mcp__kluzo__kluzo_result"},
		{"implementer", []string{"read", "edit", "bash", "test"}, []string{"make test"}, "Read,Glob,Grep,Edit,Write,NotebookEdit,Bash", "Read,Glob,Grep,Edit,Write,NotebookEdit,Bash,mcp__kluzo__kluzo_result"},
		{"test only", []string{"read", "test"}, []string{"make test", "make lint"}, "Read,Glob,Grep,Bash", "Read,Glob,Grep,Bash(make test),Bash(make lint),mcp__kluzo__kluzo_result"},
		{"test without commands", []string{"test"}, nil, "Bash", "mcp__kluzo__kluzo_result"},
	}
	for _, tt := range tests {
		ts, err := mapTools(tt.canonical, tt.verify, []string{"kluzo_result"})
		if err != nil || strings.Join(ts.available, ",") != tt.available || strings.Join(ts.allowed, ",") != tt.allowed {
			t.Errorf("%s: %+v, %v", tt.name, ts, err)
		}
	}
	if _, err := mapTools([]string{"fly"}, nil, nil); err == nil {
		t.Error("unknown canonical tool accepted")
	}
}

func TestBudgetAndTurnsFlagsAreOptional(t *testing.T) {
	spec := ports.SessionSpec{System: "s", Role: ports.RoleConfig{Tools: []string{"read"}}}
	ts, _ := mapTools(spec.Role.Tools, nil, nil)
	args := argv(spec, ts, "id", false, files{mcp: "m", settings: "s", system: "p"})
	for _, f := range []string{"--max-turns", "--max-budget-usd", "--model", "--effort"} {
		if slices.Contains(args, f) {
			t.Errorf("%s is set without a value", f)
		}
	}
}

// The system prompt goes by file, so a prompt over the 128 KiB limit of one
// argument still starts the session, whole.
func TestLargeSystemPromptGoesByFile(t *testing.T) {
	e := newEnv(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	spec := e.spec("success", map[string]string{"KLUZO_FAKE_RECORD": rec})
	spec.System = strings.Repeat("rule\n", 60_000) // 300 KB
	if res, _ := e.play(spec); res.State != ports.StateFinished {
		t.Fatalf("result = %+v", res)
	}
	var got struct {
		Argv []string
		Sys  struct{ Content string } `json:"--append-system-prompt-file"`
	}
	data, _ := os.ReadFile(rec)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Sys.Content != spec.System {
		t.Errorf("system prompt file has %d bytes, want %d", len(got.Sys.Content), len(spec.System))
	}
	for _, a := range got.Argv {
		if len(a) > 4096 {
			t.Errorf("argv carries a %d-byte argument", len(a))
		}
	}
}

func TestPromptAndSendUseStreamJSONInput(t *testing.T) {
	e := newEnv(t)
	spec := e.spec("wait_input", nil)
	s, err := e.rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "the answer is 42"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, evs := e.finish(s)
	if res.State != ports.StateFinished {
		t.Fatalf("result = %+v", res)
	}
	var texts []string
	for _, ev := range find(evs, "session.message") {
		texts = append(texts, payload[contracts.SessionMessage](t, ev).Text)
	}
	if !slices.Contains(texts, "got: the answer is 42") {
		t.Errorf("the fake did not receive the Send message: %v", texts)
	}
	if err := s.Send(context.Background(), "late"); err == nil {
		t.Error("Send after the end succeeded")
	}
}

func TestRawStreamIsLoggedRedacted(t *testing.T) {
	e := newEnv(t)
	e.play(e.spec("died", nil))
	log, err := os.ReadFile(filepath.Join(e.dir, "logs", "ss_1.jsonl"))
	if err != nil || len(log) == 0 {
		t.Fatalf("log: %v", err)
	}
	if st, _ := os.Stat(filepath.Join(e.dir, "logs", "ss_1.jsonl")); st.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v", st.Mode().Perm())
	}
	// A credential in the stream is masked before it reaches the log.
	e2 := newEnv(t)
	spec := e2.spec("success", nil)
	spec.Prompt = "x"
	e2.play(spec)
	data, _ := os.ReadFile(filepath.Join(e2.dir, "logs", "ss_1.jsonl"))
	for _, secret := range []string{spec.MCPToken, "sk-ant-test-credential-1234"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("the log holds %q", secret)
		}
	}
}

func TestHarnessUUID(t *testing.T) {
	a, b := harnessUUID("ss_1", 0), harnessUUID("ss_1", 0)
	if a != b || a == harnessUUID("ss_1", 1) || a == harnessUUID("ss_2", 0) {
		t.Errorf("the id must be stable and depend on both inputs: %s", a)
	}
	// Version 5, RFC 4122 variant.
	if len(a) != 36 || a[14] != '5' || !strings.ContainsRune("89ab", rune(a[19])) {
		t.Errorf("uuid = %s", a)
	}
}

func TestResume(t *testing.T) {
	e := newEnv(t)
	rec := filepath.Join(t.TempDir(), "resume.json")
	spec := e.spec("success", nil)
	s, err := e.rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := e.finish(s)
	ref := s.HarnessRef()
	if !strings.HasPrefix(ref, "claude-code/"+harnessUUID(spec.SessionID, 0)+"/") {
		t.Errorf("ref = %s", ref)
	}

	// Same inputs: resumed with --resume and the same id, with the limits free to change.
	again := e.spec("resumed", map[string]string{"KLUZO_FAKE_RECORD": rec})
	again.SessionID = spec.SessionID
	again.MaxBudgetUSDMicros = 700_000
	again.Role.MaxTurns = 10
	again.Prompt = "reduced task"
	again.PriorUsage = first.Cumulative
	rs, err := e.rt.Resume(context.Background(), ref, again)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// The harness sums cost and tokens over the resumes (0.12 then 0.18):
	// the resumed session counts only the difference.
	res, evs := e.finish(rs)
	if res.State != ports.StateFinished || res.CostUSD != 60_000 || res.Cumulative != (ports.Usage{InputTokens: 180, OutputTokens: 90, CostUSD: 180_000}) {
		t.Errorf("resumed result = %+v", res)
	}
	if c := payload[contracts.Cost](t, find(evs, "cost")[0]); c != (contracts.Cost{InputTokens: 60, OutputTokens: 30, CostUSDMicros: 60_000}) {
		t.Errorf("resumed cost = %+v", c)
	}
	// A resumed session that ends without a result carries the prior total
	// forward, so the next resume does not count the earlier cost again.
	hung := e.spec("hang", nil)
	hung.SessionID = spec.SessionID
	hung.PriorUsage = res.Cumulative
	hs, err := e.rt.Resume(context.Background(), ref, hung)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	_ = hs.Cancel()
	if hres, _ := e.finish(hs); hres.State != ports.StateCancelled || hres.Cumulative != res.Cumulative || hres.CostUSD != 0 {
		t.Errorf("cancelled resume = %+v", hres)
	}
	// A prior larger than the harness total counts zero and says so.
	wrong := e.spec("resumed", nil)
	wrong.SessionID = spec.SessionID
	wrong.PriorUsage = ports.Usage{CostUSD: 999_000_000}
	ws, err := e.rt.Resume(context.Background(), ref, wrong)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if wres, _ := e.finish(ws); wres.CostUSD != 0 || wres.Unknown["usage/prior-exceeds-total"] != 1 {
		t.Errorf("resume with a wrong prior = %+v", wres)
	}
	var got struct{ Argv []string }
	data, _ := os.ReadFile(rec)
	_ = json.Unmarshal(data, &got)
	i := slices.Index(got.Argv, "--resume")
	if i < 0 || got.Argv[i+1] != harnessUUID(spec.SessionID, 0) || slices.Contains(got.Argv, "--session-id") {
		t.Errorf("argv = %v", got.Argv)
	}
	// The CLI checks the limit against the invocation, not the conversation
	// (Q40): the resume gets what the node has left, without the prior cost.
	if b := slices.Index(got.Argv, "--max-budget-usd"); b < 0 || got.Argv[b+1] != "0.700000" {
		t.Errorf("budget of the resume = %v", got.Argv)
	}
	if rs.HarnessRef() != ref {
		t.Errorf("ref changed: %s", rs.HarnessRef())
	}

	// Anything that must stay the same and does not: a new session is needed.
	for name, mutate := range map[string]func(*ports.SessionSpec){
		"protocol segment": func(s *ports.SessionSpec) { s.Segments.Protocol = "other" },
		"role segment":     func(s *ports.SessionSpec) { s.Segments.Role = "other" },
		"rules segment":    func(s *ports.SessionSpec) { s.Segments.Rules = "other" },
		"working dir":      func(s *ports.SessionSpec) { s.Workdir = t.TempDir() },
		"model":            func(s *ports.SessionSpec) { s.Role.Model = "opus" },
		"system prompt":    func(s *ports.SessionSpec) { s.System = "changed" },
		"tools":            func(s *ports.SessionSpec) { s.Role.Tools = []string{"read"} },
	} {
		t.Run(name, func(t *testing.T) {
			sp := e.spec("resumed", nil)
			sp.SessionID = spec.SessionID
			mutate(&sp)
			if _, err := e.rt.Resume(context.Background(), ref, sp); err == nil || !strings.Contains(err.Error(), ports.ErrResumeMismatch.Error()) {
				t.Errorf("Resume = %v, want a mismatch", err)
			}
		})
	}
	if _, err := e.rt.Resume(context.Background(), "garbage", again); err == nil {
		t.Error("a foreign ref was accepted")
	}
}

func TestCancelKillsTheProcessGroup(t *testing.T) {
	e := newEnv(t)
	s, err := e.rt.Start(context.Background(), e.spec("hang_ignore_term", nil))
	if err != nil {
		t.Fatal(err)
	}
	// The fake says "ready" once it ignores SIGTERM and has started a grandchild.
	ready := false
	for ev := range s.Events() {
		if ev.Kind == contracts.EventSessionMessage && payload[contracts.SessionMessage](t, ev).Text == "ready" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("the fake never said it was ready")
	}
	start := time.Now()
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	res, _ := e.finish(s)
	if res.State != ports.StateCancelled {
		t.Errorf("result = %+v", res)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 5*time.Second {
		t.Errorf("stopped after %v, want SIGKILL after the 300ms grace", d)
	}
}

func TestTimeoutAndContextCancel(t *testing.T) {
	e := newEnv(t)
	spec := e.spec("hang", nil)
	spec.Timeout = 300 * time.Millisecond
	if res, _ := e.play(spec); res.State != ports.StateTimeout {
		t.Errorf("result = %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, err := e.rt.Start(ctx, e.spec("hang", nil))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if res, _ := e.finish(s); res.State != ports.StateCancelled {
		t.Errorf("result = %+v", res)
	}
}

func TestWaitWithoutReadingEvents(t *testing.T) {
	e := newEnv(t)
	s, err := e.rt.Start(context.Background(), e.spec("success", nil))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := s.Wait(); err != nil || res.State != ports.StateFinished {
		t.Errorf("Wait = %+v, %v", res, err)
	}
}

func TestStartErrors(t *testing.T) {
	e := newEnv(t)
	spec := e.spec("success", nil)
	spec.Role.Tools = []string{"fly"}
	if _, err := e.rt.Start(context.Background(), spec); err == nil {
		t.Error("an unmappable tool was accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(e.dir, "config"))
	if len(entries) != 0 {
		t.Errorf("failed starts left %d config dirs behind", len(entries))
	}
	bad := newEnv(t, func(c *Config) { c.Bin = "/nonexistent/claude" })
	if _, err := bad.rt.Start(context.Background(), bad.spec("success", nil)); err == nil {
		t.Error("a missing binary was accepted")
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		vars  map[string]string
		cred  *Credential
		want  string // substring of the error; empty means none
		is    error
		wantV string
	}{
		{"ok", nil, nil, "", nil, "2.1.319"},
		{"too old", map[string]string{"KLUZO_FAKE_VERSION": "2.1.186"}, nil, "needs 2.1.295", ports.ErrRuntimeVersion, "2.1.186"},
		{"exactly the minimum", map[string]string{"KLUZO_FAKE_VERSION": "2.1.295"}, nil, "", nil, "2.1.295"},
		{"missing flag", map[string]string{"KLUZO_FAKE_FLAGS": strings.ReplaceAll(goodFlags, "--restricted", "")}, nil, "--restricted", ports.ErrRuntimeFlags, "2.1.319"},
		{"hidden flag unknown", map[string]string{"KLUZO_FAKE_UNKNOWN": "--append-system-prompt-file"}, nil, "--append-system-prompt-file", ports.ErrRuntimeFlags, "2.1.319"},
		{"not logged in", map[string]string{"KLUZO_FAKE_LOGGED_OUT": "1"}, &Credential{}, "claude auth login", ports.ErrRuntimeAuth, "2.1.319"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]string{"KLUZO_FAKE_FLAGS": goodFlags}
			for k, v := range tt.vars {
				vars[k] = v
			}
			e := newEnv(t, func(c *Config) {
				c.Bin = fakeBin(t, vars)
				if tt.cred != nil {
					c.Credential = *tt.cred
				}
			})
			info, err := e.rt.Check(context.Background())
			if tt.want == "" {
				if err != nil || !info.Authenticated || info.AuthSource != "env:ANTHROPIC_API_KEY" {
					t.Errorf("info %+v, err %v", info, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) || !errorsIs(err, tt.is) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			if info.Version != tt.wantV {
				t.Errorf("version = %q, want %q", info.Version, tt.wantV)
			}
		})
	}
	// A login reported by `claude auth status` counts when there is no credential variable.
	e := newEnv(t, func(c *Config) { c.Credential = Credential{} })
	if info, err := e.rt.Check(context.Background()); err != nil || info.AuthSource != "login" {
		t.Errorf("info %+v, err %v", info, err)
	}
	// A binary that is not there.
	missing := newEnv(t, func(c *Config) { c.Bin = "/nonexistent/claude" })
	if _, err := missing.rt.Check(context.Background()); !errorsIs(err, ports.ErrRuntimeMissing) {
		t.Errorf("err = %v, want ErrRuntimeMissing", err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		sign int
	}{{"2.1.295", "2.1.295", 0}, {"2.1.319", "2.1.295", 1}, {"2.1.99", "2.1.295", -1}, {"2.2.0", "2.1.999", 1}, {"3.0", "2.9.9", 1}} {
		got := compareVersions(c.a, c.b)
		if (got > 0) != (c.sign > 0) || (got < 0) != (c.sign < 0) {
			t.Errorf("compareVersions(%s, %s) = %d, want sign %d", c.a, c.b, got, c.sign)
		}
	}
}

func TestFrozenArgvHidesThePromptAndTheFiles(t *testing.T) {
	e := newEnv(t)
	res, evs := e.play(e.spec("success", nil))
	if res.State != ports.StateFinished {
		t.Fatal(res)
	}
	st := payload[contracts.SessionStarted](t, evs[0])
	joined := strings.Join(st.Argv, " ")
	sum := sha256.Sum256([]byte("SYSTEM PROMPT"))
	if strings.Contains(joined, "SYSTEM PROMPT") || strings.Contains(joined, e.dir) || !strings.Contains(joined, "--append-system-prompt-file sha256:"+hex.EncodeToString(sum[:])) || !strings.Contains(joined, "--session-id") {
		t.Errorf("frozen argv = %s", joined)
	}
}

func errorsIs(err, target error) bool {
	if target == nil {
		return true
	}
	return err != nil && errors.Is(err, target)
}
