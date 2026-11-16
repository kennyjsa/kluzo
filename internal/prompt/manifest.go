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
	Rules    string `json:"rules"`
	Task     string `json:"task"`
}

// Runtime is what the harness adapter knows about the session and the prompt
// builder does not.
type Runtime struct {
	KluzoVersion   string
	Harness        string
	HarnessVersion string
	Model          string
}

// projectHashes are the hashes of the project files in segment 2b.
type projectHashes struct {
	rules  map[string]*string // nil: the file does not exist
	skills map[string]string
}

func hashFiles(rules, skills []ProjectFile) projectHashes {
	h := projectHashes{rules: map[string]*string{}, skills: map[string]string{}}
	for _, f := range rules {
		if f.Content == nil {
			h.rules[f.Name] = nil
			continue
		}
		sum := hash(string(f.Content))
		h.rules[f.Name] = &sum
	}
	for _, f := range skills {
		h.skills[f.Name] = hash(string(f.Content))
	}
	return h
}

// Manifest describes the session that received the prompt.
func (p Prompt) Manifest(rt Runtime) Manifest {
	return Manifest{
		KluzoVersion: rt.KluzoVersion, Protocol: contracts.ProtocolVersion,
		Harness: rt.Harness, HarnessVersion: rt.HarnessVersion, Model: rt.Model,
		Segments:     SegmentHashes{Protocol: hash(p.Protocol), Role: hash(p.Role), Rules: hash(p.Rules), Task: hash(p.Task)},
		ProjectRules: p.files.rules, Skills: p.files.skills,
		Limits: p.limits,
	}
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}
