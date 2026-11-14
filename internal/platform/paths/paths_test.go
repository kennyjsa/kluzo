package paths

import "testing"

func TestWithin(t *testing.T) {
	for _, tt := range []struct {
		dir, name, rel string
		ok             bool
	}{
		{"/ws/wt", "/ws/wt", ".", true},
		{"/ws/wt", "/ws/wt/a/b.go", "a/b.go", true},
		{"/ws/wt", "a/../b.go", "b.go", true},
		{"/ws/wt", "/ws/wt/../x", "", false},
		{"/ws/wt", "../x", "", false},
		{"/ws/wt", "/ws/wtx/a", "", false},
		{"/ws/wt", "/ws", "", false},
		{"/ws/wt", "..x/a", "..x/a", true},
	} {
		if rel, ok := Within(tt.dir, tt.name); rel != tt.rel || ok != tt.ok {
			t.Errorf("Within(%q, %q) = %q, %v; want %q, %v", tt.dir, tt.name, rel, ok, tt.rel, tt.ok)
		}
	}
}

func TestWithinAbs(t *testing.T) {
	for _, tt := range []struct {
		dir, name string
		want      bool
	}{
		{"/ws", "/ws/worktrees/1", true},
		{"/ws", "/ws", true},
		{"/ws", "/other", false},
		{"/ws", "worktrees/1", false},
		{"/ws", "", false},
	} {
		if got := WithinAbs(tt.dir, tt.name); got != tt.want {
			t.Errorf("WithinAbs(%q, %q) = %v, want %v", tt.dir, tt.name, got, tt.want)
		}
	}
}
