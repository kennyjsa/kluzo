package claudecode

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/exec"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// outcome is everything known when the process has ended.
type outcome struct {
	accepted  json.RawMessage // the kluzo_result the MCP server accepted, if any
	abort     *abort          // why Kluzo stopped the session, if it did
	cancelled bool            // the caller asked to stop
	timedOut  bool
	end       *ended // the result message of the stream
	exit      exec.Status
	stderr    string
	t         *tracker
}

// authHint matches what the CLI writes to stderr when the credential is
// refused before the stream starts.
var authHint = regexp.MustCompile(`(?i)invalid api key|authentication_error|not logged in|please run /login|401`)

// classify decides how the session ended (ADR 0024, item 7). A kluzo_result
// accepted by the server wins over any later error; only a failure of the
// safety checks of the session (CauseGuard) beats it.
func classify(o outcome) ports.Result {
	// Without a result message the harness total is unknown: the cumulative
	// carries the prior one forward, so the next resume does not count the
	// earlier sessions again.
	res := ports.Result{Turns: o.t.turns, Unknown: o.t.unknown, Version: o.t.version, Cumulative: o.t.prior}
	if o.end != nil {
		res.CostUSD, res.Cumulative = o.end.Session.CostUSD, o.end.Cumulative
		res.Turns = max(res.Turns, o.end.Turns)
	}
	fail := func(cause ports.Cause, detail string) ports.Result {
		res.State, res.Cause, res.Detail = ports.StateFailed, cause, o.t.redact(detail)
		return res
	}
	switch {
	case o.abort != nil && o.abort.cause == ports.CauseGuard:
		return fail(o.abort.cause, o.abort.detail)
	case o.accepted != nil:
		res.State, res.Raw = ports.StateFinished, o.accepted
		return res
	case o.cancelled:
		res.State = ports.StateCancelled
		return res
	case o.timedOut:
		res.State = ports.StateTimeout
		return res
	case o.abort != nil:
		return fail(o.abort.cause, o.abort.detail)
	case o.end == nil:
		detail := fmt.Sprintf("the process ended without a result (exit code %d%s): %s", o.exit.Code, signalNote(o.exit), strings.TrimSpace(o.stderr))
		if authHint.MatchString(o.stderr) {
			return fail(ports.CauseAuth, detail)
		}
		return fail(ports.CauseRuntime, detail)
	}
	return failure(o.end, fail)
}

func signalNote(st exec.Status) string {
	if st.Signal == "" {
		return ""
	}
	return ", " + st.Signal
}

// failure classifies a result message that is not an accepted success.
func failure(e *ended, fail func(ports.Cause, string) ports.Result) ports.Result {
	detail := fmt.Sprintf("%s (terminal reason %q, API status %d): %s", e.Subtype, e.TerminalReason, e.APIStatus, e.Text)
	switch {
	case e.Subtype == "error_max_turns":
		return fail(ports.CauseMaxTurns, detail)
	case e.Subtype == "error_max_budget_usd":
		return fail(ports.CauseBudget, detail)
	case e.StopReason == "refusal":
		return fail(ports.CauseRefusal, detail)
	case e.APIStatus == 401 || e.APIStatus == 403:
		return fail(ports.CauseAuth, detail)
	case e.APIStatus == 429 || e.APIStatus == 529:
		return fail(ports.CauseRateLimit, detail)
	case e.Subtype == "success" && !e.IsError:
		return fail(ports.CauseNoResult, "the session ended without an accepted kluzo_result")
	}
	return fail(ports.CauseRuntime, detail) // error_during_execution, API errors, structured output retries
}
