package claudecode

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/platform/text"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// userTextMax bounds the text of a user message echoed in the stream.
const userTextMax = 2000

// line is one message of the stream-json output. Only the fields the adapter
// reads are declared; the raw line is kept in the log.
type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	// system/init
	Model          string   `json:"model"`
	PermissionMode string   `json:"permissionMode"`
	Tools          []string `json:"tools"`
	MCPServers     []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Version string `json:"claude_code_version"`

	// system/hook_started and hook_response, reported by
	// --include-hook-events. They do not carry the tool_use id: the tool is
	// in hook_name ("PreToolUse:Read").
	HookEvent string `json:"hook_event"`
	HookName  string `json:"hook_name"`

	// assistant and user
	Message *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// result
	IsError        bool    `json:"is_error"`
	TerminalReason string  `json:"terminal_reason"`
	StopReason     string  `json:"stop_reason"`
	APIErrorStatus *int    `json:"api_error_status"`
	NumTurns       int     `json:"num_turns"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	ModelUsage     map[string]struct {
		InputTokens  int64   `json:"inputTokens"`
		OutputTokens int64   `json:"outputTokens"`
		CostUSD      float64 `json:"costUSD"`
	} `json:"modelUsage"`
	Result string   `json:"result"`
	Errors []string `json:"errors"`
}

// block is an element of a message's content.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ended is what the result message said.
type ended struct {
	Subtype        string
	IsError        bool
	TerminalReason string
	StopReason     string
	APIStatus      int
	Turns          int
	Cumulative     ports.Usage // summed over every resume of the conversation
	Session        ports.Usage // this session: Cumulative minus the prior usage
	Text           string
}

// abort is a reason to stop the session before the harness ends it.
type abort struct {
	cause  ports.Cause
	detail string
}

// tracker follows one stream: it normalizes the messages into events and
// watches what must stop the session (ADR 0024, items 5, 7, 8).
type tracker struct {
	role, harness string
	attempt       int
	round         int
	expectedTools []string
	argv          []string // the frozen command line, for the manifest
	maxTurns      int
	prior         ports.Usage // what the conversation used before this session
	redact        func(string) string

	hooks     map[string]int    // tool -> PreToolUse hooks started and not yet matched to a tool_result
	toolNames map[string]string // tool_use id -> tool
	messages  map[string]bool   // assistant message ids already counted
	turns     int
	unknown   map[string]int
	version   string
	end       *ended
}

func newTracker(spec ports.SessionSpec, harness string, expected, argv []string, redact func(string) string) *tracker {
	return &tracker{
		role: spec.Role.Name, harness: harness, attempt: spec.Attempt, round: spec.Round,
		expectedTools: expected, argv: argv, maxTurns: spec.Role.MaxTurns, prior: spec.PriorUsage, redact: redact,
		hooks: map[string]int{}, toolNames: map[string]string{}, messages: map[string]bool{}, unknown: map[string]int{},
	}
}

func event(kind string, payload any) ports.Event {
	b, _ := json.Marshal(payload)
	return ports.Event{Kind: kind, Payload: b}
}

// handle processes one raw line. Lines that are not JSON, and types the
// adapter does not know, only count: they stay in the raw log.
func (t *tracker) handle(raw []byte) ([]ports.Event, *abort) {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		t.unknown["invalid-json"]++
		return nil, nil
	}
	switch l.Type {
	case "system":
		switch {
		case l.Subtype == "init":
			return t.init(l)
		case l.Subtype == "hook_started" && l.HookEvent == "PreToolUse":
			if tool, ok := strings.CutPrefix(l.HookName, "PreToolUse:"); ok {
				t.hooks[tool]++
			} else {
				t.unknown["system/hook_started"]++
			}
		case l.Subtype == "hook_response":
			// the answer of the guard is in the tool_result that follows
		default:
			t.unknown["system/"+l.Subtype]++
		}
	case "assistant":
		return t.assistant(l)
	case "user":
		return t.user(l)
	case "result":
		return t.result(l), nil
	default:
		t.unknown[l.Type]++
	}
	return nil, nil
}

// initAlias renames the tools the init message lists under another name than
// the tool_use blocks use: init says Task, the call is Agent.
var initAlias = map[string]string{"Task": "Agent"}

// init verifies the tools and the MCP server of the session before anything
// runs: the list must be exactly the expected one (ADR 0024, item 5).
func (t *tracker) init(l line) ([]ports.Event, *abort) {
	t.version = l.Version
	started := event(contracts.EventSessionStarted, contracts.SessionStarted{
		Role: t.role, Harness: t.harness, Model: l.Model, Attempt: t.attempt, Round: t.round,
		HarnessVersion: l.Version, PermissionMode: l.PermissionMode, Argv: t.argv,
	})
	got := make([]string, len(l.Tools))
	for i, name := range l.Tools {
		got[i] = cmp.Or(initAlias[name], name)
	}
	slices.Sort(got)
	got = slices.Compact(got)
	if !slices.Equal(got, t.expectedTools) {
		var extra, missing []string
		for _, g := range got {
			if !slices.Contains(t.expectedTools, g) {
				extra = append(extra, g)
			}
		}
		for _, e := range t.expectedTools {
			if !slices.Contains(got, e) {
				missing = append(missing, e)
			}
		}
		return []ports.Event{started}, &abort{ports.CauseGuard, fmt.Sprintf("the tools of the session are not the expected ones: unexpected %v, missing %v", extra, missing)}
	}
	for _, s := range l.MCPServers {
		if s.Name == "kluzo" && s.Status == "failed" {
			return []ports.Event{started}, &abort{ports.CauseGuard, "the MCP server kluzo failed to connect"}
		}
	}
	return []ports.Event{started}, nil
}

func (t *tracker) assistant(l line) ([]ports.Event, *abort) {
	if l.Message == nil {
		return nil, nil
	}
	// One API response can arrive as several lines with the same message id.
	if id := l.Message.ID; id == "" || !t.messages[id] {
		t.messages[id] = true
		t.turns++
	}
	var evs []ports.Event
	for _, b := range blocks(l.Message.Content) {
		switch b.Type {
		case "text":
			evs = append(evs, event(contracts.EventSessionMessage, contracts.SessionMessage{Role: "assistant", Text: t.redact(b.Text)}))
		case "tool_use":
			t.toolNames[b.ID] = b.Name
			evs = append(evs, t.toolCall(b)...)
		case "thinking", "redacted_thinking":
			// reasoning is not a session event
		default:
			t.unknown["assistant/"+b.Type]++
		}
	}
	if t.maxTurns > 0 && t.turns > t.maxTurns {
		return evs, &abort{ports.CauseMaxTurns, fmt.Sprintf("the session used %d turns, the limit is %d", t.turns, t.maxTurns)}
	}
	return evs, nil
}

// toolCall is tool.call plus the more specific file.edited or command.run.
func (t *tracker) toolCall(b block) []ports.Event {
	evs := []ports.Event{event(contracts.EventToolCall, contracts.ToolCall{Tool: b.Name, InputSummary: t.summary(string(b.Input))})}
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		OldString    string `json:"old_string"`
		NewString    string `json:"new_string"`
		Content      string `json:"content"`
		NewSource    string `json:"new_source"`
		Command      string `json:"command"`
	}
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "Edit":
		evs = append(evs, event(contracts.EventFileEdited, contracts.FileEdited{Path: in.FilePath, Added: lines(in.NewString), Removed: lines(in.OldString)}))
	case "Write":
		evs = append(evs, event(contracts.EventFileEdited, contracts.FileEdited{Path: in.FilePath, Added: lines(in.Content)}))
	case "NotebookEdit":
		evs = append(evs, event(contracts.EventFileEdited, contracts.FileEdited{Path: in.NotebookPath, Added: lines(in.NewSource)}))
	case "Bash":
		evs = append(evs, event(contracts.EventCommandRun, contracts.CommandRun{Command: t.summary(in.Command)}))
	}
	return evs
}

func (t *tracker) user(l line) ([]ports.Event, *abort) {
	if l.Message == nil {
		return nil, nil
	}
	var msg string
	if json.Unmarshal(l.Message.Content, &msg) == nil { // a plain string: a user message
		return []ports.Event{event(contracts.EventSessionMessage, contracts.SessionMessage{Role: "user", Text: text.Clip(t.redact(msg), userTextMax, text.Ellipsis)})}, nil
	}
	var evs []ports.Event
	var ab *abort
	for _, b := range blocks(l.Message.Content) {
		switch b.Type {
		case "tool_result":
			// The canary: a tool that ran without the guard hook means the
			// settings were ignored, and the guard is gone (ADR 0024, item 6).
			// Hook events name the tool, not the call, and those of a
			// subagent can come before its tool_use line: each tool_result
			// takes one hook started for the same tool.
			name := t.toolNames[b.ToolUseID]
			switch {
			case t.hooks[name] > 0:
				t.hooks[name]--
			case ab == nil:
				ab = &abort{ports.CauseGuard, fmt.Sprintf("tool %s (%s) ran without a PreToolUse hook event: the guard is not active", name, b.ToolUseID)}
			}
			evs = append(evs, event(contracts.EventToolResult, contracts.ToolResult{
				Tool: t.toolNames[b.ToolUseID], OK: !b.IsError, OutputSummary: t.summary(resultText(b.Content)),
			}))
		case "text":
			evs = append(evs, event(contracts.EventSessionMessage, contracts.SessionMessage{Role: "user", Text: text.Clip(t.redact(b.Text), userTextMax, text.Ellipsis)}))
		default:
			t.unknown["user/"+b.Type]++
		}
	}
	return evs, ab
}

// result reads the result message. total_cost_usd and modelUsage are summed
// over every resume of the harness conversation (subagents included), so what
// this session used is the difference to the prior usage of the spec.
func (t *tracker) result(l line) []ports.Event {
	var total ports.Usage
	var cost float64
	for _, u := range l.ModelUsage {
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		cost += u.CostUSD
	}
	if len(l.ModelUsage) == 0 {
		cost = l.TotalCostUSD
	}
	total.CostUSD = int64(cost*1e6 + 0.5)
	session := ports.Usage{
		InputTokens:  max(total.InputTokens-t.prior.InputTokens, 0),
		OutputTokens: max(total.OutputTokens-t.prior.OutputTokens, 0),
		CostUSD:      max(total.CostUSD-t.prior.CostUSD, 0),
	}
	e := &ended{
		Subtype: l.Subtype, IsError: l.IsError, TerminalReason: l.TerminalReason, StopReason: l.StopReason,
		Turns: l.NumTurns, Cumulative: total, Session: session, Text: l.Result,
	}
	if len(l.Errors) > 0 {
		e.Text = strings.Join(l.Errors, "; ") // what went wrong beats the generic text
	}
	if l.APIErrorStatus != nil {
		e.APIStatus = *l.APIErrorStatus
	}
	t.end = e
	return []ports.Event{event(contracts.EventCost, contracts.Cost{InputTokens: session.InputTokens, OutputTokens: session.OutputTokens, CostUSDMicros: session.CostUSD})}
}

// summary redacts and cuts a text to the size of a *_summary field.
func (t *tracker) summary(s string) string {
	return text.Clip(t.redact(s), contracts.SummaryMax, text.Ellipsis)
}

func blocks(raw json.RawMessage) []block {
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	return bs
}

// resultText is the text of a tool_result, whose content is a string or a list
// of text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range blocks(raw) {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
