package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
)

// Config is kluzo.yaml (docs/contracts/config.md).
type Config struct {
	Version        int                 `yaml:"version"`
	Project        ProjectInfo         `yaml:"project"`
	Tracker        Tracker             `yaml:"tracker"`
	Branches       Branches            `yaml:"branches"`
	Areas          map[string][]string `yaml:"areas"`
	ProtectedPaths []string            `yaml:"protected_paths"`
	// SecretPaths are files the harness hook refuses to let a file tool read.
	SecretPaths    []string            `yaml:"secret_paths"`
	Worktree       Worktree            `yaml:"worktree"`
	Caches         []string            `yaml:"caches"` // ecosystems with a shared download cache (ADR 0025)
	Retention      Retention           `yaml:"retention"`
	Gates          []Gate              `yaml:"gates"`
	MilestoneGates []Gate              `yaml:"milestone_gates"`
	Scheduler      Scheduler           `yaml:"scheduler"`
	CodeMap        CodeMap             `yaml:"code_map"`
	Security       Security            `yaml:"security"`
	Budgets        Budgets             `yaml:"budgets"`
	Runtimes       map[Harness]Runtime `yaml:"runtimes"`
	Defaults       Defaults            `yaml:"defaults"`
	Notify         *Notify             `yaml:"notify"`
}

// ProjectInfo names the project and the language of agent-written text.
type ProjectInfo struct {
	Name     string `yaml:"name"`
	Language string `yaml:"language"`
}

// Tracker is the issue tracker and SCM the milestone lives in.
type Tracker struct {
	Kind         string        `yaml:"kind"`
	Repo         string        `yaml:"repo"`
	Auth         TrackerAuth   `yaml:"auth"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Operators    []string      `yaml:"operators"`
}

// TrackerAuth says how Kluzo authenticates to the tracker.
type TrackerAuth struct {
	Kind       string `yaml:"kind"`
	AppIDEnv   string `yaml:"app_id_env"`
	KeyFileEnv string `yaml:"key_file_env"`
	// TokenEnv names the variable that holds the token when Kind is token.
	// Empty means the default of the tracker (see TokenEnvName).
	TokenEnv string `yaml:"token_env"`
}

// Default token variables by tracker kind (docs/contracts/config.md).
const (
	DefaultGitHubTokenEnv = "KLUZO_GH_TOKEN"
	DefaultGitLabTokenEnv = "KLUZO_GL_TOKEN"
)

// TokenEnvName returns the variable the token is read from: token_env, or the
// default of the tracker kind. Only meaningful when Kind is token.
func (a TrackerAuth) TokenEnvName(trackerKind string) string {
	switch {
	case a.TokenEnv != "":
		return a.TokenEnv
	case trackerKind == "gitlab":
		return DefaultGitLabTokenEnv
	default:
		return DefaultGitHubTokenEnv
	}
}

// Branches holds the branch naming scheme.
type Branches struct {
	Main            string `yaml:"main"`
	MilestonePrefix string `yaml:"milestone_prefix"`
	NodePattern     string `yaml:"node_pattern"`
}

// Gate is a check that decides whether a node (or milestone) may advance:
// either a command or the tracker's CI checks.
type Gate struct {
	Name           string        `yaml:"name"`
	Run            string        `yaml:"run"`
	Source         string        `yaml:"source"`
	RequiredChecks []string      `yaml:"required_checks"`
	Fast           bool          `yaml:"fast"`
	Timeout        time.Duration `yaml:"timeout"`
	EnvAllow       []string      `yaml:"env_allow"`
}

// Scheduler configures node dispatch.
type Scheduler struct {
	MaxParallel int `yaml:"max_parallel"`
}

// CodeMap configures the code map used by the kluzo_code_* tools (v0.3, ADR 0021).
type CodeMap struct {
	Adapter string   `yaml:"adapter"` // none | cartog | lsp
	Exclude []string `yaml:"exclude"` // globs left out of the map
}

// Security holds the switches that relax the default isolation (ADR 0020).
type Security struct {
	AllowHostCredentials bool `yaml:"allow_host_credentials"`
}

// Budgets are the default spending limits.
type Budgets struct {
	Node      NodeBudget      `yaml:"node"`
	Milestone MilestoneBudget `yaml:"milestone"`
}

// NodeBudget limits one node.
type NodeBudget struct {
	Attempts int     `yaml:"attempts"`
	Minutes  int     `yaml:"minutes"`
	USD      float64 `yaml:"usd"`
}

// MilestoneBudget limits a whole milestone.
type MilestoneBudget struct {
	USD     float64 `yaml:"usd"`
	AlertAt float64 `yaml:"alert_at"`
}

// Worktree prepares each worktree before the first session of a node.
type Worktree struct {
	Setup   string        `yaml:"setup"`   // command read from the milestone tip
	Include []string      `yaml:"include"` // ignored files copied from the main checkout
	Timeout time.Duration `yaml:"timeout"`
}

// Retention limits what the workspace keeps (ADR 0025).
type Retention struct {
	Logs             string `yaml:"logs"`               // e.g. 90d
	DiskWarn         string `yaml:"disk_warn"`          // e.g. 20GB
	DockerBuildCache string `yaml:"docker_build_cache"` // e.g. 10GB, or off
}

// Runtime configures one harness CLI.
type Runtime struct {
	Bin        string   `yaml:"bin"`
	MinVersion string   `yaml:"min_version"`
	EnvAllow   []string `yaml:"env_allow"`
}

// Defaults are the harness and model used by roles that do not set their own.
type Defaults struct {
	Harness Harness `yaml:"harness"`
	Model   string  `yaml:"model"`
}

// Notify configures notifications (v0.3).
type Notify struct {
	Kind   string `yaml:"kind"`
	URLEnv string `yaml:"url_env"`
}

var (
	versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	envName        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	repoSegment    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

func (c *Config) validate(r report) {
	if c.Version != 1 {
		r.add("version", fmt.Sprintf("unsupported version %d (want 1)", c.Version))
	}
	if c.Project.Name == "" {
		r.add("project.name", "required")
	}
	c.validateTracker(r)
	if c.Scheduler.MaxParallel < 0 {
		r.add("scheduler.max_parallel", "must not be negative")
	}
	for name, globs := range c.Areas {
		if len(globs) == 0 {
			r.add(join("areas", name), "area needs at least one glob")
		}
	}
	c.validateCodeMap(r)
	validateGates(r, "gates", c.Gates)
	validateGates(r, "milestone_gates", c.MilestoneGates)
	c.validateBudgets(r)
	c.validateWorkspaceResources(r)
	for h, rt := range c.Runtimes {
		p := join("runtimes", string(h))
		if !h.known() {
			r.add(p, "unknown harness (want claude-code or opencode)")
		}
		if rt.MinVersion != "" && !versionPattern.MatchString(rt.MinVersion) {
			r.add(join(p, "min_version"), fmt.Sprintf("%q is not a version like 2.1.186", rt.MinVersion))
		}
	}
	if d := c.Defaults; d.Harness != "" && d.Model != "" {
		if msg := checkPair(d.Harness, d.Model); msg != "" {
			r.add("defaults.model", msg)
		}
	} else if d.Harness != "" && !d.Harness.known() {
		r.add("defaults.harness", fmt.Sprintf("unknown harness %q (want claude-code or opencode)", d.Harness))
	}
	if n := c.Notify; n != nil && n.Kind != "log" && n.Kind != "ntfy" {
		r.add("notify.kind", fmt.Sprintf("unknown kind %q (want log or ntfy)", n.Kind))
	}
}

func (c *Config) validateCodeMap(r report) {
	if a := c.CodeMap.Adapter; !slices.Contains([]string{"", "none", "cartog", "lsp"}, a) {
		r.add("code_map.adapter", fmt.Sprintf("unknown adapter %q (want none, cartog or lsp)", a))
	}
	for i, g := range c.CodeMap.Exclude {
		if _, err := path.Match(g, ""); err != nil {
			r.add(idx("code_map.exclude", i), fmt.Sprintf("invalid glob %q: %v", g, err))
		}
	}
}

func (c *Config) validateTracker(r report) {
	t := c.Tracker
	switch t.Kind {
	case "github", "gitlab":
	case "":
		r.add("tracker.kind", "required")
	default:
		r.add("tracker.kind", fmt.Sprintf("unknown kind %q (want github or gitlab)", t.Kind))
	}
	if t.Repo == "" {
		r.add("tracker.repo", "required")
	} else if !validRepo(t.Kind, t.Repo) {
		want := "owner/name"
		if t.Kind == "gitlab" {
			want = "group/project"
		}
		r.add("tracker.repo", fmt.Sprintf("%q must look like %s", t.Repo, want))
	}
	if k := t.Auth.Kind; k != "" && k != "github_app" && k != "token" {
		r.add("tracker.auth.kind", fmt.Sprintf("unknown kind %q (want github_app or token)", k))
	}
	if env := t.Auth.TokenEnv; env != "" {
		switch {
		case t.Auth.Kind != "token":
			r.add("tracker.auth.token_env", "only valid when tracker.auth.kind is token")
		case !envName.MatchString(env):
			r.add("tracker.auth.token_env", fmt.Sprintf("%q is not an environment variable name", env))
		}
	}
	if t.PollInterval < 0 {
		r.add("tracker.poll_interval", "must not be negative")
	}
}

// validRepo accepts owner/name for GitHub and group[/subgroup]/project for GitLab.
func validRepo(kind, repo string) bool {
	parts := strings.Split(repo, "/")
	if kind != "gitlab" && len(parts) != 2 {
		return false
	}
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if !repoSegment.MatchString(p) {
			return false
		}
	}
	return true
}

var (
	retentionDays = regexp.MustCompile(`^[0-9]+d$`)
	retentionSize = regexp.MustCompile(`^[0-9]+(MB|GB|TB)$`)
)

// validateWorkspaceResources checks secret_paths, worktree, caches and retention.
func (c *Config) validateWorkspaceResources(r report) {
	for i, g := range c.SecretPaths {
		if _, err := path.Match(g, ""); err != nil {
			r.add(idx("secret_paths", i), fmt.Sprintf("%q is not a valid glob", g))
		}
	}
	if c.Worktree.Timeout < 0 {
		r.add("worktree.timeout", "must not be negative")
	}
	for i, f := range c.Worktree.Include {
		if f == "" || path.IsAbs(f) || path.Clean(f) != f || strings.HasPrefix(f, "../") {
			r.add(idx("worktree.include", i), fmt.Sprintf("%q must be a clean path relative to the repository", f))
		}
	}
	for i, e := range c.Caches {
		if !slices.Contains(contracts.CacheEcosystems(), e) {
			r.add(idx("caches", i), fmt.Sprintf("unknown ecosystem %q (known: %s)", e, strings.Join(contracts.CacheEcosystems(), ", ")))
		}
	}
	ret := c.Retention
	if ret.Logs != "" && !retentionDays.MatchString(ret.Logs) {
		r.add("retention.logs", fmt.Sprintf("%q is not a duration in days like 90d", ret.Logs))
	}
	if ret.DiskWarn != "" && !retentionSize.MatchString(ret.DiskWarn) {
		r.add("retention.disk_warn", fmt.Sprintf("%q is not a size like 20GB", ret.DiskWarn))
	}
	if b := ret.DockerBuildCache; b != "" && b != "off" && !retentionSize.MatchString(b) {
		r.add("retention.docker_build_cache", fmt.Sprintf("%q is not a size like 10GB, or off", b))
	}
}

func (c *Config) validateBudgets(r report) {
	n, m := c.Budgets.Node, c.Budgets.Milestone
	if n.Attempts < 0 || n.Minutes < 0 || n.USD < 0 {
		r.add("budgets.node", "limits must not be negative")
	}
	if m.USD < 0 || m.AlertAt < 0 {
		r.add("budgets.milestone", "limits must not be negative")
	}
}

// validateGates checks a gate list: unique names, and exactly one of a command
// (`run`) or the tracker's checks (`source: tracker`).
func validateGates(r report, path string, gates []Gate) {
	seen := map[string]bool{}
	for i, g := range gates {
		p := idx(path, i)
		switch {
		case g.Name == "":
			r.add(join(p, "name"), "required")
		case seen[g.Name]:
			r.add(join(p, "name"), fmt.Sprintf("duplicate gate %q", g.Name))
		}
		seen[g.Name] = true
		hasRun := g.Run != ""
		switch {
		case hasRun && g.Source != "":
			r.add(p, "set either run or source, not both")
		case !hasRun && g.Source == "":
			r.add(p, "needs run (a command) or source: tracker")
		case g.Source != "" && g.Source != "tracker":
			r.add(join(p, "source"), fmt.Sprintf("unknown source %q (want tracker)", g.Source))
		case g.Source == "tracker" && len(g.RequiredChecks) == 0:
			r.add(join(p, "required_checks"), "required when source is tracker")
		case hasRun && len(g.RequiredChecks) > 0:
			r.add(join(p, "required_checks"), "only valid with source: tracker")
		}
		if g.Timeout < 0 {
			r.add(join(p, "timeout"), "must not be negative")
		}
	}
}
