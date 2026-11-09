// Package hostcred finds the credentials of the host user that an agent
// session could use to publish on its own: an authenticated gh, a git
// credential helper or stored credentials, and private SSH keys (ADR 0020,
// item 6). It only reads files; the git configuration is read by gitops and
// passed in.
package hostcred

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Spec is where the host user keeps its configuration.
type Spec struct {
	Home        string
	XDGConfig   string   // XDG_CONFIG_HOME; default Home/.config
	GHConfigDir string   // GH_CONFIG_DIR; default XDGConfig/gh
	Helpers     []string // git credential helpers of the host (gitops.HostCredentialHelpers)
}

// FromEnv reads the Spec from an environment (KEY=VALUE).
func FromEnv(env []string, helpers []string) Spec {
	s := Spec{Helpers: helpers}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "HOME":
			s.Home = v
		case "XDG_CONFIG_HOME":
			s.XDGConfig = v
		case "GH_CONFIG_DIR":
			s.GHConfigDir = v
		}
	}
	return s
}

// Check scans the host whose environment is env; helpers reads the git
// credential helpers of a HOME and XDG_CONFIG_HOME
// (gitops.HostCredentialHelpers).
func Check(ctx context.Context, env []string, helpers func(ctx context.Context, home, xdgConfig string) ([]string, error)) ([]string, error) {
	s := FromEnv(env, nil)
	if s.Home == "" {
		return nil, errors.New("HOME is not set: the credentials of the host cannot be checked")
	}
	h, err := helpers(ctx, s.Home, s.XDGConfig)
	if err != nil {
		return nil, err
	}
	s.Helpers = h
	return Scan(s), nil
}

// Scan returns one line per credential found, empty when the host is clean.
func Scan(s Spec) []string {
	if s.XDGConfig == "" && s.Home != "" {
		s.XDGConfig = filepath.Join(s.Home, ".config")
	}
	if s.GHConfigDir == "" && s.XDGConfig != "" {
		s.GHConfigDir = filepath.Join(s.XDGConfig, "gh")
	}
	var out []string
	if s.GHConfigDir != "" && nonEmpty(filepath.Join(s.GHConfigDir, "hosts.yml")) {
		out = append(out, "gh is authenticated ("+filepath.Join(s.GHConfigDir, "hosts.yml")+")")
	}
	for _, h := range s.Helpers {
		out = append(out, "git has the credential helper "+h)
	}
	if s.Home != "" {
		for _, f := range []string{filepath.Join(s.Home, ".git-credentials"), filepath.Join(s.XDGConfig, "git", "credentials")} {
			if nonEmpty(f) {
				out = append(out, "git credentials are stored in "+f)
			}
		}
		out = append(out, sshKeys(filepath.Join(s.Home, ".ssh"))...)
	}
	return out
}

// nonEmpty reports whether a regular file exists with content.
func nonEmpty(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// sshKeys lists the readable private keys in dir: the files that start like
// one, whatever their name.
func sshKeys(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if !e.Type().IsRegular() || strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue // not readable by this user: not visible to the agents either
		}
		head := make([]byte, 64)
		n, _ := io.ReadFull(f, head)
		_ = f.Close()
		if bytes.Contains(head[:n], []byte("PRIVATE KEY")) {
			out = append(out, "a private SSH key is readable at "+path)
		}
	}
	return out
}
