package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// StepKind is the kind of a step of the node flow.
type StepKind string

// Step kinds the node flow runs (docs/contracts/config.md#flows).
const (
	StepRole       StepKind = "role"
	StepGate       StepKind = "gate"
	StepMergeQueue StepKind = "merge_queue"
)

// Roles the node flow knows how to run.
const (
	RoleImplementer = "implementer"
	RoleReviewer    = "reviewer"
)

// DefaultFastRetries is max_retries of a fast gate step when it is not set.
const DefaultFastRetries = 3

// FlowStep is one step of the node flow, with its references resolved to
// indexes.
type FlowStep struct {
	ID        string
	Kind      StepKind
	Role      string    // role steps
	Phase     NodeState // role steps: NodeRunning or NodeInReview
	OnReject  int       // role steps in review: the step a rejection returns to
	MaxRounds int       // role steps in review
	Gates     []string  // gate steps
	// Fast marks a gate step whose gates are all fast: it runs with the node
	// still running, before the review (ADR 0020).
	Fast       bool
	OnFail     int // gate steps: the step a failure returns to
	MaxRetries int // fast gate steps: failures allowed per attempt
}

// Flow is the node flow (flows/node.yaml) as the core runs it.
type Flow struct{ Steps []FlowStep }

// ErrFlowShape means the node flow is one this version cannot run.
var ErrFlowShape = errors.New("node flow not supported")

// Validate checks the shape v0.1 runs: the implementer first, fast gates only
// before the review, at most one review, full gates after it, and the merge
// queue last. config has already checked the references.
func (f Flow) Validate() error {
	bad := func(i int, msg string) error { return fmt.Errorf("%w: step %d: %s", ErrFlowShape, i+1, msg) }
	if len(f.Steps) < 2 || f.Steps[0].Kind != StepRole || f.Steps[0].Role != RoleImplementer || f.Steps[0].Phase != NodeRunning {
		return fmt.Errorf("%w: the first step must be the implementer, in phase running", ErrFlowShape)
	}
	reviewed := false
	for i, s := range f.Steps {
		last := i == len(f.Steps)-1
		switch {
		case (s.Kind == StepMergeQueue) != last:
			return bad(i, "the merge queue must be the last step, and only it")
		case s.Kind == StepRole && i > 0 && (s.Role != RoleReviewer || reviewed):
			return bad(i, "only one reviewer step may follow the implementer")
		case s.Kind == StepRole && i > 0 && (s.OnReject != 0 || s.MaxRounds < 1 || s.Phase != NodeInReview):
			return bad(i, "the review must be in phase in_review, return to the implementer and have max_rounds")
		case s.Kind == StepGate && s.Fast == reviewed:
			return bad(i, "fast gates run before the review and full gates after it")
		case s.Kind == StepGate && f.Steps[s.OnFail].Role != RoleImplementer:
			return bad(i, "a gate failure must return to the implementer")
		}
		reviewed = reviewed || (s.Kind == StepRole && i > 0)
	}
	if !reviewed {
		return fmt.Errorf("%w: the flow needs a reviewer step", ErrFlowShape)
	}
	return nil
}

// HarnessSession is a harness conversation a later session may resume.
type HarnessSession struct {
	Ref        string
	Harness    string
	Model      string
	Segments   ports.SegmentHashes
	Nonce      string      // the envelope nonce of its prompt, which a resume keeps
	Cumulative ports.Usage // pass as PriorUsage to the resume
}

// Run is the progress of a node through its flow, kept with the node across
// rounds, attempts and restarts of Kluzo (the node.flow event).
type Run struct {
	Step     int
	Findings Findings
	// FastGateFailures mirrors Node.FastGateFailures, which the node
	// projection does not keep.
	FastGateFailures int
	// Implementer is the session the next implementer round resumes; nil
	// starts a new one (first round of an attempt).
	Implementer *HarnessSession
	Report      json.RawMessage // the last accepted node-result
	Responses   []Response      // its answers to the open findings
	// Fresh asks for a new worktree before the next implementer session: set
	// at the start of every attempt.
	Fresh bool
	// Base is the commit the attempt started from, RoundBase the commit the
	// current round started from, and Head the commit of the last
	// implementer session: the reviewer judges Base..Head, and from round 2
	// RoundBase..Head as the round.
	Base, RoundBase, Head string
	// GateFailures are the failed gates the implementer is sent back for.
	GateFailures []ports.GateResult
	// Rejected says the implementer is back for review findings; it stays
	// set until the reviewer judges them, through fast gate retries.
	Rejected bool
	Ready    bool // the node reached the merge queue
	// Tip is the milestone tip the node branch was rebased on, while the
	// node integrates.
	Tip string `json:",omitempty"`
	// Integration is why the implementer is back after a failed integration:
	// a rebase conflict or red freshness gates.
	Integration *IntegrationFailure `json:",omitempty"`
}

// IntegrationFailure is a rebase conflict or a freshness gate that failed.
type IntegrationFailure struct {
	ConflictFiles []string `json:",omitempty"`
	Markers       string   `json:",omitempty"` // the conflict markers, as text
	Freshness     string   `json:",omitempty"` // what changed in the milestone since the review
}

// NodeFlow is a node and its progress through the flow.
type NodeFlow struct {
	Node Node
	Run  Run
}

// Input is something that happened to a node in its flow.
type Input interface{ isInput() }

// Ready is the dependencies of a planned node satisfied.
type Ready struct{}

// Dispatch is the scheduler giving a ready node a slot.
type Dispatch struct{}

// SessionDone is a role session that ended, after the post-session check.
type SessionDone struct {
	Step   int
	State  string // ports.State*
	Cause  ports.Cause
	Detail string
	// Raw is the accepted result, validated by schema and coverage.
	Raw     json.RawMessage
	Session *HarnessSession // the harness conversation, for a later resume
	// Tampered means the session changed the refs, the git configuration or
	// the hooks (ADR 0024, item 4): the node fails.
	Tampered bool
	// Head is the commit of the implementer's work, and Base the commit its
	// attempt started from (set by the first session of an attempt).
	Head, Base string
	// Changed are the lines the round changed (reviewer, from round 2).
	Changed map[string][]LineRange
	// What the check after an implementer round found in the work of the
	// attempt (ADR 0020): Proposal are the changes that need a human (a
	// protected path, a test deleted or disabled), Auto the findings Kluzo
	// raises itself (a new lint suppression), without ids.
	Proposal []string
	Auto     []Finding
}

// SessionRetry is a session that failed with a retryable cause and will run
// again in a new session of the same attempt. Its cost is already in the
// journal (session.finished).
type SessionRetry struct {
	Step int
}

// GatesDone is a gate step that ran every gate, or, with Freshness, the full
// gates run again on the rebased branch of an integrating node.
type GatesDone struct {
	Step      int
	Results   []ports.GateResult
	Freshness bool
	// Changed is what changed in the milestone since the review, for the
	// implementer when the freshness gates fail.
	Changed string
}

// QueueTurn is the merge queue giving a queued node its turn.
type QueueTurn struct{}

// Rebased is the node branch rebased on the milestone tip Tip, now at Head;
// Conflict is set, and Head empty, when the rebase stopped on a conflict.
type Rebased struct {
	Tip, Head string
	Conflict  *IntegrationFailure
}

// TipMoved is the milestone tip moved on the remote after the rebase: the
// node is rebased again, and the freshness gate judges the new tip.
type TipMoved struct{}

// Merged is the node merged into the milestone branch, at the merge commit SHA,
// and the branch pushed.
type Merged struct{ SHA string }

func (Ready) isInput()        {}
func (Dispatch) isInput()     {}
func (SessionDone) isInput()  {}
func (SessionRetry) isInput() {}
func (GatesDone) isInput()    {}
func (QueueTurn) isInput()    {}
func (Rebased) isInput()      {}
func (Merged) isInput()       {}
func (TipMoved) isInput()     {}

// Move is a transition of the node, in the order it happened. The store
// replays each one through NextNode with its limits, and must reach To.
type Move struct {
	Event  NodeEvent
	Limits Limits
	To     NodeState
}

// EffectKind is the kind of an effect of the outbox.
type EffectKind string

// Effect kinds of the node flow (docs/contracts/data-model.md).
const (
	EffectSession EffectKind = "session.start"
	EffectGates   EffectKind = "gate.run"
	EffectLabel   EffectKind = "tracker.set_label"
	EffectComment EffectKind = "tracker.comment"
	EffectNotify  EffectKind = "notify"
	// The merge queue (ADR 0009) and the cleanup of a node that ended
	// (ADR 0025).
	EffectRebase      EffectKind = "git.rebase"
	EffectMerge       EffectKind = "git.merge"
	EffectCleanupNode EffectKind = "cleanup.node"
)

// Effect is something the supervisor must do after the decision is stored.
type Effect struct {
	Kind EffectKind `json:"kind"`
	Key  string     `json:"key"` // unique within the decision
	Step int        `json:"step,omitempty"`
	Role string     `json:"role,omitempty"`
	// Resume asks the session to continue Run.Implementer.
	Resume   bool   `json:"resume,omitempty"`
	Label    string `json:"label,omitempty"`
	Body     string `json:"body,omitempty"`
	Title    string `json:"title,omitempty"`
	Priority string `json:"priority,omitempty"`
	// Freshness asks the gates effect for the full gates again, on the
	// rebased branch of an integrating node.
	Freshness bool `json:"freshness,omitempty"`
}

// Decision is what Apply decided.
type Decision struct {
	Next    NodeFlow
	Moves   []Move
	Effects []Effect
	// Workspace is a trigger for the workspace ("" for none): a refused
	// credential pauses it (state-machine.md).
	Workspace WorkspaceTrigger
	// Freshness is set when a rebase was clean: whether the milestone moved
	// since the commit the review judged, and so the gates run again.
	Freshness *Freshness
}

// Freshness compares the commit the review judged with the milestone tip.
type Freshness struct {
	Reviewed, Tip string
	Rerun         bool
}

// ErrStaleInput means an input does not belong to the current step: a result
// of a step the node already left.
var ErrStaleInput = errors.New("input does not match the current step")

// Apply decides what an input does to a node in its flow. It is pure: the
// supervisor stores the decision (moves, the new run and the effects) in one
// transaction and runs the effects. lim carries MaxAttempts; the review and
// fast gate limits come from the steps.
func Apply(s NodeFlow, in Input, f Flow, lim Limits) (Decision, error) {
	s.Node.FastGateFailures = s.Run.FastGateFailures
	d := &decider{d: Decision{Next: s}, flow: f, lim: lim}
	var err error
	switch in := in.(type) {
	case Ready:
		err = d.move(NodeEvent{Trigger: TriggerDepsSatisfied}, lim, "")
	case Dispatch:
		err = d.dispatch()
	case SessionDone:
		err = d.session(in)
	case SessionRetry:
		err = d.retry(in)
	case GatesDone:
		err = d.gates(in)
	case QueueTurn:
		err = d.queueTurn()
	case Rebased:
		err = d.rebased(in)
	case Merged:
		err = d.merged(in)
	case TipMoved:
		err = d.tipMoved()
	default:
		err = fmt.Errorf("unknown input %T", in)
	}
	if err != nil {
		return Decision{}, err
	}
	d.d.Next.Run.FastGateFailures = d.d.Next.Node.FastGateFailures
	return d.d, nil
}

type decider struct {
	d    Decision
	flow Flow
	lim  Limits
}

func (d *decider) run() *Run   { return &d.d.Next.Run }
func (d *decider) node() *Node { return &d.d.Next.Node }

// move applies a transition and queues its label and comment.
func (d *decider) move(ev NodeEvent, lim Limits, detail string) error {
	from := d.node().State
	next, err := NextNode(*d.node(), ev, lim)
	if err != nil {
		return err
	}
	d.d.Next.Node = next
	d.d.Moves = append(d.d.Moves, Move{Event: ev, Limits: lim, To: next.State})
	n := len(d.d.Moves)
	d.effect(Effect{Kind: EffectLabel, Key: fmt.Sprintf("label-%d", n), Label: next.State.Label()})
	d.effect(Effect{Kind: EffectComment, Key: fmt.Sprintf("comment-%d", n), Body: TransitionComment(from, next, ev, detail)})
	if next.State == NodeIntegrated || next.State == NodeCancelled {
		d.effect(Effect{Kind: EffectCleanupNode, Key: "cleanup"}) // a failed node keeps its worktree (ADR 0025)
	}
	return nil
}

func (d *decider) effect(e Effect) { d.d.Effects = append(d.d.Effects, e) }

// block moves the node to blocked and tells a person.
func (d *decider) block(reason BlockedReason, detail string) error {
	if err := d.move(NodeEvent{Trigger: TriggerBlock, Reason: reason}, d.lim, detail); err != nil {
		return err
	}
	d.effect(Effect{Kind: EffectNotify, Key: "notify", Title: fmt.Sprintf("Node blocked (%s)", reason), Body: detail, Priority: ports.PriorityHigh})
	return nil
}

func (d *decider) dispatch() error {
	if err := d.move(NodeEvent{Trigger: TriggerDispatched}, d.lim, ""); err != nil {
		return err
	}
	r := d.run()
	r.Step, r.Implementer, r.Fresh, r.GateFailures, r.Rejected = 0, nil, true, nil, false
	r.Ready, r.Tip, r.Integration = false, "", nil
	return d.start()
}

// start queues the work of the current step.
func (d *decider) start() error {
	r := d.run()
	st := d.flow.Steps[r.Step]
	switch st.Kind {
	case StepRole:
		d.effect(Effect{Kind: EffectSession, Key: "session", Step: r.Step, Role: st.Role, Resume: st.Role == RoleImplementer && r.Implementer != nil})
	case StepGate:
		d.effect(Effect{Kind: EffectGates, Key: "gates", Step: r.Step})
	case StepMergeQueue:
		r.Ready = true
	}
	return nil
}

// advance moves to step i, with the transition the new step needs.
func (d *decider) advance(i int) error {
	d.run().Step = i
	st := d.flow.Steps[i]
	var ev NodeTrigger
	switch {
	case st.Kind == StepRole && st.Phase == NodeInReview && d.node().State == NodeRunning:
		ev = TriggerImplementerDone
	case st.Kind == StepMergeQueue && d.node().State == NodeGating:
		ev = TriggerGatesPassed
	}
	if ev != "" {
		if err := d.move(NodeEvent{Trigger: ev}, d.lim, ""); err != nil {
			return err
		}
	}
	return d.start()
}

func (d *decider) session(in SessionDone) error {
	r := d.run()
	if in.Step != r.Step || d.flow.Steps[r.Step].Kind != StepRole {
		return fmt.Errorf("%w: session of step %d, node at step %d", ErrStaleInput, in.Step, r.Step)
	}
	st := d.flow.Steps[r.Step]
	switch {
	case in.Tampered:
		return d.fail("the session changed the repository outside its branch: " + in.Detail)
	case in.State != ports.StateFinished || len(in.Raw) == 0:
		return d.failed(st, in)
	case st.Role == RoleImplementer:
		return d.implemented(in)
	default:
		return d.reviewed(st, in)
	}
}

func (d *decider) retry(in SessionRetry) error {
	r := d.run()
	if in.Step != r.Step || d.flow.Steps[r.Step].Kind != StepRole {
		return fmt.Errorf("%w: retry of step %d, node at step %d", ErrStaleInput, in.Step, r.Step)
	}
	// A failed session gets a new one, not another resume of the same
	// conversation (prompt.md#sessões).
	if d.flow.Steps[r.Step].Role == RoleImplementer {
		r.Implementer = nil
	}
	return nil
}

// fail ends the node: the post-session check found tampering.
func (d *decider) fail(detail string) error {
	if err := d.move(NodeEvent{Trigger: TriggerFail}, d.lim, detail); err != nil {
		return err
	}
	d.effect(Effect{Kind: EffectNotify, Key: "notify", Title: "Node failed", Body: detail, Priority: ports.PriorityHigh})
	return nil
}

// failed applies the classification of a session that did not deliver
// (ADR 0024, item 7). Retryable causes reach the core only when the new
// sessions of the attempt ran out.
func (d *decider) failed(st FlowStep, in SessionDone) error {
	cause := in.Cause
	if cause == "" {
		cause = ports.CauseNoResult
	}
	detail := fmt.Sprintf("%s session: %s", st.Role, cause)
	if in.Detail != "" {
		detail += ": " + in.Detail
	}
	switch cause {
	case ports.CauseAuth, ports.CauseHostCredentials:
		d.d.Workspace = TriggerPause
		return d.block(BlockedPaused, detail+"; run kluzo doctor and resume the workspace")
	case ports.CauseBudget:
		return d.block(BlockedBudget, detail)
	default:
		return d.block(BlockedTriage, detail)
	}
}

func (d *decider) implemented(in SessionDone) error {
	var res NodeResult
	if err := json.Unmarshal(in.Raw, &res); err != nil {
		return fmt.Errorf("accepted node-result: %w", err)
	}
	r := d.run()
	if res.Status != "done" {
		return d.block(BlockedTriage, "the implementer gave up (status "+res.Status+")")
	}
	r.Implementer, r.Report, r.Responses, r.Head = in.Session, in.Raw, res.Responses, in.Head
	if in.Base != "" {
		r.Base = in.Base
	}
	r.Fresh, r.GateFailures, r.Integration = false, nil, nil
	if len(in.Proposal) > 0 {
		return d.block(BlockedProposal, "the work needs a human decision: "+strings.Join(in.Proposal, "; "))
	}
	n := d.node()
	r.Findings = r.Findings.SyncAuto(n.Attempt, n.Round+1, in.Auto)
	return d.advance(r.Step + 1)
}

func (d *decider) reviewed(st FlowStep, in SessionDone) error {
	var res ReviewResult
	if err := json.Unmarshal(in.Raw, &res); err != nil {
		return fmt.Errorf("accepted review-result: %w", err)
	}
	r, n := d.run(), d.node()
	r.Rejected = false
	out := DeriveReview(ReviewInput{
		Result: res, Findings: r.Findings, Attempt: n.Attempt, Round: n.Round + 1, MaxRounds: st.MaxRounds,
		Changed: in.Changed, Responses: r.Responses,
	})
	r.Findings = out.Findings
	switch out.Event.Trigger {
	case TriggerReviewApproved:
		if err := d.move(out.Event, d.lim, ""); err != nil {
			return err
		}
		return d.advance(r.Step + 1)
	case TriggerReviewRejected:
		lim := d.lim
		lim.MaxReviewRounds = st.MaxRounds
		if err := d.move(out.Event, lim, fmt.Sprintf("%d open findings", len(r.Findings.Open()))); err != nil {
			return err
		}
		r.Step, r.Rejected, r.RoundBase = st.OnReject, true, r.Head
		return d.start()
	default:
		return d.block(BlockedTriage, fmt.Sprintf("the review rounds are over with %d open findings or unmet criteria", len(r.Findings.Open())))
	}
}

func (d *decider) gates(in GatesDone) error {
	r := d.run()
	if in.Freshness {
		return d.freshGates(in)
	}
	if in.Step != r.Step || d.flow.Steps[r.Step].Kind != StepGate {
		return fmt.Errorf("%w: gates of step %d, node at step %d", ErrStaleInput, in.Step, r.Step)
	}
	st := d.flow.Steps[r.Step]
	failed := slices.DeleteFunc(slices.Clone(in.Results), func(g ports.GateResult) bool { return g.State == ports.GatePassed })
	if len(failed) == 0 {
		return d.advance(r.Step + 1)
	}
	names := make([]string, len(failed))
	for i, g := range failed {
		names[i] = g.Gate
	}
	detail := fmt.Sprintf("gates failed: %v", names)
	if st.Fast {
		lim := d.lim
		lim.MaxFastRetries = st.MaxRetries
		next, err := FastGateFailed(*d.node(), lim)
		if err != nil {
			return err
		}
		if next.State == NodeBlocked {
			return d.block(BlockedTriage, fmt.Sprintf("%s; the fast gates failed %d times", detail, next.FastGateFailures))
		}
		d.d.Next.Node = next
	} else {
		err := d.move(NodeEvent{Trigger: TriggerGateFailed}, d.lim, detail)
		if errors.Is(err, ErrLimitReached) {
			return d.block(BlockedTriage, detail+"; the attempts are over")
		}
		if err != nil {
			return err
		}
		r.Implementer, r.Fresh, r.Rejected = nil, true, false
	}
	r.Step, r.GateFailures = st.OnFail, failed
	return d.start()
}

// integrating checks that an input of the merge queue belongs to the node.
func (d *decider) integrating(what string) error {
	if d.node().State != NodeIntegrating || !d.run().Ready {
		return fmt.Errorf("%w: %s, node %s", ErrStaleInput, what, d.node().State)
	}
	return nil
}

// queueTurn starts the integration of a queued node: the rebase first.
func (d *decider) queueTurn() error {
	if err := d.move(NodeEvent{Trigger: TriggerQueueTurn}, d.lim, ""); err != nil {
		return err
	}
	d.effect(Effect{Kind: EffectRebase, Key: "rebase"})
	return nil
}

// rebased goes on after the rebase (ADR 0009): a conflict sends the node back
// to the implementer; otherwise it merges, after the full gates run again if
// the milestone moved since the commit the review judged (freshness gate).
func (d *decider) rebased(in Rebased) error {
	if err := d.integrating("rebase"); err != nil {
		return err
	}
	if in.Conflict != nil {
		return d.integrationFailed(in.Conflict, nil, "rebase conflict")
	}
	r := d.run()
	r.Head, r.Tip = in.Head, in.Tip
	f := &Freshness{Reviewed: r.Base, Tip: in.Tip, Rerun: in.Tip != r.Base}
	d.d.Freshness = f
	if f.Rerun {
		d.effect(Effect{Kind: EffectGates, Key: "freshness", Step: r.Step, Freshness: true})
		return nil
	}
	d.effect(Effect{Kind: EffectMerge, Key: "merge"})
	return nil
}

// freshGates merges after green freshness gates, or sends the node back.
func (d *decider) freshGates(in GatesDone) error {
	if err := d.integrating("freshness gates"); err != nil {
		return err
	}
	failed := slices.DeleteFunc(slices.Clone(in.Results), func(g ports.GateResult) bool { return g.State == ports.GatePassed })
	if len(failed) > 0 {
		return d.integrationFailed(&IntegrationFailure{Freshness: in.Changed}, failed, "freshness gates failed")
	}
	d.effect(Effect{Kind: EffectMerge, Key: "merge"})
	return nil
}

// tipMoved rebases the node again on the new tip.
func (d *decider) tipMoved() error {
	if err := d.integrating("moved tip"); err != nil {
		return err
	}
	d.effect(Effect{Kind: EffectRebase, Key: "rebase"})
	return nil
}

// merged ends the node: integrated.
func (d *decider) merged(in Merged) error {
	if err := d.integrating("merge"); err != nil {
		return err
	}
	return d.move(NodeEvent{Trigger: TriggerMerged}, d.lim, "merged at "+in.SHA)
}

// integrationFailed starts a new attempt with the cause, or blocks the node
// when the attempts are over.
func (d *decider) integrationFailed(cause *IntegrationFailure, gates []ports.GateResult, detail string) error {
	err := d.move(NodeEvent{Trigger: TriggerIntegrationFailed}, d.lim, detail)
	if errors.Is(err, ErrLimitReached) {
		return d.block(BlockedTriage, detail+"; the attempts are over")
	}
	if err != nil {
		return err
	}
	r := d.run()
	r.Step, r.Implementer, r.Fresh, r.Rejected, r.Ready, r.Tip = 0, nil, true, false, false, ""
	r.GateFailures, r.Integration = gates, cause
	return d.start()
}

// FullGates are the gates of the gate steps after the review: those the
// freshness gate runs again.
func (f Flow) FullGates() []string {
	var out []string
	for _, s := range f.Steps {
		if s.Kind == StepGate && !s.Fast {
			for _, g := range s.Gates {
				if !slices.Contains(out, g) {
					out = append(out, g)
				}
			}
		}
	}
	return out
}
