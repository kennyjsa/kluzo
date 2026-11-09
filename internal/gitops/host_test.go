package gitops

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHostCredentialHelpers(t *testing.T) {
	g := New(Config{ParentEnv: os.Environ()})
	home := t.TempDir()
	got, err := g.HostCredentialHelpers(context.Background(), home, "")
	if err != nil {
		t.Fatal(err)
	}
	system := got // whatever the machine sets system-wide
	write(t, filepath.Join(home, ".gitconfig"), "[credential]\n\thelper = store\n", 0o644)
	got, err = g.HostCredentialHelpers(context.Background(), home, "")
	if err != nil || !slices.Contains(got, "store") || len(got) != len(system)+1 {
		t.Errorf("helpers = %v, %v (system %v)", got, err, system)
	}
	// The XDG file of the user counts too.
	home = t.TempDir()
	write(t, filepath.Join(home, ".config", "git", "config"), "[credential]\n\thelper = osxkeychain\n", 0o644)
	if got, err := g.HostCredentialHelpers(context.Background(), home, ""); err != nil || !slices.Contains(got, "osxkeychain") {
		t.Errorf("XDG helpers = %v, %v", got, err)
	}
	xdg := t.TempDir()
	write(t, filepath.Join(xdg, "git", "config"), "[credential]\n\thelper = cache\n", 0o644)
	if got, err := g.HostCredentialHelpers(context.Background(), t.TempDir(), xdg); err != nil || !slices.Contains(got, "cache") {
		t.Errorf("XDG_CONFIG_HOME helpers = %v, %v", got, err)
	}
}
