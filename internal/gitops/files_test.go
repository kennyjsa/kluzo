package gitops

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/contracttest"
)

func TestFilesContract(t *testing.T) {
	contracttest.RunFiles(t, func(t *testing.T, files map[string]string) ports.Files {
		dir := t.TempDir()
		for name, content := range files {
			write(t, filepath.Join(dir, filepath.FromSlash(name)), content, 0o644)
		}
		return NewFiles(dir)
	})
}

func TestFilesSymlinks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package a\n", 0o644)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	write(t, outside, "s\n", 0o644)
	for link, target := range map[string]string{"in.go": "a.go", "out.txt": outside, "rel.txt": "../" + filepath.Base(outside)} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		path string
		n    int
		ok   bool
	}{
		{"in.go", 1, true},
		{"out.txt", 0, false},
		{"rel.txt", 0, false},
	}
	f := NewFiles(dir)
	for _, tt := range tests {
		n, ok, err := f.Lines(tt.path)
		if err != nil || n != tt.n || ok != tt.ok {
			t.Errorf("Lines(%q) = %d, %v, %v; want %d, %v, nil", tt.path, n, ok, err, tt.n, tt.ok)
		}
	}
}

// What an agent can leave in the worktree must give a problem to fix, never
// hang the check or turn into an infrastructure error.
func TestFilesSpecialFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file, so locked.go would be citable; run the tests as a regular user (Q68)")
	}
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.go"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "locked.go"), "x\n", 0o000)
	f := NewFiles(root)
	for _, p := range []string{"pipe.go", "locked.go"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if n, ok, err := f.Lines(p); ok || err != nil {
				t.Errorf("Lines(%q) = %d, %v, %v; want not citable", p, n, ok, err)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Lines(%q) blocked", p)
		}
	}
}
