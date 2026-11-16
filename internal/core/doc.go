// Package core holds the deterministic domain of Kluzo: the state machines of
// workspaces and nodes, the scheduler and the flow evaluation.
//
// The core performs no I/O and never reads the clock or random sources
// (ADR 0001, ADR 0012). Everything it needs arrives as data; everything it
// decides leaves as data. The rules are checked by internal/archtest.
package core
