package supervisor

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/mcp"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/store"
)

// Recorder journals what the MCP server of the workspace reports: the
// kluzo_* calls and the tool calls the guard hook denied (events.md).
type Recorder struct {
	DB    *store.DB
	Clock clock.Clock
	IDs   *ids.Generator
	Log   *slog.Logger
}

var _ mcp.Recorder = (*Recorder)(nil)

// mcpCall is the payload of mcp.call.
type mcpCall struct {
	Tool string `json:"tool"`
	OK   bool   `json:"ok"`
}

// Call implements mcp.Recorder.
func (r *Recorder) Call(ctx context.Context, e mcp.CallEvent) {
	r.append(ctx, contracts.EventMCPCall, e.Node, e.Session, mcpCall{Tool: e.Tool, OK: e.OK})
}

// Denied implements mcp.Recorder.
func (r *Recorder) Denied(ctx context.Context, e mcp.DenyEvent) {
	r.append(ctx, contracts.EventGuardDenied, e.Node, e.Session, contracts.GuardDenied{Tool: e.Tool, Rule: e.Rule, Reason: e.Reason, InputSummary: e.Input})
}

// append journals one event. The MCP server cannot act on a failure, so it
// is logged: the journal misses an audit entry, the session goes on.
func (r *Recorder) append(ctx context.Context, kind, node, session string, payload any) {
	now := r.Clock.Now()
	id, err := r.IDs.New(ids.Event, now)
	if err == nil {
		body, _ := json.Marshal(payload) // plain data
		err = r.DB.Tx(ctx, func(t *store.Tx) error {
			_, err := t.Append(store.Event{Header: store.Header{ID: id, TS: now}, Kind: kind, NodeID: node, SessionID: session, Payload: body})
			return err
		})
	}
	if err != nil && r.Log != nil {
		r.Log.Warn("journal of an MCP event failed", "kind", kind, "session", session, "err", err)
	}
}
