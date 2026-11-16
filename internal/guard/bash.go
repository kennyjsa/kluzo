package guard

import (
	"path"
	"slices"
	"strings"
)

// readOnlyGit are the git subcommands that do not change the repository or the
// worktree. Any other subcommand is denied: committing, pushing, switching
// branches, staging and the like are done by Kluzo.
var readOnlyGit = []string{
	"status", "diff", "log", "show", "blame", "ls-files", "ls-tree", "rev-parse", "rev-list", "grep", "describe",
	"cat-file", "shortlog", "show-ref", "for-each-ref", "merge-base", "diff-tree", "diff-files", "diff-index",
	"name-rev", "whatchanged", "check-ignore", "count-objects", "version", "help",
}

// hosting are the CLIs of the code hosts; the agent never publishes.
var hosting = map[string]bool{"gh": true, "glab": true, "hub": true}

// gitGlobalWithValue are the global options of git that take a value.
var gitGlobalWithValue = []string{"-C", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--exec-path"}

// writers change the files they get as arguments.
var writers = []string{"rm", "mv", "cp", "tee", "truncate", "chmod", "chown", "ln", "install", "patch", "touch", "rsync", "unlink", "shred", "mkdir", "rmdir"}

// ruleBash denies the shell commands the policy forbids.
func ruleBash(p Policy, c Call, in toolInput) *Decision {
	if c.Tool != "Bash" {
		return nil
	}
	dir := "" // where the commands run, relative to the worktree, as far as `cd` says
	for _, cmd := range commands(in.Command, 0) {
		if len(cmd.words) > 0 && cmd.words[0] == "cd" {
			dir = p.cd(dir, cmd.words[1:])
			continue
		}
		if d := p.checkCommand(dir, cmd); d != nil {
			return d
		}
	}
	return nil
}

// cd follows a directory change inside the worktree. A directory it cannot
// know (a variable, "-", "~") goes back to the root.
func (p Policy) cd(dir string, args []string) string {
	if len(args) != 1 || strings.ContainsAny(args[0], "$~") || args[0] == "-" {
		return ""
	}
	target := args[0]
	if !path.IsAbs(target) {
		target = path.Join(dir, target)
	}
	if r, inside := p.rel(target); inside && r != "." {
		return r
	}
	return ""
}

func (p Policy) checkCommand(dir string, cmd command) *Decision {
	for _, target := range cmd.redirects {
		if d := p.writeTarget("redirect to", dir, target); d != nil {
			return d
		}
	}
	if len(cmd.words) == 0 {
		return nil
	}
	prog, args := path.Base(cmd.words[0]), cmd.words[1:]
	switch {
	case hosting[prog]:
		return deny(RuleHostingCLI, "%s is Kluzo's: the agent does not talk to the code host", prog)
	case prog == "git":
		return gitDecision(args)
	case slices.Contains(writers, prog) || inPlaceEditor(prog, args) || prog == "dd":
		for _, a := range args {
			a = strings.TrimPrefix(a, "of=")
			if strings.HasPrefix(a, "-") && !strings.Contains(a, "/") && !strings.Contains(a, ".") {
				continue
			}
			if d := p.writeTarget(prog+" on", dir, a); d != nil {
				return d
			}
		}
	}
	return nil
}

// inPlaceEditor reports sed -i and perl -i, which rewrite their file arguments.
func inPlaceEditor(prog string, args []string) bool {
	if prog != "sed" && prog != "perl" {
		return false
	}
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "i")) {
			return true
		}
	}
	return false
}

// writeTarget denies a write whose target is a protected path or outside the worktree.
func (p Policy) writeTarget(verb, dir, target string) *Decision {
	if target == "" || strings.HasPrefix(target, "/dev/") {
		return nil
	}
	if !path.IsAbs(target) {
		target = path.Join(dir, target)
	}
	r, inside := p.rel(target)
	switch {
	case p.protected(r):
		return deny(RuleProtectedPath, "%s %s: it is a protected path; record the need in gaps instead", verb, r)
	case !inside && path.IsAbs(target) && !strings.HasPrefix(target, "/tmp/"):
		return deny(RuleOutsideTree, "%s %s: it is outside the worktree", verb, target)
	}
	return nil
}

// gitDecision allows only the read-only subcommands, without options that make
// them write or inject configuration.
func gitDecision(args []string) *Decision {
	sub, rest := "", []string(nil)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "--config-env" || strings.HasPrefix(a, "--config-env=") || len(a) > 2 && a[0] == '-' && a[1] == 'c':
			return deny(RuleGitWrite, "git -c injects configuration; Kluzo runs git")
		case slices.Contains(gitGlobalWithValue, a):
			i++
		case strings.HasPrefix(a, "-"):
			// a flag such as --no-pager, -p, --bare
		default:
			sub, rest = a, args[i+1:]
			i = len(args)
		}
	}
	switch {
	case sub == "":
		return nil // git alone, or only options such as --version
	case sub == "branch" && onlyListing(rest):
		return nil
	case !slices.Contains(readOnlyGit, sub):
		return deny(RuleGitWrite, "git %s changes the repository: commit, push, branch and merge are Kluzo's; use git status, diff, log or show to look", sub)
	}
	for _, a := range rest {
		if strings.HasPrefix(a, "--output") || a == "--ext-diff" || a == "--textconv" {
			return deny(RuleGitWrite, "git %s %s can write files or run helpers", sub, a)
		}
	}
	return nil
}

// onlyListing reports whether the arguments of git branch only list branches:
// only listing flags, and the commit that follows --contains or --merged. A
// bare name would create a branch.
func onlyListing(args []string) bool {
	flags := []string{"--show-current", "--list", "-l", "-a", "-r", "-v", "-vv", "--all", "--remotes", "--verbose"}
	withCommit := []string{"--merged", "--no-merged", "--contains", "--no-contains"}
	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(withCommit, args[i]):
			i++
		case !slices.Contains(flags, args[i]):
			return false
		}
	}
	return true
}
