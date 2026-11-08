package contracts

// Journal event kinds the store projects (docs/contracts/events.md). The other
// kinds (session, tool, gate…) go to the journal untouched until a projection
// needs them.
const (
	EventWorkspaceCreated    = "workspace.created"
	EventWorkspaceTransition = "workspace.transition"
	EventNodeAdded           = "node.added"
	EventNodeTransition      = "node.transition"
	EventEdgeAdded           = "edge.added"
	EventMergeDone           = "merge.done"
)

// WorkspaceCreated is the payload of workspace.created: the facts the
// workspace row needs that no transition carries (docs/contracts/events.md).
type WorkspaceCreated struct {
	ID              string `json:"id"`
	Branch          string `json:"branch"`
	BaseSHA         string `json:"base_sha"`
	Profile         string `json:"profile,omitempty"`
	BudgetUSDMicros int64  `json:"budget_usd_micros,omitempty"`
}

// WorkspaceTransition is the payload of workspace.transition.
type WorkspaceTransition struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// NodeBudget is the budget of one node.
type NodeBudget struct {
	Attempts  int   `json:"attempts"`
	Minutes   int   `json:"minutes"`
	USDMicros int64 `json:"usd_micros"`
}

// NodeAdded is the payload of node.added; the node id is the event's node_id.
type NodeAdded struct {
	IssueRef string     `json:"issue_ref"`
	Title    string     `json:"title"`
	Areas    []string   `json:"areas"`
	Budget   NodeBudget `json:"budget"`
}

// NodeTransition is the payload of node.transition.
type NodeTransition struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Reason        string `json:"reason"`
	Attempt       int    `json:"attempt"`
	Round         int    `json:"round"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// EdgeAdded is the payload of edge.added.
type EdgeAdded struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// MergeDone is the payload of merge.done; the node id is the event's node_id.
type MergeDone struct {
	MergeSHA string `json:"merge_sha"`
}

// Session event kinds, normalized by the runtime adapters (docs/contracts/events.md).
const (
	EventSessionStarted  = "session.started"
	EventSessionMessage  = "session.message"
	EventToolCall        = "tool.call"
	EventToolResult      = "tool.result"
	EventFileEdited      = "file.edited"
	EventCommandRun      = "command.run"
	EventMCPCall         = "mcp.call"
	EventCost            = "cost"
	EventSessionFinished = "session.finished"
)

// SummaryMax is the longest *_summary field (events.md).
const SummaryMax = 300

// SessionStarted is the payload of session.started.
type SessionStarted struct {
	Role           string `json:"role"`
	Harness        string `json:"harness"`
	Model          string `json:"model"`
	Attempt        int    `json:"attempt"`
	Round          int    `json:"round"`
	HarnessVersion string `json:"harness_version,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	// Argv is the frozen command line of the harness (the system prompt as its
	// hash), for the session manifest (docs/contracts/prompt.md).
	Argv []string `json:"argv,omitempty"`
}

// SessionMessage is the payload of session.message.
type SessionMessage struct {
	Role string `json:"role"` // assistant | user
	Text string `json:"text"`
}

// ToolCall is the payload of tool.call.
type ToolCall struct {
	Tool         string `json:"tool"`
	InputSummary string `json:"input_summary"`
}

// ToolResult is the payload of tool.result.
type ToolResult struct {
	Tool          string `json:"tool"`
	OK            bool   `json:"ok"`
	OutputSummary string `json:"output_summary"`
}

// FileEdited is the payload of file.edited.
type FileEdited struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// CommandRun is the payload of command.run.
type CommandRun struct {
	Command    string `json:"command"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Cost is the payload of cost.
type Cost struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	CostUSDMicros int64 `json:"cost_usd_micros"`
}

// SessionFinished is the payload of session.finished.
type SessionFinished struct {
	State         string `json:"state"`
	DurationMS    int64  `json:"duration_ms"`
	CostUSDMicros int64  `json:"cost_usd_micros"`
	Cause         string `json:"cause,omitempty"`
}
