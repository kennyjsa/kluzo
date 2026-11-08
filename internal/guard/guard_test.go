package guard

import (
	"encoding/json"
	"strings"
	"testing"
)

var policy = Policy{
	Workdir:        "/work/tree",
	ProtectedPaths: []string{".kluzo/**", "AGENTS.md", "CLAUDE.md", ".claude/**", ".mcp.json", ".golangci.yml", ".github/workflows/**", "Makefile"},
	SecretPaths:    []string{".env", ".env.*"},
}

func call(tool string, in map[string]string) Call {
	b, _ := json.Marshal(in)
	return Call{Tool: tool, Input: b}
}

func bash(cmd string) Call { return call("Bash", map[string]string{"command": cmd}) }

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{".kluzo/**", ".kluzo/kluzo.yaml", true},
		{".kluzo/**", ".kluzo/roles/a/b.md", true},
		{".kluzo/**", ".kluzo", true},
		{".kluzo/**", "src/.kluzo/x", false},
		{"**/.env", ".env", true},
		{"**/.env", "web/app/.env", true},
		{"internal/*/x.go", "internal/a/x.go", true},
		{"internal/*/x.go", "internal/a/b/x.go", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "tools/Makefile", true}, // no slash: any depth
		{".env.*", "apps/web/.env.local", true},
		{".env.*", "env.local", false},
		{"web/**/dist", "web/a/b/dist", true},
		{"./AGENTS.md", "AGENTS.md", true},
	}
	for _, tt := range tests {
		if got := Match(tt.pattern, tt.name); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestBashDenials(t *testing.T) {
	denied := []struct{ cmd, rule string }{
		// git that writes, in every disguise
		{"git commit -m x", RuleGitWrite},
		{"git push origin HEAD", RuleGitWrite},
		{"git -C /other checkout main", RuleGitWrite},
		{"git add . && git commit -m x", RuleGitWrite},
		{"cd sub; git reset --hard", RuleGitWrite},
		{"echo hi | git apply", RuleGitWrite},
		{"/usr/bin/git merge main", RuleGitWrite},
		{"GIT_DIR=x git rebase -i", RuleGitWrite},
		{"sudo -u root git push", RuleGitWrite},
		{"env FOO=1 git tag v1", RuleGitWrite},
		{"timeout 30 git fetch", RuleGitWrite},
		{"xargs -n 1 git rm", RuleGitWrite},
		{"bash -c 'git push'", RuleGitWrite},
		{`sh -c "cd x && git commit -am y"`, RuleGitWrite},
		{`eval "git push"`, RuleGitWrite},
		{"echo $(git commit -m x)", RuleGitWrite},
		{"echo `git add -A`", RuleGitWrite},
		{`echo "done: $(git push)"`, RuleGitWrite},
		{"(git push)", RuleGitWrite},
		{"git -c core.hooksPath=/tmp/h log", RuleGitWrite},
		{"git -ccore.pager=x log", RuleGitWrite},
		{"git branch newbranch", RuleGitWrite},
		{"git branch -D main", RuleGitWrite},
		{"git stash", RuleGitWrite},
		{"git diff --output=/etc/x", RuleGitWrite},
		{"git config user.name x", RuleGitWrite},
		// the code host
		{"gh pr create", RuleHostingCLI},
		{"gh auth status", RuleHostingCLI},
		{"ls && gh issue list", RuleHostingCLI},
		{"nohup gh pr merge &", RuleHostingCLI},
		{"glab mr create", RuleHostingCLI},
		// protected paths
		{"echo x > .kluzo/kluzo.yaml", RuleProtectedPath},
		{"echo x >> AGENTS.md", RuleProtectedPath},
		{"cat a 2>/dev/null > Makefile", RuleProtectedPath},
		{"tee CLAUDE.md < in", RuleProtectedPath},
		{"sed -i 's/a/b/' .golangci.yml", RuleProtectedPath},
		{"sed --in-place=.bak s/a/b/ .claude/settings.json", RuleProtectedPath},
		{"rm -rf .github/workflows", RuleProtectedPath},
		{"rm .github/workflows/ci.yml", RuleProtectedPath},
		{"mv notes.md .mcp.json", RuleProtectedPath},
		{"cp evil .claude/hooks/x.sh", RuleProtectedPath},
		{"dd if=/dev/zero of=Makefile", RuleProtectedPath},
		{"cd .kluzo && touch roles/x.md", RuleProtectedPath}, // cd is not followed: the path below is relative to the root
		{"perl -pi -e 's/a/b/' Makefile", RuleProtectedPath},
		{"rm /work/tree/.kluzo/kluzo.yaml", RuleProtectedPath},
		{"truncate -s 0 ./Makefile", RuleProtectedPath},
		// outside the worktree
		{"echo x > /etc/passwd", RuleOutsideTree},
		{"rm -rf /home/user/.ssh", RuleOutsideTree},
		{"cp secrets /root/leak", RuleOutsideTree},
	}
	for _, tt := range denied {
		t.Run(tt.cmd, func(t *testing.T) {
			d := policy.Check(bash(tt.cmd))
			if d.Allowed || d.Rule != tt.rule || d.Reason == "" {
				t.Errorf("%q: %+v, want denied by %s", tt.cmd, d, tt.rule)
			}
		})
	}
}

func TestBashAllowed(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...", "make test", "ls -la", "git status", "git diff HEAD~1", "git log --oneline -5", "git show HEAD:main.go",
		"git -C sub status", "git --no-pager diff", "git branch --show-current", "git branch --list", "git branch -a",
		"git branch --contains abc123", "git ls-files | head", "git blame main.go", "git grep -n foo",
		"echo 'git push is forbidden'", `echo "run gh later"`, "cat README.md > /tmp/out.txt", "ls > /dev/null 2>&1",
		"go build ./... 2>&1 | tail", "sed -n '1,5p' Makefile", "cat .kluzo/kluzo.yaml", "grep -r TODO .", "sed -i s/a/b/ internal/x.go",
		"rm -rf build", "mkdir -p internal/new", "cp a.go internal/b.go", "touch internal/new.go", "cd internal && go vet ./...",
		"make lint # git push", "FOO=1 go test ./...", "git version", "echo $(date)", "npm test -- --watch=false",
	} {
		if d := policy.Check(bash(cmd)); !d.Allowed {
			t.Errorf("%q denied: %+v", cmd, d)
		}
	}
}

func TestFileTools(t *testing.T) {
	tests := []struct {
		name string
		call Call
		rule string // empty: allowed
	}{
		{"edit source", call("Edit", map[string]string{"file_path": "/work/tree/internal/a.go"}), ""},
		{"edit relative source", call("Write", map[string]string{"file_path": "internal/a.go"}), ""},
		{"write protected", call("Write", map[string]string{"file_path": ".kluzo/roles/implementer.md"}), RuleProtectedPath},
		{"edit protected, absolute", call("Edit", map[string]string{"file_path": "/work/tree/AGENTS.md"}), RuleProtectedPath},
		{"edit protected through ..", call("Edit", map[string]string{"file_path": "internal/../Makefile"}), RuleProtectedPath},
		{"notebook protected", call("NotebookEdit", map[string]string{"notebook_path": ".claude/x.ipynb"}), RuleProtectedPath},
		{"write outside", call("Write", map[string]string{"file_path": "/etc/cron.d/x"}), RuleOutsideTree},
		{"write through .. out of the tree", call("Write", map[string]string{"file_path": "../other/x.go"}), RuleOutsideTree},
		{"read source", call("Read", map[string]string{"file_path": "internal/a.go"}), ""},
		{"read protected is fine", call("Read", map[string]string{"file_path": ".kluzo/kluzo.yaml"}), ""},
		{"read secret", call("Read", map[string]string{"file_path": ".env"}), RuleSecretPath},
		{"read nested secret", call("Read", map[string]string{"file_path": "/work/tree/apps/web/.env.local"}), RuleSecretPath},
		{"glob for secrets", call("Glob", map[string]string{"pattern": "**/.env"}), RuleSecretPath},
		{"glob source", call("Glob", map[string]string{"pattern": "internal/**/*.go"}), ""},
		{"grep in a secret", call("Grep", map[string]string{"pattern": "KEY", "path": ".env"}), RuleSecretPath},
		{"grep with a secret glob", call("Grep", map[string]string{"pattern": "KEY", "glob": ".env.*"}), RuleSecretPath},
		{"grep source", call("Grep", map[string]string{"pattern": "TODO", "path": "internal"}), ""},
		{"an MCP tool", call("mcp__kluzo__kluzo_result", map[string]string{}), ""},
		{"unknown tool", call("WebFetch", map[string]string{"url": "http://x"}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := policy.Check(tt.call)
			if tt.rule == "" && !d.Allowed || tt.rule != "" && (d.Allowed || d.Rule != tt.rule) {
				t.Errorf("%+v, want rule %q", d, tt.rule)
			}
		})
	}
}

func TestCheckChanges(t *testing.T) {
	got := policy.CheckChanges([]Change{
		{Path: "internal/a.go"},
		{Path: ".kluzo/kluzo.yaml"},
		{Path: "Makefile", Deleted: true},
		{Path: ".github/workflows/ci.yml"},
		{Path: "docs/x.md"},
	})
	var paths []string
	for _, v := range got {
		if v.Rule != RuleProtectedPath || v.Reason == "" {
			t.Errorf("violation = %+v", v)
		}
		paths = append(paths, v.Path)
	}
	if strings.Join(paths, ",") != ".kluzo/kluzo.yaml,Makefile,.github/workflows/ci.yml" {
		t.Errorf("violations on %v", paths)
	}
}

// A script that does not parse never panics the hook.
func TestOddInputDoesNotPanic(*testing.T) {
	for _, cmd := range []string{"", "'", `"`, "`", "$(", "$((", "echo $(", "a && ", "\\", "<<<", ">", "x=", "bash -c", "eval", "sudo", "echo \"$(\"", strings.Repeat("$(", 50)} {
		_ = policy.Check(bash(cmd))
	}
	_ = policy.Check(Call{Tool: "Bash", Input: []byte("not json")})
	_ = policy.Check(Call{Tool: "Edit"})
}
