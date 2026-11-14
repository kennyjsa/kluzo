package hostcred

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(home string) Spec
		want  []string // substrings, in order
	}{
		{"clean host", func(h string) Spec {
			write(t, filepath.Join(h, ".ssh", "known_hosts"), "github.com ssh-ed25519 AAAA")
			write(t, filepath.Join(h, ".ssh", "id_ed25519.pub"), "ssh-ed25519 AAAA")
			write(t, filepath.Join(h, ".config", "gh", "config.yml"), "git_protocol: https")
			return Spec{Home: h}
		}, nil},
		{"gh authenticated", func(h string) Spec {
			write(t, filepath.Join(h, ".config", "gh", "hosts.yml"), "github.com:\n  user: me\n")
			return Spec{Home: h}
		}, []string{"gh is authenticated"}},
		{"gh in GH_CONFIG_DIR", func(h string) Spec {
			write(t, filepath.Join(h, "ghc", "hosts.yml"), "github.com:\n")
			return FromEnv([]string{"HOME=" + h, "GH_CONFIG_DIR=" + filepath.Join(h, "ghc")}, nil)
		}, []string{"gh is authenticated"}},
		{"credential helper", func(h string) Spec { return Spec{Home: h, Helpers: []string{"osxkeychain"}} }, []string{"credential helper osxkeychain"}},
		{"stored git credentials", func(h string) Spec {
			write(t, filepath.Join(h, ".git-credentials"), "https://me:tok@github.com")
			return Spec{Home: h}
		}, []string{"stored in"}},
		{"private key with any name", func(h string) Spec {
			write(t, filepath.Join(h, ".ssh", "deploy"), "-----BEGIN OPENSSH PRIVATE KEY-----\nabc")
			return Spec{Home: h}
		}, []string{"private SSH key"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Scan(tt.setup(t.TempDir()))
			if len(got) != len(tt.want) {
				t.Fatalf("Scan = %q, want %q", got, tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("Scan[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
	if got := Scan(Spec{}); got != nil {
		t.Errorf("no home: %q", got)
	}
}
