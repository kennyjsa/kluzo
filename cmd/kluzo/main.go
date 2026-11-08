// Command kluzo orchestrates a whole milestone with coding agents.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kennyjsa/kluzo/internal/app"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit code.
// Exit codes follow docs/product/features/F29-cli.md:
// 0 ok, 1 usage error, 2 invalid configuration, 3 integration failure.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 1
	}
	switch args[0] {
	case "version", "--version", "-v":
		printf(stdout, "kluzo %s\n", version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "roles":
		return runRoles(args[1:], stdout, stderr)
	default:
		printf(stderr, "kluzo: unknown command %q\n\n", args[0])
		usage(stderr)
		return 1
	}
}

const (
	exitOK            = 0
	exitUsage         = 1
	exitInvalidConfig = 2
)

// newFlags returns a flag set for a command with the shared --dir flag.
func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("kluzo "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", app.DefaultConfigDir, "configuration directory")
	return fs, dir
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs, dir := newFlags("init", stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	out, err := app.InitConfig{}.Execute(context.Background(), app.InitConfigInput{Dir: *dir})
	if err != nil {
		printf(stderr, "kluzo init: %v\n", err)
		return exitUsage
	}
	for _, f := range out.Files {
		printf(stdout, "created %s/%s\n", *dir, f)
	}
	printf(stdout, "\nEdit the items marked TODO, then run `kluzo roles check`.\n")
	return exitOK
}

func runRoles(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		printf(stderr, "Usage: kluzo roles check [--dir %s]\n", app.DefaultConfigDir)
		return exitUsage
	}
	fs, dir := newFlags("roles check", stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	out, err := app.CheckRoles{}.Execute(context.Background(), app.CheckRolesInput{Dir: *dir})
	if err != nil {
		printf(stderr, "kluzo roles check: %v\n", err)
		return exitUsage
	}
	if len(out.Problems) > 0 {
		for _, p := range out.Problems {
			printf(stderr, "%s\n", p)
		}
		printf(stderr, "\n%d problem(s) found\n", len(out.Problems))
		return exitInvalidConfig
	}
	printf(stdout, "%d roles valid\n", len(out.Roles))
	return exitOK
}

// printf writes to a terminal stream. A failed write to stdout/stderr cannot
// be reported anywhere useful, so the error is deliberately discarded.
func printf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func usage(w io.Writer) {
	printf(w, "%s", `Usage: kluzo <command>

Commands:
  init         create the default .kluzo/ configuration
  roles check  validate the roles against the protocol of this binary
  version      print the kluzo version
  help         show this help
`)
}
