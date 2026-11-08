package contracttest

import (
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// RunFiles runs the ports.Files contract against the implementation make
// returns for a worktree holding the given files (relative path -> content).
func RunFiles(t *testing.T, make func(t *testing.T, files map[string]string) ports.Files) {
	t.Helper()
	f := make(t, map[string]string{
		"a.go":            "package a\n\nfunc A() {}\n",
		"no_newline.txt":  "one\ntwo",
		"empty.txt":       "",
		"dir/nested/b.go": "package b\n",
	})
	tests := []struct {
		path string
		n    int
		ok   bool
	}{
		{"a.go", 3, true},
		{"no_newline.txt", 2, true},
		{"empty.txt", 0, true},
		{"dir/nested/b.go", 1, true},
		{"missing.go", 0, false},
		{"dir", 0, false},
		{"dir/nested", 0, false},
		{"../a.go", 0, false},
		{"dir/../../a.go", 0, false},
		{"/etc/passwd", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			n, ok, err := f.Lines(tt.path)
			if err != nil {
				t.Fatalf("Lines(%q): %v", tt.path, err)
			}
			if n != tt.n || ok != tt.ok {
				t.Errorf("Lines(%q) = %d, %v; want %d, %v", tt.path, n, ok, tt.n, tt.ok)
			}
		})
	}
}
