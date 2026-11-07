package core

import "errors"

// Domain errors. Callers use errors.Is to tell them apart.
var (
	// ErrInvalidTransition means the trigger is not allowed from the current
	// state (docs/contracts/state-machine.md).
	ErrInvalidTransition = errors.New("invalid transition")
	// ErrLimitReached means the transition exists but a limit (review rounds,
	// attempts) has been exhausted; the caller hands the node to triage.
	ErrLimitReached = errors.New("limit reached")
)
