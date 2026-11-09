package ports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Runtime runs agent sessions on a harness (docs/contracts/interfaces.md).
type Runtime interface {
	Name() string // "claude-code" | "opencode"
	// Check verifies the binary, the minimum version and the credential.
	Check(ctx context.Context) (RuntimeInfo, error)
	Start(ctx context.Context, spec SessionSpec) (Session, error)
	// Resume continues the harness session named by harnessRef. It fails with
	// ErrResumeMismatch when the prompt segments, the working directory or the
	// command line changed since the session started; the caller then starts
	// a new session with the next Counter (ADR 0024, item 9).
	Resume(ctx context.Context, harnessRef string, spec SessionSpec) (Session, error)
}

// RuntimeInfo is what Check learned.
type RuntimeInfo struct {
	Version       string
	Authenticated bool
	AuthSource    string // how the credential was found, for doctor
}

// Check failures. Each wraps ErrPermanent: retrying does not fix them.
var (
	ErrRuntimeMissing = fmt.Errorf("harness binary not found: %w", ErrPermanent)
	ErrRuntimeVersion = fmt.Errorf("harness version too old: %w", ErrPermanent)
	ErrRuntimeFlags   = fmt.Errorf("harness lacks a required flag: %w", ErrPermanent)
	ErrRuntimeAuth    = fmt.Errorf("harness is not authenticated: %w", ErrPermanent)
)

// ErrResumeMismatch is returned by Resume when the session cannot be resumed.
var ErrResumeMismatch = errors.New("session cannot be resumed: its inputs changed")

// ErrNoGuard means the spec has no hook endpoint, or no way to ask it what it
// received, so the session would run unguarded.
var ErrNoGuard = fmt.Errorf("the session has no guard hook endpoint: %w", ErrPermanent)

// RoleConfig is the part of a role a runtime needs, with the precedence of
// overrides already applied.
type RoleConfig struct {
	Name     string
	Model    string
	Effort   string
	Tools    []string // canonical: read, edit, bash, test
	MaxTurns int      // 0: the runtime default
}

// SegmentHashes identify the stable prompt segments; a session may only be
// resumed when they are unchanged (docs/contracts/prompt.md).
type SegmentHashes struct {
	Protocol, Role, Rules string
}

// SessionSpec is everything a runtime needs to run one session.
type SessionSpec struct {
	SessionID string // ss_<ulid> of Kluzo
	// Counter numbers the harness sessions of one SessionID: the first is 0,
	// and a new session after a failed resume takes the next number.
	Counter int
	Attempt int
	Round   int
	Role    RoleConfig
	// System is the system prompt: protocol, role and project rules. Prompt is
	// the task segment, sent as the first user message.
	System   string
	Prompt   string
	Segments SegmentHashes
	Workdir  string // worktree of the node, or the repository upstream
	// Verify are the gate commands the test tool may run.
	Verify []string
	// MCPEndpoint and MCPToken reach the MCP server of Kluzo for this session;
	// MCPTools are the tools it serves. Accepted reports the kluzo_result the
	// server accepted, which is the only way a session succeeds.
	MCPEndpoint string
	MCPToken    string
	MCPTools    []string
	Accepted    func() (json.RawMessage, bool)
	// HookEndpoint is where the harness reports every tool call before it
	// runs (the guard hook of the MCP server, same token as MCPToken). It is
	// required: a session without the guard does not start.
	HookEndpoint string
	// Hooked reports whether the hook endpoint received the tool call with
	// this tool_use id. The adapter asks it for every tool result: a call the
	// hook did not see ran unguarded (the canary, ADR 0024, item 6). Required
	// with HookEndpoint.
	Hooked func(toolUseID string) bool
	// Env holds the project variables of the session (env_allow, caches,
	// COMPOSE_PROJECT_NAME). Nothing of the environment of Kluzo is passed.
	Env map[string]string
	// MaxBudgetUSDMicros is what the node has left to spend (0: no limit).
	MaxBudgetUSDMicros int64
	// PriorUsage is the Result.Cumulative of the previous session of the same
	// harness conversation, for Resume: the harness reports cost and tokens
	// summed over every resume, and the Result counts only this session.
	// Zero for Start.
	PriorUsage Usage
	Timeout    time.Duration // 0: no timeout
}

// Session is a running agent session.
type Session interface {
	HarnessRef() string // id of the session in the harness, for Resume
	// Events delivers the normalized events (docs/contracts/events.md) and is
	// closed when the session ends. The caller must keep reading it.
	Events() <-chan Event
	Wait() (Result, error) // blocks until the end
	// Send writes a message to a running session (interviews, answers).
	Send(ctx context.Context, msg string) error
	Cancel() error // kills the whole process group; safe to call twice
}

// Event is a normalized session event; Payload is the JSON of the matching
// type of internal/contracts.
type Event struct {
	Kind    string
	Payload json.RawMessage
}

// Session end states.
const (
	StateFinished  = "finished"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateTimeout   = "timeout"
)

// Cause says why a session failed (ADR 0024, item 7).
type Cause string

// Failure causes.
const (
	CauseMaxTurns  Cause = "max_turns"  // the step fails
	CauseBudget    Cause = "budget"     // the node is blocked by budget
	CauseAuth      Cause = "auth"       // 401/403: pause the workspace and notify
	CauseRateLimit Cause = "rate_limit" // 429/529: retry with backoff
	CauseRuntime   Cause = "runtime"    // execution error or dead process: retry
	CauseRefusal   Cause = "refusal"    // the model refused; triage
	CauseNoResult  Cause = "no_result"  // the session ended without an accepted kluzo_result
	CauseGuard     Cause = "guard"      // the safety checks of the session failed (tools, MCP, hook canary)
)

// Retryable reports whether a new session in the same attempt may help
// (at most 2, with backoff).
func (c Cause) Retryable() bool { return c == CauseRateLimit || c == CauseRuntime }

// Usage is what a harness conversation consumed.
type Usage struct {
	InputTokens, OutputTokens int64
	CostUSD                   int64 // micro-dollars
}

// Result is how a session ended.
type Result struct {
	State  string
	Cause  Cause  // set when State is StateFailed
	Detail string // for humans: what the harness said, redacted
	// Raw is the accepted kluzo_result, before the core validates its coverage.
	Raw json.RawMessage
	// CostUSD is what this session cost, in micro-dollars: the harness
	// total minus SessionSpec.PriorUsage.
	CostUSD int64
	// Cumulative is what the harness reported for the whole conversation;
	// pass it as PriorUsage to the next Resume.
	Cumulative Usage
	Turns      int
	// Unknown counts the stream event types the adapter does not know; they
	// go to the raw log only (events.md).
	Unknown map[string]int
	Version string // harness version seen when the session started
}
