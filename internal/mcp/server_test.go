package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/platform/redact"
)

// counter is a deterministic entropy source: every read returns distinct bytes.
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
	events []CallEvent
	denied []DenyEvent
}

func (r *recorder) Denied(_ context.Context, e DenyEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = append(r.denied, e)
}

func (r *recorder) Call(_ context.Context, e CallEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []CallEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CallEvent(nil), r.events...)
}

type env struct {
	srv *Server
	rec *recorder
}

func newEnv(t *testing.T) env {
	t.Helper()
	rec := &recorder{}
	srv := New(rec, &counter{}, "test")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return env{srv, rec}
}

func (e env) open(t *testing.T, id string, tools ...string) *Session {
	t.Helper()
	schema, _ := contracts.OutputSchema("report-result")
	s, err := e.srv.Open(SessionSpec{ID: id, Workspace: "v1", Node: "42", Role: "reporter", Tools: tools, Schema: schema, Guard: testGuard})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// bearer adds the session token to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) (*sdk.ClientSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

func call(t *testing.T, cs *sdk.ClientSession, tool string, args any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	return out, res.IsError
}

var testGuard = guard.Policy{Workdir: "/work/tree", ProtectedPaths: []string{".kluzo/**"}, SecretPaths: []string{".env"}}

var (
	validReport   = map[string]any{"v": 1, "summary": "done", "markdown": "## Report"}
	invalidReport = map[string]any{"v": 1, "summary": "done"} // markdown is required
)

func TestResultIsValidatedAndAccepted(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	cs, err := connect(t, s.URL(), s.Token())
	if err != nil {
		t.Fatal(err)
	}

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != ToolResult {
		t.Fatalf("tools = %+v, err = %v; v0.1 serves only kluzo_result", tools, err)
	}

	got, isErr := call(t, cs, ToolResult, validReport)
	if isErr || got["accepted"] != true {
		t.Fatalf("reply = %v", got)
	}
	select {
	case <-s.Done():
	default:
		t.Error("Done is not closed after an accepted result")
	}
	out := s.Outcome()
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil || !out.Accepted || result["markdown"] != "## Report" || out.Calls != 1 {
		t.Errorf("outcome = %+v", out)
	}

	// One valid call per step.
	if again, isErr := call(t, cs, ToolResult, validReport); !isErr || again["error"] != errSessionClosed {
		t.Errorf("second call = %v (isError %v), want session_closed", again, isErr)
	}
	want := []CallEvent{{"s1", "v1", "42", ToolResult, true}, {"s1", "v1", "42", ToolResult, false}}
	if got := e.rec.all(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("mcp.call events = %v, want %v", got, want)
	}
}

func TestOneCorrectionIsAllowed(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	cs, _ := connect(t, s.URL(), s.Token())

	got, isErr := call(t, cs, ToolResult, invalidReport)
	errs, _ := got["errors"].([]any)
	if isErr || got["accepted"] != false || len(errs) == 0 || !strings.HasPrefix(errs[0].(string), "$") || !strings.Contains(errs[0].(string), "markdown") {
		t.Fatalf("first reply = %v", got)
	}
	select {
	case <-s.Done():
		t.Fatal("the session ended after the first invalid result")
	default:
	}
	if got, _ := call(t, cs, ToolResult, validReport); got["accepted"] != true {
		t.Errorf("correction not accepted: %v", got)
	}
	if out := s.Outcome(); !out.Accepted || out.Calls != 2 {
		t.Errorf("outcome = %+v", out)
	}
}

func TestSecondInvalidResultEndsTheStep(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	cs, _ := connect(t, s.URL(), s.Token())

	call(t, cs, ToolResult, invalidReport)
	got, _ := call(t, cs, ToolResult, map[string]any{"v": 2, "summary": "x", "markdown": "m"})
	if got["accepted"] != false {
		t.Fatalf("reply = %v", got)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("the step must end after the second invalid result")
	}
	if out := s.Outcome(); out.Accepted || len(out.Errors) == 0 || out.Calls != 2 || len(out.Result) != 0 {
		t.Errorf("outcome = %+v", out)
	}
	if again, isErr := call(t, cs, ToolResult, validReport); !isErr || again["error"] != errSessionClosed {
		t.Errorf("third call = %v", again)
	}
}

func TestErrorsNameThePath(t *testing.T) {
	schema, _ := contracts.OutputSchema("review-result")
	sess, err := newSession(New(&recorder{}, &counter{}, "t"), SessionSpec{ID: "x", Schema: schema, Guard: testGuard})
	if err != nil {
		t.Fatal(err)
	}
	errs := sess.validate([]byte(`{"v":1,"summary":"s","criteria":[],"findings":[{"severity":"nope","category":"style","file":"a","line":1,"message":"m"}]}`))
	if len(errs) == 0 || !strings.HasPrefix(errs[0], "$.findings[0].severity: ") {
		t.Errorf("errors = %v", errs)
	}
	if errs := sess.validate([]byte(`not json`)); len(errs) != 1 || !strings.HasPrefix(errs[0], "$: not valid JSON") {
		t.Errorf("errors = %v", errs)
	}
}

func TestOnlyTheLocalHostAndTheTokenGetIn(t *testing.T) {
	e := newEnv(t)
	host, _, err := net.SplitHostPort(e.srv.addr)
	if err != nil || host != "127.0.0.1" {
		t.Errorf("listening on %q, want 127.0.0.1", e.srv.addr)
	}
	a, b := e.open(t, "a", ToolResult), e.open(t, "b", ToolResult)
	if a.Token() == b.Token() || a.Token() == "" {
		t.Error("tokens must be distinct and non-empty")
	}
	for name, token := range map[string]string{"no token": "", "wrong token": "nope", "token of another session": b.Token()} {
		if _, err := connect(t, a.URL(), token); err == nil {
			t.Errorf("%s: connection accepted", name)
		}
	}
	if _, err := connect(t, "http://"+e.srv.addr+"/s/missing/mcp", a.Token()); err == nil {
		t.Error("unknown session accepted")
	}

	cs, err := connect(t, a.URL(), a.Token())
	if err != nil {
		t.Fatal(err)
	}
	a.Close() // the session ended: the token is revoked
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: ToolResult, Arguments: validReport}); err == nil {
		t.Error("a revoked token still works")
	}
	if a.Token() != "" {
		t.Error("token not cleared on Close")
	}
}

func TestToolsOutsideTheRoleAreRefused(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult, "kluzo_context")
	cs, _ := connect(t, s.URL(), s.Token())

	tests := []struct{ tool, want string }{
		{"kluzo_ask", errForbidden},       // known tool, not declared by the role
		{"kluzo_context", errUnavailable}, // declared, but only from v0.2
		{"kluzo_made_up", errForbidden},
	}
	for _, tt := range tests {
		got, isErr := call(t, cs, tt.tool, map[string]any{})
		if !isErr || got["error"] != tt.want {
			t.Errorf("%s: %v (isError %v), want %s", tt.tool, got, isErr, tt.want)
		}
	}
	for _, ev := range e.rec.all() {
		if ev.OK {
			t.Errorf("refused call recorded as ok: %v", ev)
		}
	}
	if len(e.rec.all()) != 3 {
		t.Errorf("events = %v, want one mcp.call per call", e.rec.all())
	}
}

func TestOpenRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	e.open(t, "s1", ToolResult)
	schema, _ := contracts.OutputSchema("report-result")
	if _, err := e.srv.Open(SessionSpec{ID: "s1", Schema: schema, Guard: testGuard}); err == nil {
		t.Error("duplicate session accepted")
	}
	if _, err := e.srv.Open(SessionSpec{ID: "s2", Schema: []byte(`{"type":`), Guard: testGuard}); err == nil {
		t.Error("broken schema accepted")
	}
	if _, err := New(&recorder{}, &counter{}, "t").Open(SessionSpec{ID: "s3", Schema: schema, Guard: testGuard}); err == nil {
		t.Error("Open before Start accepted")
	}
}

func TestCloseFreesTheIDForAResumedSession(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	oldToken := s.Token()
	s.Close()
	again := e.open(t, "s1", ToolResult)
	if again.Token() == oldToken {
		t.Error("a reopened session must get a new token")
	}
	if _, err := connect(t, again.URL(), oldToken); err == nil {
		t.Error("the old token works on the reopened session")
	}
	if _, err := connect(t, again.URL(), again.Token()); err != nil {
		t.Errorf("reopened session refuses its own token: %v", err)
	}
	s.Close() // closing the stale handle must not drop the new session
	if _, err := connect(t, again.URL(), again.Token()); err != nil {
		t.Errorf("stale Close removed the new session: %v", err)
	}
}

// The core's coverage check shares the single correction with the schema.
func TestCoverageAndSchemaShareTheSingleCorrection(t *testing.T) {
	e := newEnv(t)
	schema, _ := contracts.OutputSchema("report-result")
	s, err := e.srv.Open(SessionSpec{ID: "s1", Workspace: "v1", Node: "42", Role: "reporter", Tools: []string{ToolResult}, Schema: schema, Guard: testGuard,
		Check: func(r json.RawMessage) []string {
			if strings.Contains(string(r), "bad coverage") {
				return []string{"$.criteria: AC2 is missing"}
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := connect(t, s.URL(), s.Token())

	got, _ := call(t, cs, ToolResult, map[string]any{"v": 1, "summary": "bad coverage", "markdown": "m"})
	if got["accepted"] != false || s.Outcome().Invalid != 1 {
		t.Fatalf("coverage rejection = %v, outcome %+v", got, s.Outcome())
	}
	// The schema failure that follows is the second invalid call: the step ends.
	call(t, cs, ToolResult, invalidReport)
	select {
	case <-s.Done():
	default:
		t.Fatal("schema + coverage failures must end the step")
	}
	if out := s.Outcome(); out.Accepted || out.Invalid != 2 {
		t.Errorf("outcome = %+v", out)
	}
}

func TestOpenNeedsTheWorktreeForTheGuard(t *testing.T) {
	e := newEnv(t)
	schema, _ := contracts.OutputSchema("report-result")
	if _, err := e.srv.Open(SessionSpec{ID: "s9", Schema: schema}); err == nil {
		t.Error("a session without a guard policy was opened")
	}
}

// hookCall posts a PreToolUse event to the hook endpoint of the session.
func hookCall(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHookDeniesWhatThePolicyForbids(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	// The body Claude Code 2.1.319 posts (recorded; a subagent adds agent_id and agent_type).
	pre := func(tool, input string) string {
		return `{"session_id":"00000000-0000-4000-8000-000000000001","transcript_path":"/home/user/.claude/projects/-work/00000000-0000-4000-8000-000000000001.jsonl",` +
			`"cwd":"/work","prompt_id":"00000000-0000-4000-8000-000000000002","permission_mode":"dontAsk","agent_id":"agent01","agent_type":"general-purpose",` +
			`"effort":{"level":"medium"},"hook_event_name":"PreToolUse","tool_name":"` + tool + `","tool_input":` + input + `,"tool_use_id":"toolu_1"}`
	}

	code, out := hookCall(t, s.HookURL(), s.Token(), pre("Bash", `{"command":"git push origin HEAD"}`))
	decision, _ := out["hookSpecificOutput"].(map[string]any)
	if code != 200 || decision["permissionDecision"] != "deny" || decision["hookEventName"] != "PreToolUse" || !strings.Contains(decision["permissionDecisionReason"].(string), "git push") {
		t.Fatalf("deny reply = %d %v", code, out)
	}
	if len(e.rec.denied) != 1 {
		t.Fatalf("denials = %+v", e.rec.denied)
	}
	if d := e.rec.denied[0]; d.Session != "s1" || d.Workspace != "v1" || d.Node != "42" || d.Tool != "Bash" || d.Rule != guard.RuleGitWrite || !strings.Contains(d.Input, "git push") {
		t.Errorf("denied event = %+v", d)
	}

	for name, body := range map[string]string{
		"an allowed command":    pre("Bash", `{"command":"go test ./..."}`),
		"a read of source":      pre("Read", `{"file_path":"internal/a.go"}`),
		"the result tool":       pre("mcp__kluzo__kluzo_result", `{}`),
		"another hook event":    `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git push"}}`,
		"a protected path read": pre("Read", `{"file_path":".kluzo/kluzo.yaml"}`),
		"an unknown tool":       pre("WebFetch", `{"url":"http://x"}`),
	} {
		if code, out := hookCall(t, s.HookURL(), s.Token(), body); code != 200 || len(out) != 0 {
			t.Errorf("%s: %d %v, want an empty answer (no opinion)", name, code, out)
		}
	}
	if code, out := hookCall(t, s.HookURL(), s.Token(), pre("Edit", `{"file_path":".kluzo/kluzo.yaml"}`)); code != 200 || out["hookSpecificOutput"] == nil {
		t.Errorf("protected edit: %d %v", code, out)
	}
	if code, out := hookCall(t, s.HookURL(), s.Token(), pre("Read", `{"file_path":".env"}`)); code != 200 || out["hookSpecificOutput"] == nil {
		t.Errorf("secret read: %d %v", code, out)
	}
	if len(e.rec.denied) != 3 {
		t.Errorf("denials = %d, want 3", len(e.rec.denied))
	}
}

// The input summary of guard.denied goes through redact and is cut to the
// size of a *_summary field before it reaches the Recorder.
func TestHookDenialInputIsRedactedAndCut(t *testing.T) {
	e := newEnv(t)
	schema, _ := contracts.OutputSchema("report-result")
	s, err := e.srv.Open(SessionSpec{ID: "s1", Workspace: "v1", Node: "42", Role: "reporter", Schema: schema, Guard: testGuard, Secrets: []string{"hunter2-db-password"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := "git push https://x:hunter2-db-password@example.com HEAD # ghp_" + strings.Repeat("a", 36) + " " + s.Token() + " " + strings.Repeat("z", 400)
	input, _ := json.Marshal(map[string]string{"command": cmd})
	body := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":` + string(input) + `,"tool_use_id":"toolu_1"}`
	if code, out := hookCall(t, s.HookURL(), s.Token(), body); code != 200 || out["hookSpecificOutput"] == nil {
		t.Fatalf("deny reply = %d %v", code, out)
	}
	if len(e.rec.denied) != 1 {
		t.Fatalf("denials = %+v", e.rec.denied)
	}
	got := e.rec.denied[0].Input
	for _, secret := range []string{"hunter2-db-password", "ghp_aaaa", s.Token()} {
		if strings.Contains(got, secret) {
			t.Errorf("input summary leaks %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, redact.Mask) || !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) != contracts.SummaryMax {
		t.Errorf("input summary = %q (%d chars)", got, utf8.RuneCountInString(got))
	}
}

// A request the hook cannot use is an error status, so the harness blocks the tool (onFailure: block).
func TestHookFailsClosed(t *testing.T) {
	e := newEnv(t)
	s := e.open(t, "s1", ToolResult)
	for name, tt := range map[string]struct {
		token, body string
		want        int
	}{
		"no token":    {"", `{"hook_event_name":"PreToolUse"}`, http.StatusUnauthorized},
		"wrong token": {"nope", `{"hook_event_name":"PreToolUse"}`, http.StatusUnauthorized},
		"not JSON":    {s.Token(), `<html>`, http.StatusBadRequest},
		"empty body":  {s.Token(), ``, http.StatusBadRequest},
	} {
		if code, _ := hookCall(t, s.HookURL(), tt.token, tt.body); code != tt.want {
			t.Errorf("%s: status %d, want %d", name, code, tt.want)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, s.HookURL(), nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", resp.StatusCode)
	}
	// The token dies with the session, for the hook as for MCP.
	token := s.Token()
	s.Close()
	if code, _ := hookCall(t, s.HookURL(), token, `{}`); code != http.StatusUnauthorized {
		t.Errorf("after Close: status %d", code)
	}
}
