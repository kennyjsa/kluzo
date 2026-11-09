package core

import (
	"fmt"
	"strings"
)

// The decisions of a person on a node from the CLI, without the automatic
// triage of v0.2 (v0.1 item 10b): the proposal a node is blocked for, the
// exit of a blocked node (unblock or a new attempt), and the cancellation.

// ProposalChange is a change of the work that needs a person (ADR 0020): a
// protected path, a test deleted or disabled. Hash is the SHA-256 of the
// content of Path at the head of the round ("" when it was deleted): an
// approved change is an exception for that path with that content.
type ProposalChange struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Hash   string `json:"sha256"`
}

// reasons are the reasons of the changes, in order.
func reasons(cs []ProposalChange) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Reason
	}
	return strings.Join(out, "; ")
}

// ProposalDecided is the decision of a person on the proposal a node is
// blocked for. An approval lets the work go on to the next step; a rejection
// sends the node back to the implementer, with an automatic finding per
// change asking to revert it.
type ProposalDecided struct {
	Approved bool
	By       string
	Changes  []ProposalChange
}

// Unblock is a person who resolved what blocked the node: it goes back to
// the state it was blocked in, and its current step runs again.
type Unblock struct{ By string }

// Retry is a person asking for a new attempt of a blocked node that was
// dispatched, while attempts are left.
type Retry struct{ By string }

// Cancel is a person removing the node from the milestone. The node must not
// be in the middle of work (running, in review, gating or integrating).
type Cancel struct{ By string }

func (ProposalDecided) isInput() {}
func (Unblock) isInput()         {}
func (Retry) isInput()           {}
func (Cancel) isInput()          {}

// TriggerRetry starts a new attempt of a blocked node (`kluzo node retry`).
const TriggerRetry NodeTrigger = "retry"

// blockedFor checks that a person may take the blocked node out of its block:
// a proposal is decided with kluzo proposal, a question answered.
func (d *decider) blockedFor(what string) error {
	n := d.node()
	switch {
	case n.State != NodeBlocked:
		return fmt.Errorf("%w: %s a node %s, not blocked", ErrInvalidTransition, what, n.State)
	case n.BlockedReason == BlockedProposal:
		return fmt.Errorf("%w: the node is blocked for a proposal: approve or reject it (kluzo proposal)", ErrInvalidTransition)
	case n.BlockedReason == BlockedQuestion:
		return fmt.Errorf("%w: the node is blocked for a question: answer it", ErrInvalidTransition)
	}
	return nil
}

// proposalDecided applies the decision on the proposal the node is blocked for.
func (d *decider) proposalDecided(in ProposalDecided) error {
	n := d.node()
	if n.State != NodeBlocked || n.BlockedReason != BlockedProposal {
		return fmt.Errorf("%w: the node is %s, not blocked for a proposal", ErrInvalidTransition, n.State)
	}
	r := d.run()
	if in.Approved {
		if err := d.move(NodeEvent{Trigger: TriggerUnblock}, d.lim, "proposal approved by "+in.By); err != nil {
			return err
		}
		return d.advance(r.Step + 1)
	}
	if err := d.move(NodeEvent{Trigger: TriggerUnblock}, d.lim, "proposal rejected by "+in.By); err != nil {
		return err
	}
	revert := make([]Finding, len(in.Changes))
	for i, c := range in.Changes {
		revert[i] = Finding{Severity: SeverityBlocking, Category: "scope", File: c.Path, Auto: true,
			Message: "revert this change (" + c.Reason + "): the proposal to keep it was rejected by " + in.By,
			Check:   "the file is as it was at the start of the attempt"}
	}
	r.Findings, _ = r.Findings.Add(n.Attempt, n.Round+1, revert...)
	r.Rejected = true
	return d.start()
}

// unblock takes the node back to the state it was blocked in, and runs its
// current step again: the session or the gates of the step, or the
// integration from the rebase. The failures of the fast gates count again
// from zero, after the action of the person.
func (d *decider) unblock(in Unblock) error {
	if err := d.blockedFor("unblock"); err != nil {
		return err
	}
	if err := d.move(NodeEvent{Trigger: TriggerUnblock}, d.lim, "unblocked by "+in.By); err != nil {
		return err
	}
	d.node().FastGateFailures = 0
	switch d.node().State {
	case NodeRunning, NodeInReview, NodeGating:
		return d.start()
	case NodeIntegrating:
		d.effect(Effect{Kind: EffectRebase, Key: "rebase"})
	}
	return nil // planned, ready and queued wait for the scheduler
}

// retryAttempt starts a new attempt of the blocked node from the first step,
// with a new worktree, when the node was dispatched and attempts are left.
func (d *decider) retryAttempt(in Retry) error {
	if err := d.blockedFor("retry"); err != nil {
		return err
	}
	if err := d.move(NodeEvent{Trigger: TriggerRetry}, d.lim, "new attempt asked by "+in.By); err != nil {
		return err
	}
	r := d.run()
	r.Step, r.Implementer, r.Fresh, r.Rejected, r.Ready, r.Tip = 0, nil, true, false, false, ""
	r.GateFailures, r.Integration = nil, nil
	return d.start()
}

// cancel removes the node from the milestone; its cleanup follows.
func (d *decider) cancel(in Cancel) error {
	switch s := d.node().State; s {
	case NodeRunning, NodeInReview, NodeGating, NodeIntegrating:
		return fmt.Errorf("%w: the node is %s: pause the workspace and wait for the work in flight, or block it first", ErrInvalidTransition, s)
	}
	return d.move(NodeEvent{Trigger: TriggerCancel}, d.lim, "cancelled by "+in.By)
}
