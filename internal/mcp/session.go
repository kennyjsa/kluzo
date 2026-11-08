package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Tool errors (docs/contracts/mcp.md#erros).
const (
	errForbidden     = "forbidden"
	errSessionClosed = "session_closed"
	errUnavailable   = "unavailable"
)

// maxCorrections is how many times a role may correct an invalid result.
const maxCorrections = 1

// maxReportedErrors bounds the validation errors sent back to the agent.
const maxReportedErrors = 20

// SessionSpec identifies a session. The agent never says who it is: the
// endpoint does.
type SessionSpec struct {
	ID        string
	Workspace string
	Node      string
	Role      string
	// Tools is the role's mcp: list; only these tools are accepted.
	Tools []string
	// Schema is the JSON Schema of the role's output (kluzo_result input).
	Schema []byte
}

// Outcome is what the session delivered through kluzo_result.
type Outcome struct {
	Accepted bool
	Result   json.RawMessage // the accepted JSON; empty when not accepted
	Errors   []string        // the validation errors of the last invalid call
	Calls    int             // kluzo_result calls made
}

// Session is one agent session's endpoint.
type Session struct {
	spec    SessionSpec
	server  *Server
	schema  *jsonschema.Schema
	handler http.Handler

	mu       sync.Mutex
	token    string // empty once revoked
	outcome  Outcome
	finished bool
	done     chan struct{}
}

func newSession(s *Server, spec SessionSpec) (*Session, error) {
	schema, err := compile(spec.Schema)
	if err != nil {
		return nil, fmt.Errorf("output schema of role %s: %w", spec.Role, err)
	}
	token, err := s.newToken()
	if err != nil {
		return nil, err
	}
	sess := &Session{spec: spec, server: s, schema: schema, token: token, done: make(chan struct{})}

	srv := sdk.NewServer(&sdk.Implementation{Name: "kluzo", Version: s.version}, nil)
	var inputSchema json.RawMessage = spec.Schema
	srv.AddTool(&sdk.Tool{
		Name:        ToolResult,
		Description: "Deliver the final result of this step. Call it once, with the JSON of the output schema of your role. If it is rejected you may correct it once.",
		InputSchema: inputSchema,
	}, sess.result)
	srv.AddReceivingMiddleware(sess.guard)
	sess.handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return sess, nil
}

func compile(schema []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:///output.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("mem:///output.json")
}

// URL is the endpoint of the session.
func (s *Session) URL() string { return "http://" + s.server.addr + "/s/" + s.spec.ID + "/mcp" }

// Token is the bearer token of the session.
func (s *Session) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// Done is closed when the session has delivered an accepted result, or has
// failed to deliver a valid one.
func (s *Session) Done() <-chan struct{} { return s.done }

// Outcome returns what the session delivered so far.
func (s *Session) Outcome() Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outcome
}

// Close revokes the token. It is called when the session ends, whatever the
// reason; later requests are refused.
func (s *Session) Close() {
	s.mu.Lock()
	s.token = ""
	s.mu.Unlock()
}

// record writes the mcp.call event.
func (s *Session) record(ctx context.Context, tool string, ok bool) {
	s.server.rec.Call(ctx, CallEvent{Session: s.spec.ID, Tool: tool, OK: ok})
}

// guard answers a call to any tool other than kluzo_result: forbidden when the
// role does not declare it, unavailable when it does but this version does not
// serve it yet.
func (s *Session) guard(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		if p, ok := req.GetParams().(*sdk.CallToolParamsRaw); ok && method == "tools/call" && p.Name != ToolResult {
			code := errForbidden
			if slices.Contains(s.spec.Tools, p.Name) && slices.Contains(contracts.MCPTools(), p.Name) {
				code = errUnavailable
			}
			s.record(ctx, p.Name, false)
			return toolError(code, p.Name), nil
		}
		return next(ctx, method, req)
	}
}

func toolError(code, tool string) *sdk.CallToolResult {
	body, _ := json.Marshal(map[string]string{"error": code, "tool": tool})
	return jsonText(string(body), true)
}

// result handles kluzo_result: it validates the output against the schema of
// the role and answers accepted/errors. One correction is allowed; after the
// second invalid call, or after an accepted one, the session is closed for
// results.
func (s *Session) result(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		s.record(ctx, ToolResult, false)
		return toolError(errSessionClosed, ToolResult), nil
	}
	errs := s.validate(req.Params.Arguments)
	s.outcome.Calls++
	switch {
	case len(errs) == 0:
		s.outcome.Accepted, s.outcome.Errors = true, nil
		s.outcome.Result = bytes.Clone(req.Params.Arguments)
		s.finish()
	case s.outcome.Calls > maxCorrections:
		s.outcome.Errors = errs
		s.finish()
	default:
		s.outcome.Errors = errs
	}
	s.mu.Unlock()

	s.record(ctx, ToolResult, len(errs) == 0)
	reply := map[string]any{"accepted": len(errs) == 0}
	if len(errs) > 0 {
		reply["errors"] = errs
	}
	body, _ := json.Marshal(reply)
	return jsonText(string(body), false), nil
}

func (s *Session) finish() {
	s.finished = true
	close(s.done)
}

// validate returns the validation errors of the arguments, as
// "$.path: message" lines; empty when valid.
func (s *Session) validate(args json.RawMessage) []string {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(args))
	if err != nil {
		return []string{"$: not valid JSON: " + err.Error()}
	}
	err = s.schema.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{"$: " + err.Error()}
	}
	seen := map[string]bool{}
	var out []string
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil {
			continue
		}
		line := instancePath(u.InstanceLocation) + ": " + u.Error.String()
		if !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	sort.Strings(out)
	if len(out) > maxReportedErrors {
		out = append(out[:maxReportedErrors], fmt.Sprintf("… %d more errors", len(out)-maxReportedErrors))
	}
	if len(out) == 0 {
		out = []string{"$: " + ve.Error()}
	}
	return out
}

// instancePath turns a JSON pointer ("/findings/0/severity") into
// "$.findings[0].severity".
func instancePath(ptr string) string {
	var b strings.Builder
	b.WriteString("$")
	for _, seg := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		if seg == "" {
			continue
		}
		seg = strings.NewReplacer("~1", "/", "~0", "~").Replace(seg)
		if _, err := strconv.Atoi(seg); err == nil {
			b.WriteString("[" + seg + "]")
		} else {
			b.WriteString("." + seg)
		}
	}
	return b.String()
}
