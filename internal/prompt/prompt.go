// Package prompt assembles what each agent session receives: the protocol, the
// role and the task (docs/contracts/prompt.md, ADR 0019). The assembly is
// deterministic: the same input gives the same text, except for the nonce.
package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
)

// ErrNonceCollision means external text contained the closing marker of every
// nonce tried; the session cannot be built.
var ErrNonceCollision = errors.New("untrusted text contains the envelope marker of the session")

// maxNonceTries bounds the recreation of the nonce on a collision.
const maxNonceTries = 5

// Input is everything the assembly needs.
type Input struct {
	Role      *config.Role
	Language  string // project.language
	Milestone string
	Task      Task
	// Rules are AGENTS.md and CLAUDE.md, and Skills the skills of the role, as
	// read from the tip of the milestone at dispatch (ADR 0024). A file with
	// nil Content does not exist.
	Rules  []ProjectFile
	Skills []ProjectFile
	// Schema is the JSON Schema of the role's output; it only feeds the list
	// of required fields in the Deliver section.
	Schema []byte
}

// ProjectFile is a project rules file or a skill, already read.
type ProjectFile struct {
	Name    string
	Content []byte
}

// Prompt is an assembled session prompt.
type Prompt struct {
	Protocol string // segment 1: system prompt
	Role     string // segment 2: system prompt
	Rules    string // segment 2b: system prompt, empty when the project has no rules
	Task     string // segment 3: user message
	Nonce    string // appears only in Task

	limits config.PromptLimits
	files  projectHashes
}

// System joins the segments of the system prompt, in order: protocol, role and
// project rules.
func (p Prompt) System() string {
	return strings.Join(slices.DeleteFunc([]string{p.Protocol, p.Role, p.Rules}, func(s string) bool { return s == "" }), "\n")
}

// Combined joins the segments, in order, into one message for a harness
// without a system prompt channel.
func (p Prompt) Combined() string { return p.System() + "\n\n" + p.Task }

// NewNonce returns 8 random hexadecimal characters.
func NewNonce(entropy io.Reader) (string, error) {
	var b [4]byte
	if _, err := io.ReadFull(entropy, b[:]); err != nil {
		return "", fmt.Errorf("read entropy for the nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// CryptoNonce is the nonce source for a real session.
func CryptoNonce() (string, error) { return NewNonce(rand.Reader) }

// Build assembles the three segments. nonce is asked for the nonce of the
// session; if external text contains its marker, a new one is asked for. A
// resumed session passes a source that always returns its own nonce, and gets
// ErrNonceCollision when it collides, which means the session must be
// recreated.
func Build(in Input, nonce func() (string, error)) (Prompt, error) {
	limits := in.Role.PromptLimits.Resolved()
	p := Prompt{
		Protocol: Protocol(ProtocolInput{
			Role: in.Role.Name, Issue: in.Task.Issue, Milestone: in.Milestone,
			Language: in.Language, MCP: in.Role.MCP, Output: in.Role.Output,
		}),
		Role:   in.Role.Body,
		Rules:  projectRules(in.Rules, in.Skills),
		limits: limits,
		files:  hashFiles(in.Rules, in.Skills),
	}
	for range maxNonceTries {
		n, err := nonce()
		if err != nil {
			return Prompt{}, err
		}
		r := &renderer{nonce: n, limits: limits, in: in}
		task := r.renderTask()
		if !r.collided {
			p.Task, p.Nonce = task, n
			return p, nil
		}
	}
	return Prompt{}, ErrNonceCollision
}
