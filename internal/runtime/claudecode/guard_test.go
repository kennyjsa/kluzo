package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/guard"
	"github.com/kennyjsa/kluzo/internal/ports"
)

func readLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", " "))
}

// The harness reports each tool call to the hook of the MCP server, which
// denies what the policy forbids and records the attempt.
func TestGuardHookDeniesThroughTheServer(t *testing.T) {
	e := newEnv(t)
	hooklog := filepath.Join(t.TempDir(), "hooks.log")
	res, _ := e.play(e.spec("hook_denied", map[string]string{"KLUZO_FAKE_HOOKLOG": hooklog}))
	if res.State != ports.StateFinished {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"toolu_1", "deny", "toolu_2", "allow", "toolu_3", "allow"}
	if got := readLog(t, hooklog); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("hook outcomes = %v, want %v", got, want)
	}
	if len(e.rec.denied) != 1 {
		t.Fatalf("denials = %+v", e.rec.denied)
	}
	if d := e.rec.denied[0]; d.Tool != "Bash" || d.Rule != guard.RuleGitWrite || d.Workspace != "ws1" || d.Node != "nd1" || !strings.Contains(d.Input, "git push") {
		t.Errorf("denied = %+v", d)
	}
}

// The settings carry the hook with the session token and onFailure: block.
func TestSettingsHaveTheGuardHook(t *testing.T) {
	e := newEnv(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	spec := e.spec("one_tool", map[string]string{"KLUZO_FAKE_RECORD": rec, "KLUZO_FAKE_HOOKLOG": filepath.Join(t.TempDir(), "h.log")})
	e.play(spec)
	var got struct {
		Settings struct{ Content string } `json:"--settings"`
	}
	data, _ := os.ReadFile(rec)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct {
					Type, URL, OnFailure string
					Timeout              int
					Headers              map[string]string
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(got.Settings.Content), &doc); err != nil {
		t.Fatal(err)
	}
	pre := doc.Hooks.PreToolUse
	if len(pre) != 1 || pre[0].Matcher != "*" || len(pre[0].Hooks) != 1 {
		t.Fatalf("hooks = %+v", doc.Hooks)
	}
	h := pre[0].Hooks[0]
	if h.Type != "http" || h.URL != spec.HookEndpoint || h.OnFailure != "block" || h.Timeout <= 0 || h.Headers["Authorization"] != "Bearer "+spec.MCPToken {
		t.Errorf("hook = %+v", h)
	}
}

// With Kluzo's hook endpoint down, the harness blocks the tool (onFailure: block).
func TestGuardHookDownBlocks(t *testing.T) {
	e := newEnv(t)
	hooklog := filepath.Join(t.TempDir(), "hooks.log")
	spec := e.spec("one_tool", map[string]string{"KLUZO_FAKE_HOOKLOG": hooklog})
	spec.HookEndpoint = "http://127.0.0.1:1/s/" + spec.SessionID + "/hook" // nothing listens there
	e.play(spec)
	if got := readLog(t, hooklog); strings.Join(got, " ") != "toolu_1 blocked" {
		t.Errorf("hook outcomes = %v, want the tool blocked", got)
	}
}

// A hook that never ran is the canary: the session aborts as a guard failure.
func TestCanaryAbortsWhenTheHookDoesNotRun(t *testing.T) {
	e := newEnv(t)
	res, evs := e.play(e.spec("one_tool", map[string]string{"KLUZO_FAKE_NOHOOK": "1"}))
	if res.State != ports.StateFailed || res.Cause != ports.CauseGuard || !strings.Contains(res.Detail, "without reaching the guard hook") || !strings.Contains(res.Detail, "toolu_1") {
		t.Errorf("result = %+v", res)
	}
	if k := kinds(evs); k[len(k)-1] != "session.finished" {
		t.Errorf("events = %v", k)
	}
	// A late accepted result does not hide it: the guard failure beats it.
	e2 := newEnv(t)
	res, _ = e2.play(e2.spec("success", map[string]string{"KLUZO_FAKE_NOHOOK": "1"}))
	if res.State != ports.StateFailed || res.Cause != ports.CauseGuard {
		t.Errorf("result with an accepted kluzo_result = %+v", res)
	}
}

func TestSessionWithoutAGuardEndpointDoesNotStart(t *testing.T) {
	for name, unset := range map[string]func(*ports.SessionSpec){
		"no hook endpoint": func(s *ports.SessionSpec) { s.HookEndpoint = "" },
		"no Hooked":        func(s *ports.SessionSpec) { s.Hooked = nil },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			spec := e.spec("success", nil)
			unset(&spec)
			if _, err := e.rt.Start(context.Background(), spec); !errors.Is(err, ports.ErrNoGuard) {
				t.Errorf("err = %v, want ErrNoGuard", err)
			}
			entries, _ := os.ReadDir(filepath.Join(e.dir, "config"))
			if len(entries) != 0 {
				t.Errorf("a refused start left %d config dirs", len(entries))
			}
		})
	}
}

// The canary asks the hook endpoint by tool_use id: hook events in the stream
// do not replace it, and their absence alone does not abort.
func TestCanaryChecksTheHookByID(t *testing.T) {
	e := newEnv(t)
	spec := e.spec("one_tool", nil)
	spec.Hooked = func(string) bool { return false } // the stream reports the hook, the endpoint never saw the call
	if res, _ := e.play(spec); res.Cause != ports.CauseGuard || !strings.Contains(res.Detail, "a stream hook event for it: true") {
		t.Errorf("result = %+v, want a guard failure", res)
	}
}
