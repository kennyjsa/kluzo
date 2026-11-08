package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Role is roles/<name>.md: YAML frontmatter plus the prompt body (segment 2 of
// the prompt, docs/contracts/prompt.md).
type Role struct {
	Name         string       `yaml:"-"`
	Version      int          `yaml:"version"`
	Protocol     int          `yaml:"protocol"`
	Description  string       `yaml:"description"`
	Harness      Harness      `yaml:"harness"`
	Model        string       `yaml:"model"`
	Advisor      string       `yaml:"advisor"`
	Subagents    string       `yaml:"subagents"`
	Effort       string       `yaml:"effort"`
	Tools        []string     `yaml:"tools"`
	Skills       []string     `yaml:"skills"`
	MCP          []string     `yaml:"mcp"`
	Output       string       `yaml:"output"`
	Budget       RoleBudget   `yaml:"budget"`
	MaxTurns     int          `yaml:"max_turns"`
	PromptLimits PromptLimits `yaml:"prompt_limits"`
	Fallback     []Override   `yaml:"fallback"`
	Body         string       `yaml:"-"`
	bodyLine     int          // file line where Body starts
}

// RoleBudget limits one role step.
type RoleBudget struct {
	Minutes int     `yaml:"minutes"`
	USD     float64 `yaml:"usd"`
}

// PromptLimits overrides the limits of docs/contracts/prompt.md#limites.
// Zero means "use the default".
type PromptLimits struct {
	Findings       int `yaml:"findings" json:"findings"`
	TestFailures   int `yaml:"test_failures" json:"test_failures"`
	FailureMessage int `yaml:"failure_message_chars" json:"failure_message_chars"`
	LogHead        int `yaml:"log_head" json:"log_head"`
	LogTail        int `yaml:"log_tail" json:"log_tail"`
	LogChars       int `yaml:"log_chars" json:"log_chars"`
	ChangedFiles   int `yaml:"changed_files" json:"changed_files"`
	DiffLines      int `yaml:"diff_lines" json:"diff_lines"`
}

// Default prompt limits (docs/contracts/prompt.md#limites).
const (
	DefaultFindings       = 20
	DefaultTestFailures   = 10
	DefaultFailureMessage = 1500
	DefaultLogHead        = 60
	DefaultLogTail        = 120
	DefaultLogChars       = 12000
	DefaultChangedFiles   = 50
	DefaultDiffLines      = 1500
)

// Resolved returns the limits with every zero replaced by its default.
func (p PromptLimits) Resolved() PromptLimits {
	or := func(v, def int) int {
		if v == 0 {
			return def
		}
		return v
	}
	return PromptLimits{
		Findings: or(p.Findings, DefaultFindings), TestFailures: or(p.TestFailures, DefaultTestFailures),
		FailureMessage: or(p.FailureMessage, DefaultFailureMessage), LogHead: or(p.LogHead, DefaultLogHead),
		LogTail: or(p.LogTail, DefaultLogTail), LogChars: or(p.LogChars, DefaultLogChars),
		ChangedFiles: or(p.ChangedFiles, DefaultChangedFiles), DiffLines: or(p.DiffLines, DefaultDiffLines),
	}
}

// Override replaces the harness and/or the model of a role (a fallback step).
type Override struct {
	Harness Harness `yaml:"harness"`
	Model   string  `yaml:"model"`
}

var (
	roleName    = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	toolMention = regexp.MustCompile(`kluzo_[a-z]+(?:_[a-z]+)*`)
)

// splitFrontmatter separates the YAML block between the first two `---` lines
// from the markdown body. bodyLine is the file line where the body starts.
func splitFrontmatter(data string) (front, body string, bodyLine int, err string) {
	lines := strings.Split(data, "\n")
	if strings.TrimRight(lines[0], "\r") != "---" {
		return "", "", 0, "a role must start with a `---` frontmatter line"
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), i + 2, ""
		}
	}
	return "", "", 0, "frontmatter is not closed by a `---` line"
}

// parseRole decodes one role file. It returns nil if the file cannot be parsed.
func parseRole(file, name, data string) (*Role, *source, Errors) {
	front, body, bodyLine, msg := splitFrontmatter(data)
	if msg != "" {
		return nil, nil, Errors{{File: file, Line: 1, Msg: msg}}
	}
	ro := &Role{Name: name}
	src, errs := decode(file, []byte(front), 1, ro)
	if errs != nil {
		return nil, nil, errs
	}
	ro.Body, ro.bodyLine = body, bodyLine
	return ro, src, nil
}

func (ro *Role) validate(r report, d Defaults, outputs []string) {
	if !roleName.MatchString(ro.Name) {
		r.add("", fmt.Sprintf("role name %q (from the file name) must be lowercase letters, digits, - or _", ro.Name))
	}
	if ro.Version != 1 {
		r.add("version", fmt.Sprintf("unsupported role version %d (want 1)", ro.Version))
	}
	if ro.Protocol != contracts.ProtocolVersion {
		r.add("protocol", fmt.Sprintf("role written for protocol %d, this binary implements protocol %d", ro.Protocol, contracts.ProtocolVersion))
	}
	if ro.Description == "" {
		r.add("description", "required")
	}
	ro.validateEffort(r)
	harness, model := ro.validateModels(r, d)
	if !r.src.has("tools") {
		r.add("tools", "required")
	}
	ro.validateTools(r)
	ro.validateMCP(r)
	if !slices.Contains(outputs, ro.Output) {
		msg := "required"
		if ro.Output != "" {
			msg = fmt.Sprintf("unknown output schema %q (built-in: %s; add yours in schemas/)", ro.Output, strings.Join(contracts.OutputSchemas(), ", "))
		}
		r.add("output", msg)
	}
	if ro.Budget.Minutes < 0 || ro.Budget.USD < 0 {
		r.add("budget", "limits must not be negative")
	}
	if ro.MaxTurns < 0 {
		r.add("max_turns", "must not be negative")
	}
	if ro.PromptLimits.negative() {
		r.add("prompt_limits", "limits must not be negative")
	}
	ro.validateFallback(r, harness, model)
	ro.validateBody(r)
}

func (ro *Role) validateEffort(r report) {
	if e := ro.Effort; e != "" && !slices.Contains([]string{"low", "medium", "high"}, e) {
		r.add("effort", fmt.Sprintf("unknown effort %q (want low, medium or high)", e))
	}
}

func (p PromptLimits) negative() bool {
	return slices.ContainsFunc([]int{p.Findings, p.TestFailures, p.FailureMessage, p.LogHead, p.LogTail, p.LogChars, p.ChangedFiles, p.DiffLines},
		func(n int) bool { return n < 0 })
}

// validateModels checks the effective harness + model pair, the advisor and
// the subagents model. It returns the effective harness and model.
func (ro *Role) validateModels(r report, d Defaults) (Harness, string) {
	harness, model := ro.Harness, ro.Model
	if harness == "" {
		harness = d.Harness
	}
	if model == "" {
		model = d.Model
	}
	switch {
	case harness == "":
		r.add("harness", "no harness: set it here or in defaults.harness")
	case !harness.known():
		r.add("harness", checkPair(harness, model))
	default:
		if msg := checkPair(harness, model); msg != "" {
			r.add("model", msg)
		}
	}
	for _, f := range []struct{ key, value string }{{"advisor", ro.Advisor}, {"subagents", ro.Subagents}} {
		if f.value == "" {
			continue
		}
		if harness != ClaudeCode {
			r.add(f.key, fmt.Sprintf("%s is only supported by claude-code (this role uses %q)", f.key, harness))
		} else if msg := checkPair(ClaudeCode, f.value); msg != "" {
			r.add(f.key, msg)
		}
	}
	if ra, rm := modelRank(ro.Advisor), modelRank(model); ra > 0 && rm > 0 && ra < rm {
		r.add("advisor", fmt.Sprintf("advisor %q is weaker than model %q", ro.Advisor, model))
	}
	return harness, model
}

func (ro *Role) validateTools(r report) {
	for i, t := range ro.Tools {
		p := idx("tools", i)
		if known := contracts.Tools(); !slices.Contains(known, t) {
			r.add(p, fmt.Sprintf("unknown tool %q (known: %s)", t, strings.Join(known, ", ")))
		}
	}
}

func (ro *Role) validateMCP(r report) {
	if !slices.Contains(ro.MCP, "kluzo_result") {
		r.add("mcp", "must include kluzo_result: every role delivers its output through it")
	}
	for i, t := range ro.MCP {
		if known := contracts.MCPTools(); !slices.Contains(known, t) {
			r.add(idx("mcp", i), fmt.Sprintf("unknown tool %q (known: %s)", t, strings.Join(known, ", ")))
		}
	}
}

// validateFallback checks that each override still yields a valid pair.
func (ro *Role) validateFallback(r report, harness Harness, model string) {
	for i, o := range ro.Fallback {
		p := idx("fallback", i)
		if o.Harness == "" && o.Model == "" {
			r.add(p, "set harness, model or both")
			continue
		}
		fh, fm := harness, model
		if o.Harness != "" {
			fh = o.Harness
		}
		if o.Model != "" {
			fm = o.Model
		}
		if msg := checkPair(fh, fm); msg != "" {
			key := "model"
			if !fh.known() {
				key = "harness"
			}
			r.add(join(p, key), msg)
		}
	}
}

// validateBody applies the checks of docs/contracts/prompt.md#segmento-2--papel
// to the markdown body: no reserved markers, and no kluzo_* tool outside mcp.
func (ro *Role) validateBody(r report) {
	for i, line := range strings.Split(ro.Body, "\n") {
		n := ro.bodyLine + i
		for _, m := range contracts.ReservedMarkers() {
			if strings.Contains(line, m) {
				r.addAt(n, "", fmt.Sprintf("the body contains the reserved marker %q", m))
			}
		}
		seen := map[string]bool{}
		for _, t := range toolMention.FindAllString(line, -1) {
			if !seen[t] && !slices.Contains(ro.MCP, t) {
				r.addAt(n, "", fmt.Sprintf("the body mentions %s, which is not listed in mcp", t))
			}
			seen[t] = true
		}
	}
}
