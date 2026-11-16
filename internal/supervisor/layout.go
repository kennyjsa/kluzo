package supervisor

import "path/filepath"

// Layout is the directory layout of a workspace, the one definition used by
// the supervisor and by the operations of app (docs/contracts/data-model.md,
// workspace files): its journal, one checkout per issue under worktrees/ (the
// node's) and gates/ (the clean checkout of its gates), the HOMEs and build
// caches of the sessions and of the gates, apart (ADR 0020), the files of the
// harness per session and the logs. No path of a workspace is built outside
// it.
type Layout struct{ Dir string }

// DB is the journal and projections of the workspace.
func (l Layout) DB() string { return filepath.Join(l.Dir, "state.db") }

// Worktrees is the directory of the worktrees of the nodes.
func (l Layout) Worktrees() string { return filepath.Join(l.Dir, "worktrees") }

// Gates is the directory of the clean checkouts the gates run in.
func (l Layout) Gates() string { return filepath.Join(l.Dir, "gates") }

// Worktree is the worktree of the node of an issue.
func (l Layout) Worktree(issue string) string { return filepath.Join(l.Worktrees(), issue) }

// GatesCheckout is the clean checkout the gates of the node of an issue run in.
func (l Layout) GatesCheckout(issue string) string { return filepath.Join(l.Gates(), issue) }

// SessionsHome is the synthetic HOME of the sessions (ADR 0024).
func (l Layout) SessionsHome() string { return l.home(sessionsPlace) }

// SessionFiles is where the harness keeps the 0600 files of each session,
// outside every worktree.
func (l Layout) SessionFiles() string { return filepath.Join(l.Dir, "sessions") }

// Logs is the directory of the logs of the workspace.
func (l Layout) Logs() string { return filepath.Join(l.Dir, "logs") }

// SessionLogs holds the raw stream and the prompt of each session.
func (l Layout) SessionLogs() string { return filepath.Join(l.Logs(), "sessions") }

// GateLogs holds the log of each gate run.
func (l Layout) GateLogs() string { return filepath.Join(l.Logs(), "gates") }

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
