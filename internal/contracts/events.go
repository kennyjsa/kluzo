package contracts

import "encoding/json"

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
	EventAreaDeviation       = "area.deviation"
	EventGateStarted         = "gate.started"
	EventGateFinished        = "gate.finished"
	// EventPROpened records the PR milestone → main the closing opened, so
	// the merge on main can be observed (provisional, docs/planning/questions.md).
	EventPROpened = "pr.opened"
	// EventNodeFlow records the progress of a node through its flow (step,
	// findings, the session to resume), so a restart continues from the last
	// role that finished.
	EventNodeFlow = "node.flow"
)

// Journal-only kinds the supervisor writes.
const (
	EventMergeRebased    = "merge.rebased"
	EventMergeFreshness  = "merge.freshness"
	EventEffectFailed    = "effect.failed"
	EventBudgetAlert     = "budget.alert"
	EventProposalCreated = "proposal.created"
	EventPlanApproved    = "plan.approved"
)

// PlanApproved is the payload of plan.approved.
type PlanApproved struct {
	PlanVersion   int    `json:"plan_version"`
	CreatedIssues []int  `json:"created_issues"`
	By            string `json:"by"`
}

// Proposal kinds and states used so far.
const (
	ProposalChange  = "change"
	ProposalPending = "pending"
)

// ProposalCreated is the payload of proposal.created.
type ProposalCreated struct {
	ProposalID string `json:"proposal_id"`
	Kind       string `json:"kind"`
	State      string `json:"state,omitempty"`
}

// BudgetScopeMilestone is the scope of the budget alert of the milestone.
const BudgetScopeMilestone = "milestone"

// BudgetAlert is the payload of budget.alert.
type BudgetAlert struct {
	Scope          string `json:"scope"`
	SpentUSDMicros int64  `json:"spent_usd_micros"`
	LimitUSDMicros int64  `json:"limit_usd_micros"`
}

// WorkspaceCreated is the payload of workspace.created: the facts the
// workspace row needs that no transition carries (docs/contracts/events.md).
type WorkspaceCreated struct {
	ID              string `json:"id"`
	Branch          string `json:"branch"`
	BaseSHA         string `json:"base_sha"`
	Profile         string `json:"profile,omitempty"`
	BudgetUSDMicros int64  `json:"budget_usd_micros,omitempty"`
	// MilestoneRef is the milestone in the tracker (its number on GitHub),
	// which the approval reads the plan's graph from again.
	MilestoneRef string `json:"milestone_ref,omitempty"`
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

// NodeFlow is the payload of node.flow: the step index, and the whole run of
// the core (core.Run) as the supervisor stores it.
type NodeFlow struct {
	Step int             `json:"step"`
	Run  json.RawMessage `json:"run"`
}

// GateRun is the payload of gate.started and gate.finished; the finished one
// carries the state and the log.
type GateRun struct {
	Gate   string `json:"gate"`
	SHA    string `json:"sha"`
	State  string `json:"state,omitempty"`
	LogRef string `json:"log_ref,omitempty"`
}

// EffectFailed is the payload of effect.failed: an effect of the outbox that
// gave up (docs/contracts/data-model.md).
type EffectFailed struct {
	EffectID  string `json:"effect_id"`
	Kind      string `json:"kind"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
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

// MergeRebased is the payload of merge.rebased: the node branch rebased on
// the milestone tip base_sha, now at head_sha.
type MergeRebased struct {
	BaseSHA string `json:"base_sha"`
	HeadSHA string `json:"head_sha"`
}

// MergeFreshness is the payload of merge.freshness: the gates run again when
// the milestone tip moved since the commit the review judged (ADR 0009).
type MergeFreshness struct {
	ReviewedSHA string `json:"reviewed_sha"`
	TipSHA      string `json:"tip_sha"`
	Rerun       bool   `json:"rerun"`
}

// AreaDeviation is the payload of area.deviation (ADR 0011): the node touched
// areas it did not declare; they are added to the node.
type AreaDeviation struct {
	Declared []string `json:"declared"`
	Touched  []string `json:"touched"`
	Files    []string `json:"files"`
}

// PROpened is the payload of pr.opened.
type PROpened struct {
	Ref string `json:"ref"`
	URL string `json:"url"`
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
	EventGuardDenied     = "guard.denied"
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
	// HarnessSessionRef and Manifest are added by the supervisor, which knows
	// the harness conversation and the prompt it sent.
	HarnessSessionRef string          `json:"harness_session_ref,omitempty"`
	Manifest          json.RawMessage `json:"manifest,omitempty"`
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
	// Result is the result the session delivered and Kluzo accepted
	// (results.md), when it finished.
	Result json.RawMessage `json:"result,omitempty"`
}

// GuardDenied is the payload of guard.denied: a tool call the guard hook
// refused (ADR 0024, item 6).
type GuardDenied struct {
	Tool         string `json:"tool"`
	Rule         string `json:"rule"`
	Reason       string `json:"reason"`
	InputSummary string `json:"input_summary"`
}
