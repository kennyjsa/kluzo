package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"--version"}, wantCode: 0, wantStdout: "kluzo dev"},
		{name: "version subcommand", args: []string{"version"}, wantCode: 0, wantStdout: "kluzo dev"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "no args", args: nil, wantCode: 1, wantStderr: "Usage:"},
		{name: "unknown", args: []string{"nope"}, wantCode: 1, wantStderr: `unknown command "nope"`},
		{name: "doctor without configuration", args: []string{"doctor", "--dir", "/does/not/exist"}, wantCode: 2},
		{name: "every command of F29", args: []string{"help"}, wantCode: 0, wantStdout: "approve"},
		{name: "plan without its milestone", args: []string{"plan"}, wantCode: 1, wantStderr: "plan takes 1 argument"},
		{name: "bad flag", args: []string{"status", "--nope", "v1"}, wantCode: 1, wantStderr: "unknown flag"},
		{name: "clean asked to remove and not to", args: []string{"clean", "--yes", "--dry-run"}, wantCode: 1, wantStderr: "contradict"},
		{name: "proposal without a subcommand", args: []string{"proposal"}, wantCode: 1, wantStderr: "Usage:"},
		{name: "proposal approve without its id", args: []string{"proposal", "approve"}, wantCode: 1, wantStderr: "approve takes 1 argument"},
		{name: "node without a subcommand", args: []string{"node", "nope"}, wantCode: 1, wantStderr: `unknown command "nope"`},
		{name: "node retry without configuration", args: []string{"node", "retry", "7", "--dir", "/does/not/exist"}, wantCode: 2},
		{name: "every command of 10b", args: []string{"help"}, wantCode: 0, wantStdout: "proposal"},
		{name: "status without workspace", args: []string{"status", "v1", "--dir", "testdata/none"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

// runIn runs the CLI and returns its exit code and output.
func runIn(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestInitAndRolesCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kluzo")

	code, out, _ := runIn("init", "--dir", dir)
	if code != 0 || !strings.Contains(out, "roles/implementer.md") || !strings.Contains(out, "TODO") {
		t.Fatalf("init: code %d, stdout %q", code, out)
	}
	code, out, errOut := runIn("roles", "check", "--dir", dir)
	if code != 0 || !strings.Contains(out, "10 roles valid") {
		t.Fatalf("roles check on the default config: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	// init refuses to overwrite.
	if code, _, errOut := runIn("init", "--dir", dir); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Errorf("second init: code %d, stderr %q", code, errOut)
	}

	// An invalid role yields exit code 2 and one line per problem with file and line.
	reviewer := filepath.Join(dir, "roles", "reviewer.md")
	data, err := os.ReadFile(reviewer)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), "tools: [read]", "tools: [read, grep]", 1)
	if err := os.WriteFile(reviewer, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runIn("roles", "check", "--dir", dir)
	if code != 2 || !strings.Contains(errOut, reviewer+":5: tools[1]") || !strings.Contains(errOut, "1 problem(s) found") {
		t.Errorf("roles check on a broken role: code %d, stderr %q", code, errOut)
	}
}

func TestRolesCheckUsage(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"no subcommand", []string{"roles"}, 1, "Available Commands:\n  check"},
		{"unknown subcommand", []string{"roles", "list"}, 1, `unknown command "list"`},
		{"bad flag", []string{"roles", "check", "--nope"}, 1, "unknown flag: --nope"},
		{"missing config", []string{"roles", "check", "--dir", filepath.Join(t.TempDir(), "absent")}, 2, "kluzo init"},
		{"init bad flag", []string{"init", "--nope"}, 1, "unknown flag: --nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runIn(tt.args...)
			if code != tt.wantCode || !strings.Contains(errOut, tt.wantErr) {
				t.Errorf("code %d, stderr %q; want code %d containing %q", code, errOut, tt.wantCode, tt.wantErr)
			}
		})
	}
}
