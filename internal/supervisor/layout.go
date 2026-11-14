package supervisor

import "path/filepath"

// Layout is the directory layout of a workspace, the one definition used by
// the supervisor and by the operations of app (docs/contracts/data-model.md,
// workspace files): one checkout per issue under worktrees/ (the node's) and
// gates/ (the clean checkout of its gates), and the HOMEs and build caches of
// the sessions and of the gates, apart (ADR 0020).
type Layout struct{ Dir string }

// Worktrees is the directory of the worktrees of the nodes.
func (l Layout) Worktrees() string { return filepath.Join(l.Dir, "worktrees") }

// Gates is the directory of the clean checkouts the gates run in.
func (l Layout) Gates() string { return filepath.Join(l.Dir, "gates") }

// Worktree is the worktree of the node of an issue.
func (l Layout) Worktree(issue string) string { return filepath.Join(l.Worktrees(), issue) }

// GatesCheckout is the clean checkout the gates of the node of an issue run in.
func (l Layout) GatesCheckout(issue string) string { return filepath.Join(l.Gates(), issue) }

// Leftovers are the HOMEs and build caches of the sessions and of the gates:
// what the workspace keeps besides its checkouts, its journal and its logs.
func (l Layout) Leftovers() []string {
	var out []string
	for _, p := range []place{sessionsPlace, gatesPlace} {
		out = append(out, l.home(p), l.builds(p))
	}
	return out
}

// place is where a checkout of the node runs: its HOME and build caches.
type place struct{ home, builds string }

// The sessions and the gates of a node use apart HOMEs and build caches: a
// session cannot leave in them what a gate will run (ADR 0020).
var (
	sessionsPlace = place{home: "home", builds: "cache"}
	gatesPlace    = place{home: "gates-home", builds: "gates-cache"}
)

func (l Layout) home(p place) string   { return filepath.Join(l.Dir, p.home) }
func (l Layout) builds(p place) string { return filepath.Join(l.Dir, p.builds) }
