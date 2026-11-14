package supervisor_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/kennyjsa/kluzo/internal/supervisor"
)

// Every path of a workspace comes from Layout, as data-model.md draws it.
func TestLayout(t *testing.T) {
	l := supervisor.Layout{Dir: "/ws"}
	for got, want := range map[string]string{
		l.DB():               "/ws/state.db",
		l.Worktree("7"):      "/ws/worktrees/7",
		l.GatesCheckout("7"): "/ws/gates/7",
		l.SessionsHome():     "/ws/home",
		l.SessionFiles():     "/ws/sessions",
		l.Logs():             "/ws/logs",
		l.SessionLogs():      "/ws/logs/sessions",
		l.GateLogs():         "/ws/logs/gates",
	} {
		if got != filepath.FromSlash(want) {
			t.Errorf("%s, want %s", got, want)
		}
	}
	want := []string{"/ws/home", "/ws/cache", "/ws/gates-home", "/ws/gates-cache"}
	if got := l.Leftovers(); !slices.Equal(got, want) {
		t.Errorf("leftovers %v, want %v", got, want)
	}
}
