// Command kluzo orchestrates a whole milestone with coding agents.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kennyjsa/kluzo/internal/app"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes follow docs/product/features/F29-cli.md.
const (
	exitOK            = 0
	exitUsage         = 1
	exitInvalidConfig = 2
	exitIntegration   = 3
)

// exitError carries the exit code of a command that failed.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// run executes the CLI and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	var ee exitError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &ee):
		return ee.code
	}
	printf(stderr, "kluzo: %v\n", err)
	return code(err)
}

// code is the exit code of an error of a use case: invalid configuration or
// plan, a doctor that is not green or a failed integration, or a usage error
// (no such workspace, a move the workspace refuses).
func code(err error) int {
	switch {
	case errors.Is(err, app.ErrInvalid):
		return exitInvalidConfig
	case errors.Is(err, app.ErrDoctor):
		return exitIntegration
	case errors.Is(err, app.ErrNoWorkspace), errors.Is(err, app.ErrWorkspaceExists), errors.Is(err, app.ErrRefused), errors.Is(err, app.ErrNoAttempts), errors.Is(err, app.ErrNotOperator), isUsage(err):
		return exitUsage
	}
	return exitIntegration
}

// usageError is a mistake on the command line.
type usageError struct{ error }

func isUsage(err error) bool { return errors.As(err, new(usageError)) }

// newRoot builds the command tree. Each command parses its flags, calls one
// use case of internal/app and formats the result: no business logic here.
func newRoot(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "kluzo",
		Short:         "Kluzo runs a whole milestone with coding agents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE:          group(stderr),
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("kluzo {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	dir := root.PersistentFlags().String("dir", app.DefaultConfigDir, "configuration directory")
	host := func() app.Host {
		return app.Host{Log: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))}
	}
	args := func(n int) cobra.PositionalArgs {
		return func(cmd *cobra.Command, a []string) error {
			if len(a) != n {
				return usageError{fmt.Errorf("%s takes %d argument(s), got %d", cmd.Name(), n, len(a))}
			}
			return nil
		}
	}

	root.AddCommand(&cobra.Command{
		Use: "version", Short: "print the kluzo version", Args: args(0),
		Run: func(*cobra.Command, []string) { printf(stdout, "kluzo %s\n", version) },
	})

	root.AddCommand(&cobra.Command{
		Use: "init", Short: "create the default .kluzo/ configuration", Args: args(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := app.InitConfig{}.Execute(cmd.Context(), app.InitConfigInput{Dir: *dir})
			if err != nil {
				return usageError{err}
			}
			for _, f := range out.Files {
				printf(stdout, "created %s/%s\n", *dir, f)
			}
			printf(stdout, "\nEdit the items marked TODO, then run `kluzo roles check`.\n")
			return nil
		},
	})

	roles := &cobra.Command{Use: "roles", Short: "work with the roles", Args: cobra.ArbitraryArgs, RunE: group(stderr)}
	roles.AddCommand(&cobra.Command{
		Use: "check", Short: "validate the roles against the protocol of this binary", Args: args(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := app.CheckRoles{}.Execute(cmd.Context(), app.CheckRolesInput{Dir: *dir})
			if err != nil {
				return usageError{err}
			}
			if len(out.Problems) > 0 {
				for _, p := range out.Problems {
					printf(stderr, "%s\n", p)
				}
				printf(stderr, "\n%d problem(s) found\n", len(out.Problems))
				return exitError{exitInvalidConfig}
			}
			printf(stdout, "%d roles valid\n", len(out.Roles))
			return nil
		},
	})
	root.AddCommand(roles)

	root.AddCommand(&cobra.Command{
		Use: "doctor", Short: "check the CLIs, the token, the configuration and the credentials of the host", Args: args(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := app.Doctor{Host: host()}.Execute(cmd.Context(), app.DoctorInput{Dir: *dir})
			if err != nil {
				return err
			}
			return report(stdout, stderr, out)
		},
	})

	planCmd := &cobra.Command{
		Use: "plan <milestone>", Short: "create the workspace of a milestone from .kluzo/plans/<milestone>.yaml", Args: args(1),
	}
	ref := planCmd.Flags().String("ref", "", "the number of the milestone in the tracker, when more than one has its title")
	planCmd.RunE = func(cmd *cobra.Command, a []string) error {
		out, err := app.PlanWorkspace{Host: host()}.Execute(cmd.Context(), app.PlanWorkspaceInput{ConfigDir: *dir, Milestone: a[0], Ref: *ref})
		if err != nil {
			return err
		}
		printf(stdout, "workspace of %s created with %d node(s) in %s\nReview the plan, then run `kluzo approve %s`.\n", out.Milestone, out.Nodes, out.Dir, out.Milestone)
		return nil
	}
	root.AddCommand(planCmd)

	approveCmd := &cobra.Command{
		Use: "approve <milestone>", Short: "approve the plan: it is committed on the milestone branch", Args: args(1),
	}
	by := approveCmd.Flags().String("by", "", "who approves: a login of tracker.operators (default: the only operator)")
	approveCmd.RunE = func(cmd *cobra.Command, a []string) error {
		if err := (app.ApprovePlan{Host: host()}).Execute(cmd.Context(), app.ApprovePlanInput{ConfigDir: *dir, Milestone: a[0], By: *by}); err != nil {
			return err
		}
		printf(stdout, "plan of %s approved; run `kluzo run %s`.\n", a[0], a[0])
		return nil
	}
	root.AddCommand(approveCmd)

	root.AddCommand(&cobra.Command{
		Use: "run <milestone>", Short: "run (or go on running) the milestone until it is over or interrupted", Args: args(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			state, err := app.RunWorkspace{Host: host(), Version: version}.Execute(ctx, app.RunWorkspaceInput{ConfigDir: *dir, Milestone: a[0]})
			if err != nil {
				return err
			}
			printf(stdout, "workspace %s: %s\n", a[0], state)
			return nil
		},
	})

	statusCmd := &cobra.Command{
		Use: "status <milestone>", Short: "state of the milestone and its nodes", Args: args(1),
	}
	asJSON := statusCmd.Flags().Bool("json", false, "print JSON, for scripts")
	statusCmd.RunE = func(cmd *cobra.Command, a []string) error {
		out, err := app.Status{Host: host()}.Execute(cmd.Context(), app.StatusInput{ConfigDir: *dir, Milestone: a[0]})
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}
		printStatus(stdout, out)
		return nil
	}
	root.AddCommand(statusCmd)

	for _, m := range []struct {
		use, short string
		move       app.Move
	}{
		{"pause <milestone>", "pause the milestone: no new session, gate or integration", app.Pause},
		{"resume <milestone>", "resume a paused milestone (kluzo doctor must be green)", app.Resume},
	} {
		root.AddCommand(&cobra.Command{
			Use: m.use, Short: m.short, Args: args(1),
			RunE: func(cmd *cobra.Command, a []string) error {
				state, err := app.MoveWorkspace{Host: host()}.Execute(cmd.Context(), app.MoveWorkspaceInput{ConfigDir: *dir, Milestone: a[0], Move: m.move})
				if err != nil {
					return err
				}
				printf(stdout, "workspace %s: %s\n", a[0], state)
				return nil
			},
		})
	}
	cleanCmd := &cobra.Command{
		Use: "clean", Short: "remove what Kluzo left on this machine (lists only, unless --yes)", Args: args(0),
	}
	yes := cleanCmd.Flags().Bool("yes", false, "remove; without it, kluzo clean only lists (--dry-run)")
	dryRun := cleanCmd.Flags().Bool("dry-run", true, "only list what would be removed (the default)")
	images := cleanCmd.Flags().Bool("images", false, "also the images of every compose project of Kluzo whose workspace is not active, and the dangling images of the machine; never those of an active workspace (nor, while a journal of the machine cannot be read, those of unknown workspaces)")
	cleanCmd.RunE = func(cmd *cobra.Command, _ []string) error {
		switch {
		case *yes && cmd.Flags().Changed("dry-run") && *dryRun:
			return usageError{fmt.Errorf("--yes and --dry-run contradict each other")}
		case !*yes && !*dryRun:
			return usageError{fmt.Errorf("--dry-run=false removes nothing by itself: use --yes")}
		}
		out, err := app.Clean{Host: host()}.Execute(cmd.Context(), app.CleanInput{ConfigDir: *dir, Yes: *yes, Images: *images})
		if err != nil {
			return err
		}
		printClean(stdout, out)
		return nil
	}
	root.AddCommand(cleanCmd)

	proposal := &cobra.Command{Use: "proposal", Short: "decide the proposals of the agents (ADR 0020)", Args: cobra.ArbitraryArgs, RunE: group(stderr)}
	proposal.AddCommand(&cobra.Command{
		Use: "list", Short: "list the proposals of the workspaces of the project", Args: args(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			items, err := app.ListProposals{Host: host()}.Execute(cmd.Context(), *dir)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				printf(stdout, "no proposals\n")
			}
			for _, p := range items {
				printf(stdout, "%s  %-9s %-7s #%-5s %s  %s\n", p.ID, p.State, p.Kind, p.Issue, p.Milestone, strings.Join(p.Changes, "; "))
			}
			return nil
		},
	})
	for _, d := range []struct {
		use, short string
		approve    bool
	}{
		{"approve <id>", "approve a proposal: the change is kept, and the check lets it pass", true},
		{"reject <id>", "reject a proposal: the node goes back to the implementer to revert the change", false},
	} {
		proposal.AddCommand(&cobra.Command{
			Use: d.use, Short: d.short, Args: args(1),
			RunE: func(cmd *cobra.Command, a []string) error {
				if err := (app.DecideProposal{Host: host()}).Execute(cmd.Context(), app.DecideProposalInput{ConfigDir: *dir, ID: a[0], Approve: d.approve}); err != nil {
					return err
				}
				printf(stdout, "proposal %s %s\n", a[0], map[bool]string{true: "approved", false: "rejected"}[d.approve])
				return nil
			},
		})
	}
	root.AddCommand(proposal)

	node := &cobra.Command{Use: "node", Short: "take a node out of blocked, or cancel it", Args: cobra.ArbitraryArgs, RunE: group(stderr)}
	for _, n := range []struct {
		use, short string
		action     app.NodeAction
	}{
		{"retry <issue>", "start a new attempt of a blocked node, if one is left", app.NodeRetry},
		{"unblock <issue>", "put a blocked node back where it was, after you acted, and run its step again", app.NodeUnblock},
		{"cancel <issue>", "remove the node from the milestone (its branch stays on the remote)", app.NodeCancel},
	} {
		node.AddCommand(&cobra.Command{
			Use: n.use, Short: n.short, Args: args(1),
			RunE: func(cmd *cobra.Command, a []string) error {
				issue := strings.TrimPrefix(a[0], "#")
				if err := (app.DecideNode{Host: host()}).Execute(cmd.Context(), app.DecideNodeInput{ConfigDir: *dir, Issue: issue, Action: n.action}); err != nil {
					return err
				}
				printf(stdout, "node #%s: %s recorded; kluzo run carries it out\n", issue, n.action)
				return nil
			},
		})
	}
	root.AddCommand(node)

	effect := &cobra.Command{Use: "effect", Short: "act on the effects of the outbox", Args: cobra.ArbitraryArgs, RunE: group(stderr)}
	retry := &cobra.Command{
		Use: "retry <id>|--all", Short: "return a failed effect to pending, once its cause is fixed (kluzo doctor must be green)", Args: cobra.MaximumNArgs(1),
	}
	all := retry.Flags().Bool("all", false, "every failed effect of the active workspaces of the project")
	retry.RunE = func(cmd *cobra.Command, a []string) error {
		if (len(a) == 1) == *all {
			return usageError{fmt.Errorf("name one failed effect, or --all")}
		}
		in := app.RetryEffectsInput{ConfigDir: *dir, All: *all}
		if len(a) == 1 {
			in.ID = a[0]
		}
		out, err := app.RetryEffects{Host: host()}.Execute(cmd.Context(), in)
		if err != nil {
			return err
		}
		for _, f := range out {
			printf(stdout, "%s  %-18s %s  pending again (was: %s)\n", f.ID, f.Kind, f.Milestone, f.LastError)
		}
		printf(stdout, "kluzo run carries it out\n")
		return nil
	}
	effect.AddCommand(retry)
	root.AddCommand(effect)

	root.SetContext(context.Background())
	return root
}

// printClean prints what kluzo clean found, and whether it removed it.
func printClean(w io.Writer, out app.CleanOutput) {
	verb := "would remove"
	if out.Removed {
		verb = "removed"
	}
	for _, group := range []struct {
		what  string
		items []string
	}{
		{"checkout", out.Checkouts}, {"leftover", out.Leftovers}, {"compose project", out.Projects}, {"image", out.Images},
	} {
		for _, i := range group.items {
			printf(w, "%s %s %s\n", verb, group.what, i)
		}
	}
	if out.Dangling {
		printf(w, "%s the dangling images\n", verb)
	}
	for _, u := range out.Unsure {
		printf(w, "left alone (its journal cannot be read): %s\n", u)
	}
	if out.Docker != "" {
		printf(w, "docker not checked: %s\n", out.Docker)
	}
	printf(w, "the workspaces and caches of the project took %s\n", app.HumanSize(out.Disk))
	n := len(out.Checkouts) + len(out.Leftovers) + len(out.Projects) + len(out.Images)
	switch {
	case n == 0:
		printf(w, "nothing to clean\n")
	case !out.Removed:
		printf(w, "\n%d item(s); run `kluzo clean --yes` to remove them\n", n)
	}
}

// group is the run of a command that only groups others: without a
// subcommand it prints its usage, and an unknown one is a usage error.
func group(stderr io.Writer) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, a []string) error {
		if len(a) > 0 {
			return usageError{fmt.Errorf("unknown command %q (see %s --help)", a[0], cmd.CommandPath())}
		}
		cmd.SetOut(stderr)
		_ = cmd.Usage()
		return exitError{exitUsage}
	}
}

// report prints what the doctor found and returns its exit code: 2 for an
// invalid configuration, 3 for integrations or credentials of the host.
func report(stdout, stderr io.Writer, out app.DoctorOutput) error {
	if len(out.Invalid) > 0 {
		for _, p := range out.Invalid {
			printf(stderr, "%s\n", p)
		}
		return exitError{exitInvalidConfig}
	}
	for _, p := range out.Passed {
		printf(stdout, "ok: %s\n", p)
	}
	for _, w := range out.Warnings {
		printf(stdout, "warning: %s\n", w)
	}
	for _, p := range out.Problems {
		printf(stderr, "problem: %s\n", p)
	}
	for _, r := range out.Refused {
		printf(stderr, "refused: %s\n", r)
	}
	if len(out.Refused) > 0 {
		printf(stderr, "\nKluzo will not start sessions while credentials of the host are visible to them (ADR 0020).\nRun Kluzo as a dedicated user, in a container or a VM, or set security.allow_host_credentials: true.\n")
	}
	if !out.OK() {
		return exitError{exitIntegration}
	}
	return nil
}

// printStatus prints the status for a person.
func printStatus(w io.Writer, s app.StatusOutput) {
	printf(w, "milestone %s (%s): %s\n", s.Milestone, s.Workspace, s.State)
	printf(w, "branch %s: %s\n", s.Branch, s.BranchOK)
	if s.PR != "" {
		printf(w, "PR: %s\n", s.PR)
	}
	printf(w, "cost US$ %.2f", s.CostUSD)
	if s.BudgetUSD > 0 {
		printf(w, " of US$ %.2f", s.BudgetUSD)
	}
	printf(w, "\ndisk: workspace %s, caches %s", app.HumanSize(s.Disk.Workspace), app.HumanSize(s.Disk.Caches))
	if s.Disk.Docker == "" {
		printf(w, ", images %s, docker build cache %s", app.HumanSize(s.Disk.Images), app.HumanSize(s.Disk.BuildCache))
	} else {
		printf(w, " (docker: %s)", s.Disk.Docker)
	}
	printf(w, "\n\n")
	for _, n := range s.Nodes {
		state := n.State
		if n.Blocked != "" {
			state += " (" + n.Blocked + ")"
		}
		printf(w, "#%-5s %-14s attempt %d round %d  US$ %.2f  %s\n", n.Issue, state, n.Attempt, n.Round, n.CostUSD, n.Title)
	}
	if len(s.Failed) > 0 {
		printf(w, "\nfailed effects (fix the cause, then `kluzo effect retry <id>`):\n")
	}
	for _, f := range s.Failed {
		printf(w, "%s  %-18s %s\n", f.ID, f.Kind, f.LastError)
	}
}

// printf writes to a terminal stream. A failed write to stdout/stderr cannot
// be reported anywhere useful, so the error is deliberately discarded.
func printf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}
