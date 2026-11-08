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
// workspace row needs that no transition carries (docs/planning/questions.md, Q11).
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
