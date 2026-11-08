package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Harness identifies an agent runtime.
type Harness string

// Known harnesses (docs/contracts/config.md).
const (
	ClaudeCode Harness = "claude-code"
	OpenCode   Harness = "opencode"
)

func (h Harness) known() bool { return h == ClaudeCode || h == OpenCode }

var (
	claudeAliases = map[string]bool{"opus": true, "sonnet": true, "haiku": true}
	openCodeModel = regexp.MustCompile(`^[A-Za-z0-9._-]+/\S+$`)
)

// checkPair validates a harness + model pair. The model catalog is not
// closed, so the check is by shape (docs/planning/questions.md, Q1).
func checkPair(h Harness, model string) string {
	switch {
	case !h.known():
		return fmt.Sprintf("unknown harness %q (want claude-code or opencode)", h)
	case model == "":
		return "no model: set it here or in defaults.model"
	case h == ClaudeCode && !claudeAliases[model] && !strings.HasPrefix(model, "claude-"):
		return fmt.Sprintf("model %q is not valid for claude-code (want opus, sonnet, haiku or a claude-* id)", model)
	case h == OpenCode && !openCodeModel.MatchString(model):
		return fmt.Sprintf("model %q is not valid for opencode (want provider/model)", model)
	}
	return ""
}

// modelRank orders Claude model families by strength. 0 means unknown.
func modelRank(model string) int {
	switch {
	case strings.Contains(model, "haiku"):
		return 1
	case strings.Contains(model, "sonnet"):
		return 2
	case strings.Contains(model, "opus"):
		return 3
	}
	return 0
}
