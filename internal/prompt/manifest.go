package prompt

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/kennyjsa/kluzo/internal/config"
	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Manifest is recorded in the session.started event
// (docs/contracts/prompt.md#manifesto-da-sessão).
type Manifest struct {
	KluzoVersion   string              `json:"kluzo_version"`
	Protocol       int                 `json:"protocol"`
	Harness        string              `json:"harness"`
	HarnessVersion string              `json:"harness_version"`
	Model          string              `json:"model"`
	Segments       SegmentHashes       `json:"segments"`
	ProjectRules   map[string]*string  `json:"project_rules"`
	Skills         map[string]string   `json:"skills"`
	Limits         config.PromptLimits `json:"limits"`
}

// SegmentHashes are the sha256 of the three segments.
type SegmentHashes struct {
	Protocol string `json:"protocol"`
	Role     string `json:"role"`
	Task     string `json:"task"`
}

// Runtime is what the harness adapter knows about the session and the prompt
// builder does not.
type Runtime struct {
	KluzoVersion   string
	Harness        string
	HarnessVersion string
	Model          string
	// ProjectRules maps a rules file (AGENTS.md, CLAUDE.md) to its content; a
	// nil value means the file does not exist.
	ProjectRules map[string][]byte
	// Skills maps a loaded skill to its content.
	Skills map[string][]byte
}

// Manifest describes the session that received the prompt.
func (p Prompt) Manifest(rt Runtime) Manifest {
	rules := make(map[string]*string, len(rt.ProjectRules))
	for name, data := range rt.ProjectRules {
		if data == nil {
			rules[name] = nil
			continue
		}
		h := hash(string(data))
		rules[name] = &h
	}
	skills := make(map[string]string, len(rt.Skills))
	for name, data := range rt.Skills {
		skills[name] = hash(string(data))
	}
	return Manifest{
		KluzoVersion: rt.KluzoVersion, Protocol: contracts.ProtocolVersion,
		Harness: rt.Harness, HarnessVersion: rt.HarnessVersion, Model: rt.Model,
		Segments:     SegmentHashes{Protocol: hash(p.Protocol), Role: hash(p.Role), Task: hash(p.Task)},
		ProjectRules: rules, Skills: skills,
		Limits: p.limits,
	}
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}
