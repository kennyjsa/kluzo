package config

import (
	"math"
	"strings"
)

// Defaults of the branch naming scheme (docs/contracts/config.md).
const (
	DefaultMainBranch      = "main"
	DefaultMilestonePrefix = "milestone/"
)

// MainBranch is branches.main, or main.
func (b Branches) MainBranch() string {
	if b.Main != "" {
		return b.Main
	}
	return DefaultMainBranch
}

// MilestoneBranch is the branch of the milestone titled title:
// branches.milestone_prefix (milestone/ by default) and the title.
func (b Branches) MilestoneBranch(title string) string {
	prefix := b.MilestonePrefix
	if prefix == "" {
		prefix = DefaultMilestonePrefix
	}
	return prefix + title
}

// USDMicros converts dollars, as the configuration and the plan write them,
// to the micro-dollars of the journal, rounded to the nearest.
func USDMicros(usd float64) int64 { return int64(math.Round(usd * 1e6)) }

// USD converts micro-dollars back to dollars, for people.
func USD(micros int64) float64 { return float64(micros) / 1e6 }

// MilestoneTitle is the title of the milestone whose branch is branch, and
// whether branch is a milestone branch at all.
func (b Branches) MilestoneTitle(branch string) (string, bool) {
	prefix := b.MilestoneBranch("")
	title, ok := strings.CutPrefix(branch, prefix)
	return title, ok && title != ""
}
