package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// The fixtures of testdata/recorded were recorded from Claude Code 2.1.319
// with haiku (docs/research/claude-code-runtime.md), with paths and ids
// redacted. They pin the real format of the stream: hook events that name the
// tool and not the call, subagent hooks that come before their tool_use, and
// the Task/Agent alias of the init message.
func TestRecordedStreams(t *testing.T) {
	tests := []struct {
		fixture  string
		expected []string // the tools of the session, as the init must list them
		drop     string   // a subtype of system lines to remove before the replay
		rename   string   // a tool whose hook events are renamed to another tool before the replay
		accepted bool     // the MCP server accepted a kluzo_result
		state    string
		cause    ports.Cause
		results  int // tool.result events
		failed   int // of those, with ok false
	}{
		{fixture: "success_kluzo_result", expected: []string{"Read", "mcp__kluzo__kluzo_result"}, accepted: true, state: ports.StateFinished, results: 2},
		{fixture: "error_max_turns", expected: []string{"Read"}, state: ports.StateFailed, cause: ports.CauseMaxTurns, results: 2},
		{fixture: "hook_deny", expected: []string{"Edit", "Read"}, state: ports.StateFailed, cause: ports.CauseNoResult, results: 1, failed: 1},
		{fixture: "subagent_task_alias", expected: []string{"Agent", "Read"}, state: ports.StateFailed, cause: ports.CauseNoResult, results: 2},
		{fixture: "success_kluzo_result", drop: "hook_started", expected: []string{"Read", "mcp__kluzo__kluzo_result"}, accepted: true, state: ports.StateFailed, cause: ports.CauseGuard, results: 2},
		// A hook for another tool does not cover a call: the Read ran unguarded.
		{fixture: "error_max_turns", rename: "Read", expected: []string{"Read"}, state: ports.StateFailed, cause: ports.CauseGuard, results: 2},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.drop+tt.rename, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "recorded", tt.fixture+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			tr := newTracker(ports.SessionSpec{Role: ports.RoleConfig{Name: "reporter"}}, Name, tt.expected, nil, func(s string) string { return s })
			var evs []ports.Event
			var ab *abort
			if tt.rename != "" {
				raw = bytes.ReplaceAll(raw, []byte(`"hook_name":"PreToolUse:`+tt.rename+`"`), []byte(`"hook_name":"PreToolUse:Other"`))
			}
			for _, ln := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
				var head struct{ Type, Subtype string }
				_ = json.Unmarshal(ln, &head)
				if tt.drop != "" && head.Type == "system" && head.Subtype == tt.drop {
					continue
				}
				e, a := tr.handle(ln)
				evs = append(evs, e...)
				if ab == nil {
					ab = a
				}
			}
			o := outcome{abort: ab, end: tr.end, exit: exec.Status{}, t: tr}
			if tt.accepted {
				o.accepted = json.RawMessage(`{}`)
			}
			res := classify(o)
			if res.State != tt.state || res.Cause != tt.cause {
				t.Errorf("result = %s/%s (%s), want %s/%s", res.State, res.Cause, res.Detail, tt.state, tt.cause)
			}
			if got := find(evs, "tool.result"); len(got) != tt.results {
				t.Errorf("tool.result events = %d, want %d", len(got), tt.results)
			} else {
				failed := 0
				for _, ev := range got {
					var r struct{ OK bool }
					_ = json.Unmarshal(ev.Payload, &r)
					if !r.OK {
						failed++
					}
				}
				if failed != tt.failed {
					t.Errorf("failed tool results = %d, want %d", failed, tt.failed)
				}
			}
			if tr.end == nil || res.Version != "2.1.319" || res.CostUSD <= 0 {
				t.Errorf("end = %+v, result = %+v", tr.end, res)
			}
			if n := tr.unknown["system/hook_started"] + tr.unknown["system/hook_response"]; n != 0 || (tt.rename == "" && tr.unknown["system/hook_started-unmatched"] != 0) {
				t.Errorf("hook events counted as unknown: %v", tr.unknown)
			}
		})
	}
}
