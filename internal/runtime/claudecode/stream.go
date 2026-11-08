package claudecode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kennyjsa/kluzo/internal/contracts"
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
	CostMicros     int64
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
	redact        func(string) string

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
		expectedTools: expected, argv: argv, maxTurns: spec.Role.MaxTurns, redact: redact,
		toolNames: map[string]string{}, messages: map[string]bool{}, unknown: map[string]int{},
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
		if l.Subtype == "init" {
			return t.init(l)
		}
		t.unknown["system/"+l.Subtype]++
	case "assistant":
		return t.assistant(l)
	case "user":
		return t.user(l), nil
	case "result":
		return t.result(l), nil
	default:
		t.unknown[l.Type]++
	}
	return nil, nil
}

// init verifies the tools and the MCP server of the session before anything
// runs: the list must be exactly the expected one (ADR 0024, item 5).
func (t *tracker) init(l line) ([]ports.Event, *abort) {
	t.version = l.Version
	started := event(contracts.EventSessionStarted, contracts.SessionStarted{
		Role: t.role, Harness: t.harness, Model: l.Model, Attempt: t.attempt, Round: t.round,
		HarnessVersion: l.Version, PermissionMode: l.PermissionMode, Argv: t.argv,
	})
	got := slices.Clone(l.Tools)
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

func (t *tracker) user(l line) []ports.Event {
	if l.Message == nil {
		return nil
	}
	var text string
	if json.Unmarshal(l.Message.Content, &text) == nil { // a plain string: a user message
		return []ports.Event{event(contracts.EventSessionMessage, contracts.SessionMessage{Role: "user", Text: clip(t.redact(text), userTextMax)})}
	}
	var evs []ports.Event
	for _, b := range blocks(l.Message.Content) {
		switch b.Type {
		case "tool_result":
			evs = append(evs, event(contracts.EventToolResult, contracts.ToolResult{
				Tool: t.toolNames[b.ToolUseID], OK: !b.IsError, OutputSummary: t.summary(resultText(b.Content)),
			}))
		case "text":
			evs = append(evs, event(contracts.EventSessionMessage, contracts.SessionMessage{Role: "user", Text: clip(t.redact(b.Text), userTextMax)}))
		default:
			t.unknown["user/"+b.Type]++
		}
	}
	return evs
}

func (t *tracker) result(l line) []ports.Event {
	var in, out int64
	var cost float64
	for _, u := range l.ModelUsage {
		in, out, cost = in+u.InputTokens, out+u.OutputTokens, cost+u.CostUSD
	}
	if len(l.ModelUsage) == 0 {
		cost = l.TotalCostUSD
	}
	e := &ended{
		Subtype: l.Subtype, IsError: l.IsError, TerminalReason: l.TerminalReason, StopReason: l.StopReason,
		Turns: l.NumTurns, CostMicros: int64(cost*1e6 + 0.5), Text: l.Result,
	}
	if len(l.Errors) > 0 {
		e.Text = strings.Join(l.Errors, "; ") // what went wrong beats the generic text
	}
	if l.APIErrorStatus != nil {
		e.APIStatus = *l.APIErrorStatus
	}
	t.end = e
	return []ports.Event{event(contracts.EventCost, contracts.Cost{InputTokens: in, OutputTokens: out, CostUSDMicros: e.CostMicros})}
}

// summary redacts and cuts a text to the size of a *_summary field.
func (t *tracker) summary(s string) string { return clip(t.redact(s), contracts.SummaryMax) }

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

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}
