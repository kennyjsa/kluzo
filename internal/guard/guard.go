// Package guard is the policy that keeps an agent session inside the rules of
// ADR 0020 and ADR 0024: no git that writes, no gh, no writes to protected
// paths, no reading of secret paths with a file tool. The same rules judge a
// tool call as it is about to run (the PreToolUse hook of the harness) and a
// change after the round (the check of the diff of item 8), so there is one
// definition of each rule.
//
// The hook is an early warning. The guarantee stays the check of the diff
// after the round, which does not depend on the harness.
package guard

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Rule names, recorded with every denial.
const (
	RuleGitWrite      = "git_write"
	RuleHostingCLI    = "hosting_cli"
	RuleProtectedPath = "protected_path"
	RuleSecretPath    = "secret_path"
	RuleOutsideTree   = "outside_worktree"
	// Rules of the check after the round (ADR 0020, item 2).
	RuleTestRemoved  = "test_removed"
	RuleTestDisabled = "test_disabled"
	RuleSuppression  = "suppression"
)

// Policy holds what the rules need to know about the project.
type Policy struct {
	Workdir        string   // worktree of the node: relative paths are relative to it
	ProtectedPaths []string // protected_paths of kluzo.yaml
	SecretPaths    []string // secret_paths of kluzo.yaml
}

// Call is a tool call of the harness, with the tool names of Claude Code.
type Call struct {
	Tool  string
	Input json.RawMessage
}

// Decision is the verdict on a call.
type Decision struct {
	Allowed bool
	Rule    string // set when denied
	Reason  string // set when denied; shown to the agent
}

// Change is a file changed in a round, for the check after the round.
type Change struct {
	Path    string // relative to the repository root, slash-separated
	Deleted bool
	Added   []Line // the lines the change added
	Removed []Line // the lines the change removed, numbered on the old side
}

// Line is a line of a file.
type Line struct {
	N    int
	Text string
}

// Violation is a rule broken by a change.
type Violation struct {
	Rule   string
	Path   string
	Line   int // the added line, for the rules that read lines
	Reason string
}

// Proposal reports whether the violation needs a human: the node is blocked
// and the change becomes a proposal. The others become automatic findings.
func (v Violation) Proposal() bool { return v.Rule != RuleSuppression }

// callRule judges a call; it returns nil when the rule has nothing to say.
// changeRule judges a change of a round; one change may break a rule more
// than once (a suppression per line).
type (
	callRule   func(Policy, Call, toolInput) *Decision
	changeRule func(Policy, Change) []Violation
)

func callRules() []callRule {
	return []callRule{ruleBash, ruleWrite, ruleSecretRead}
}

func changeRules() []changeRule {
	return []changeRule{ruleProtectedChange, ruleTestRemoved, ruleTestDeclRemoved, ruleTestDisabled, ruleSuppression}
}

// toolInput is the part of a tool input the rules read.
type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	Glob         string `json:"glob"`
	Command      string `json:"command"`
}

// Check judges a tool call.
func (p Policy) Check(c Call) Decision {
	var in toolInput
	_ = json.Unmarshal(c.Input, &in)
	for _, r := range callRules() {
		if d := r(p, c, in); d != nil {
			return *d
		}
	}
	return Decision{Allowed: true}
}

// CheckChanges judges the files a round changed.
func (p Policy) CheckChanges(changes []Change) []Violation {
	var out []Violation
	for _, ch := range changes {
		for _, r := range changeRules() {
			out = append(out, r(p, ch)...)
		}
	}
	return out
}

func deny(rule, format string, args ...any) *Decision {
	return &Decision{Rule: rule, Reason: fmt.Sprintf(format, args...)}
}

// rel returns the path relative to the worktree, slash-separated, and whether
// it stays inside it.
func (p Policy) rel(name string) (string, bool) {
	if name == "" {
		return "", true
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(p.Workdir, name)
	}
	r, err := filepath.Rel(p.Workdir, filepath.Clean(name))
	if err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return filepath.ToSlash(name), false
	}
	return filepath.ToSlash(r), true
}

func (p Policy) protected(name string) bool { return matchAny(p.ProtectedPaths, name) }
func (p Policy) secret(name string) bool    { return matchAny(p.SecretPaths, name) }

// ruleWrite denies the file tools that write outside the worktree or into a
// protected path.
func ruleWrite(p Policy, c Call, in toolInput) *Decision {
	if !slices.Contains([]string{"Edit", "Write", "NotebookEdit", "MultiEdit"}, c.Tool) {
		return nil
	}
	target := cmpOr(in.FilePath, in.NotebookPath)
	r, inside := p.rel(target)
	switch {
	case !inside:
		return deny(RuleOutsideTree, "%s writes outside the worktree: %s", c.Tool, target)
	case p.protected(r):
		return deny(RuleProtectedPath, "%s is a protected path: a change there becomes a proposal for a human; record the need in gaps instead", r)
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ruleSecretRead denies the file tools that read a secret path. Reading through
// the shell is a known limit (config.md).
func ruleSecretRead(p Policy, c Call, in toolInput) *Decision {
	var targets []string
	switch c.Tool {
	case "Read":
		targets = []string{in.FilePath}
	case "Glob":
		targets = []string{in.Pattern, in.Path}
	case "Grep":
		targets = []string{in.Path, in.Glob}
	default:
		return nil
	}
	for _, t := range targets {
		if t == "" {
			continue
		}
		r, _ := p.rel(t)
		if p.secret(r) || p.secret(path.Base(r)) || p.secret(strings.TrimPrefix(t, "./")) {
			return deny(RuleSecretPath, "%s may not read %s: it is a secret path", c.Tool, t)
		}
	}
	return nil
}

// ruleProtectedChange flags a changed file inside the protected paths.
func ruleProtectedChange(p Policy, ch Change) []Violation {
	if p.protected(ch.Path) {
		return []Violation{{Rule: RuleProtectedPath, Path: ch.Path, Reason: ch.Path + " is a protected path"}}
	}
	return nil
}
