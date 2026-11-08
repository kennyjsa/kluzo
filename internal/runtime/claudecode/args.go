package claudecode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// MCPPrefix is how Claude Code names the tools of the MCP server "kluzo".
const MCPPrefix = "mcp__kluzo__"

// maxSystemPrompt is the longest system prompt that fits in one argument
// (Linux limits a single argument to 128 KiB).
const maxSystemPrompt = 120 << 10

// builtinTools maps the canonical tools of a role to Claude Code tools
// (ADR 0024, item 5). test and bash both need Bash; they differ in what is
// allowed.
var builtinTools = map[string][]string{
	"read": {"Read", "Glob", "Grep"},
	"edit": {"Edit", "Write", "NotebookEdit"},
	"bash": {"Bash"},
	"test": {"Bash"},
}

// toolSet is the tools of a session: those made available (--tools), those
// allowed without asking (--allowedTools), and the MCP ones.
type toolSet struct {
	available []string
	allowed   []string
	mcp       []string
}

// mapTools computes the tool set of a role. test alone allows Bash only for the
// Verify commands, though that still runs code of the agent (ADR 0024).
func mapTools(canonical, verify, mcpTools []string) (toolSet, error) {
	var ts toolSet
	add := func(list *[]string, names ...string) {
		for _, n := range names {
			if !slices.Contains(*list, n) {
				*list = append(*list, n)
			}
		}
	}
	for _, c := range canonical {
		native, ok := builtinTools[c]
		if !ok {
			return toolSet{}, fmt.Errorf("tool %q has no Claude Code mapping", c)
		}
		add(&ts.available, native...)
		if c == "test" {
			continue
		}
		add(&ts.allowed, native...)
	}
	if slices.Contains(canonical, "test") && !slices.Contains(ts.allowed, "Bash") {
		for _, cmd := range verify {
			add(&ts.allowed, "Bash("+cmd+")")
		}
	}
	for _, t := range mcpTools {
		add(&ts.mcp, MCPPrefix+t)
	}
	add(&ts.allowed, ts.mcp...)
	return ts, nil
}

// expectedInit is the exact list of tools the init message must show.
func (ts toolSet) expectedInit() []string {
	all := append(slices.Clone(ts.available), ts.mcp...)
	slices.Sort(all)
	return slices.Compact(all)
}

// files are the paths of the per-session configuration files.
type files struct{ mcp, settings string }

// argv builds the command line after the binary (ADR 0024, item 1). With
// resume, --resume replaces --session-id.
func argv(spec ports.SessionSpec, ts toolSet, id string, resume bool, f files) ([]string, error) {
	if len(spec.System) > maxSystemPrompt {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ports.ErrPromptTooLarge, len(spec.System), maxSystemPrompt)
	}
	args := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--restricted", "--strict-mcp-config", "--disable-slash-commands",
	}
	if resume {
		args = append(args, "--resume", id)
	} else {
		args = append(args, "--session-id", id)
	}
	if m := spec.Role.Model; m != "" {
		args = append(args, "--model", m)
	}
	if e := spec.Role.Effort; e != "" {
		args = append(args, "--effort", e)
	}
	if n := spec.Role.MaxTurns; n > 0 {
		args = append(args, "--max-turns", strconv.Itoa(n))
	}
	if b := spec.MaxBudgetUSDMicros; b > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.6f", float64(b)/1e6))
	}
	return append(args,
		"--append-system-prompt", spec.System, "--exclude-dynamic-system-prompt-sections",
		"--tools", strings.Join(ts.available, ","), "--allowedTools", strings.Join(ts.allowed, ","),
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--mcp-config", f.mcp, "--settings", f.settings, "--include-hook-events",
	), nil
}

// volatile are the flags whose values change legitimately between the sessions
// of one harness conversation: the id flags, and the limits that follow what
// is left of the node.
var volatile = map[string]bool{"--session-id": true, "--resume": true, "--max-budget-usd": true, "--max-turns": true}

// freeze returns the command line as the manifest records it: the system
// prompt replaced by its hash, and the file paths of the session by their base
// names. It drops the volatile flags, so it doubles as the fingerprint input.
func freeze(args []string, keepVolatile bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case volatile[a] && !keepVolatile:
			i++
		case a == "--append-system-prompt" && i+1 < len(args):
			sum := sha256.Sum256([]byte(args[i+1]))
			out = append(out, a, "sha256:"+hex.EncodeToString(sum[:]))
			i++
		case (a == "--mcp-config" || a == "--settings") && i+1 < len(args):
			out = append(out, a, "<"+strings.TrimPrefix(a, "--")+" file>")
			i++
		default:
			out = append(out, a)
		}
	}
	return out
}

// fingerprint identifies what a resume must keep: the segments, the working
// directory, HOME and the command line without its volatile flags.
func fingerprint(spec ports.SessionSpec, args []string, home string) string {
	h := sha256.New()
	for _, part := range []string{spec.Segments.Protocol, spec.Segments.Role, spec.Segments.Rules, spec.Workdir, home, strings.Join(freeze(args, false), "\x00")} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
