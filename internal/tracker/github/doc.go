// Package github implements the Tracker, SCM and CI ports on the GitHub REST
// API (docs/architecture/integrations.md). It mirrors what it is told to
// mirror and decides nothing: when to label or comment is the core's call.
//
// Every write is idempotent. Comments, issues and pull requests carry a hidden
// HTML marker with the outbox idempotency key and are looked up before being
// created (ADR 0013); labels, dependencies and branches converge to the
// requested state. Secrets never reach an error: every error passes through
// platform/redact.
package github
