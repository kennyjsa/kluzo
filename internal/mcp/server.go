// Package mcp serves the kluzo_* tools to agent sessions over MCP
// (docs/contracts/mcp.md, ADR 0007). Each session gets its own endpoint on
// 127.0.0.1, protected by a token that is revoked when the session ends. The
// package validates and relays; what happens with a result is decided by the
// supervisor and the core.
//
// v0.1 serves only kluzo_result (ADR 0019).
package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolResult is the only tool of v0.1.
const ToolResult = "kluzo_result"

// CallEvent is the payload of the mcp.call event (docs/contracts/events.md).
type CallEvent struct {
	Session   string
	Workspace string
	Node      string
	Tool      string
	OK        bool
}

// Recorder writes the mcp.call event of every call to the journal.
type Recorder interface {
	Call(ctx context.Context, e CallEvent)
}

// Server is the local MCP server of the Kluzo process.
type Server struct {
	rec     Recorder
	entropy io.Reader
	version string

	mu       sync.Mutex
	sessions map[string]*Session
	http     *http.Server
	addr     string
}

// New returns a server that records every call with rec and draws session
// tokens from entropy (crypto/rand in production). version is announced to
// the clients.
func New(rec Recorder, entropy io.Reader, version string) *Server {
	return &Server{rec: rec, entropy: entropy, version: version, sessions: map[string]*Session{}}
}

// Start listens on a free port of 127.0.0.1 and serves until Shutdown.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for MCP: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/s/{id}/mcp", s.serve)
	s.http = &http.Server{Handler: mux}
	s.addr = ln.Addr().String()
	go func() { _ = s.http.Serve(ln) }() // Serve ends with ErrServerClosed after Shutdown
	return nil
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// Open registers a session and returns it, with its endpoint and token.
// The session id must be unique in the server.
func (s *Server) Open(spec SessionSpec) (*Session, error) {
	if s.http == nil {
		return nil, errors.New("MCP server not started")
	}
	sess, err := newSession(s, spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.sessions[spec.ID]; dup {
		return nil, fmt.Errorf("session %s is already open", spec.ID)
	}
	s.sessions[spec.ID] = sess
	return sess, nil
}

func (s *Server) newToken() (string, error) {
	var b [32]byte
	if _, err := io.ReadFull(s.entropy, b[:]); err != nil {
		return "", fmt.Errorf("read entropy for the session token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// serve authenticates the request with the token of the session in the path
// and hands it to that session's MCP handler.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.sessions[r.PathValue("id")]
	s.mu.Unlock()
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if sess == nil || !ok || !sess.authorize(token) {
		http.Error(w, `{"error":"session_closed"}`, http.StatusUnauthorized)
		return
	}
	sess.handler.ServeHTTP(w, r)
}

// authorize compares the token in constant time; a revoked token never matches.
func (s *Session) authorize(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

// jsonText is a tool response whose content is a JSON document.
func jsonText(v string, isError bool) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: v}}, IsError: isError}
}
