package mcp

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/guard"
)

// maxHookBody bounds a hook request; tool inputs can carry whole files.
const maxHookBody = 8 << 20

// hookInput is the part of the PreToolUse event the guard reads.
type hookInput struct {
	Event     string          `json:"hook_event_name"`
	Tool      string          `json:"tool_name"`
	Input     json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// hook answers the PreToolUse hook of the harness: it applies the guard policy
// to the tool call and denies what the policy forbids. An answer that is not
// "deny" lets the normal permission flow decide, so the hook can only tighten
// what the session may do. A request it cannot read gets a 400, which the
// harness treats as a failure of the hook and blocks (onFailure: block).
func (s *Session) hook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody))
	var in hookInput
	if err != nil || json.Unmarshal(body, &in) != nil {
		http.Error(w, `{"error":"invalid_input"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if in.Event != "PreToolUse" {
		_, _ = w.Write([]byte("{}"))
		return
	}
	d := s.spec.Guard.Check(guard.Call{Tool: in.Tool, Input: in.Input})
	if d.Allowed {
		_, _ = w.Write([]byte("{}"))
		return
	}
	s.server.rec.Denied(r.Context(), DenyEvent{
		Session: s.spec.ID, Workspace: s.spec.Workspace, Node: s.spec.Node,
		Tool: in.Tool, Rule: d.Rule, Reason: d.Reason, Input: summarize(in.Input),
	})
	_ = json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": d.Reason,
	}})
}

// summarize cuts a tool input to the size of a *_summary field.
func summarize(raw json.RawMessage) string {
	s := []rune(string(raw))
	if len(s) > contracts.SummaryMax {
		return string(s[:contracts.SummaryMax-1]) + "…"
	}
	return string(s)
}
