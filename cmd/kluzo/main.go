// Command kluzo orchestrates a whole milestone with coding agents.
package main

import (
	"fmt"
	"io"
	"os"
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
	default:
		printf(stderr, "kluzo: unknown command %q\n\n", args[0])
		usage(stderr)
		return 1
	}
}

// printf writes to a terminal stream. A failed write to stdout/stderr cannot
// be reported anywhere useful, so the error is deliberately discarded.
func printf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func usage(w io.Writer) {
	printf(w, "%s", `Usage: kluzo <command>

Commands:
  version   print the kluzo version
  help      show this help
`)
}
