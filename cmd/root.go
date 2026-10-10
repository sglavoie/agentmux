// Package cmd is the root of the amx CLI and nothing more: the root command
// (rootCmd) itself, the artifact version it reports, the exit-status mapping
// Execute applies, and the assembly that registers every command family's
// leaves under it. Every user-facing command lives in a child package; this
// package owns no command behavior of its own, which is why it is the one
// package in the cmd tree that no other cmd package may import.
//
// Because assembly is all it does, "did every family reach the user" is the
// one regression it can hide, so TestCommandFamiliesAreRegisteredUnderRoot in
// root_test.go is the single root-owned registration assertion rather than one
// such file per family. Cross-family acceptance tests that name no command
// symbol (process lifecycle, diagnostics release, the ASL contract) also stay
// here.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sglavoie/agentmux/cmd/config"
	"github.com/sglavoie/agentmux/cmd/daemon"
	"github.com/sglavoie/agentmux/cmd/dashboard"
	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/cmd/hooks"
	"github.com/sglavoie/agentmux/cmd/notifications"
	"github.com/sglavoie/agentmux/cmd/overseer"
	"github.com/sglavoie/agentmux/cmd/project"
	"github.com/sglavoie/agentmux/cmd/release"
	"github.com/sglavoie/agentmux/cmd/session"
	"github.com/sglavoie/agentmux/cmd/transcripts"
	"github.com/sglavoie/agentmux/cmd/tuitest"
	"github.com/sglavoie/agentmux/cmd/worktree"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

const version = "0.8.0"

var (
	dashboardSourceWindowID int
	dashboardBackend        string
)

var rootCmd = &cobra.Command{
	Use:   "amx",
	Short: "Agent Mux multi-harness session manager for Kitty",
	Long:  "Agent Mux is a multi-harness session manager for Kitty. It manages Claude Code, Copilot, Codex, and other supported harness sessions as color-coded tabs with an interactive dashboard.",
	// A failed command reports its error; it does not reprint its own usage.
	// The usage block is longer than most errors and pushed the one line that
	// says what went wrong — and how to answer it — off the top of the screen.
	// `--help` still prints it, and argument and flag errors still name the
	// problem.
	SilenceUsage: true,
	// execute prints every failure itself, exactly once; see there.
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cli.ConfigureLogging(cli.DebugEnabled())
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return dashboard.Run(dashboardSourceWindowID, dashboardBackend)
	},
}

func Execute() {
	if code := execute(os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// execute runs the root command and returns the process status, writing the
// one stderr line a failure deserves. Cobra's own error printing is silenced
// on rootCmd so that this is the only place that line comes from: left to
// cobra, a JSON failure that had already written its envelope was still
// followed by "Error: ...", and a non-quiet ExitError was printed twice.
func execute(stderr io.Writer) int {
	command, err := rootCmd.ExecuteC()
	if err == nil {
		return 0
	}
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		// The command already emitted its own output (and mapped the
		// failure to a specific code). Only print when it did not present a
		// complete structured result, so JSON runs are not followed by a
		// redundant stderr line.
		if !exitErr.Quiet && exitErr.Err != nil {
			_, _ = fmt.Fprintln(stderr, exitErr.Error())
		}
		return exitErr.Code
	}
	_, _ = fmt.Fprintln(stderr, command.ErrPrefix(), err.Error())
	// Cobra adds this pointer only when it could not resolve the command;
	// keep doing the same now that it no longer prints.
	if strings.HasPrefix(err.Error(), "unknown command") {
		if !strings.Contains(err.Error(), "Did you mean") {
			if paths := nestedCommandPaths(command, err.Error()); len(paths) > 0 {
				_, _ = fmt.Fprintf(stderr, "\nDid you mean this?\n\t%s\n\n", strings.Join(paths, "\n\t"))
			}
		}
		_, _ = fmt.Fprintf(stderr, "Run '%v --help' for usage.\n", command.CommandPath())
	}
	return 1
}

// nestedCommandPaths answers an unknown command that is the name of a
// command one or more levels down — `amx search`, `amx status`, `amx prune` —
// with the full paths that name it. Cobra's own suggestions only catch
// misspellings of the commands at the level typed, so a leaf typed without its
// family otherwise got nothing but a pointer at --help.
func nestedCommandPaths(command *cobra.Command, message string) []string {
	var name string
	if _, err := fmt.Sscanf(message, "unknown command %q", &name); err != nil || name == "" {
		return nil
	}
	var paths []string
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if !child.IsAvailableCommand() {
				continue
			}
			if child.Name() == name || child.HasAlias(name) {
				paths = append(paths, child.CommandPath())
			}
			walk(child)
		}
	}
	walk(command)
	return paths
}

const (
	groupSessions = "sessions"
	groupTabs     = "tabs"
	groupSetup    = "setup"
)

// addGrouped registers commands under root in one help group.
func addGrouped(group string, commands ...*cobra.Command) {
	for _, command := range commands {
		command.GroupID = group
	}
	rootCmd.AddCommand(commands...)
}

func init() {
	// Version output carries the full artifact identity (VCS revision, dirty
	// marker, build time, Go version, binary hash) so an installed CLI can be
	// matched against the daemon's reported build.
	buildinfo.SetVersion(version)
	rootCmd.Version = buildinfo.Current().String()
	rootCmd.PersistentFlags().BoolVar(&cli.DebugFlag, "debug", false, "enable verbose debug output")
	rootCmd.Flags().StringVar(&dashboardBackend, cli.BackendFlag, "", "dashboard only: "+dashboard.BackendFlagUsage)
	rootCmd.Flags().IntVar(&dashboardSourceWindowID, "window-id", 0, "dashboard only: start with this Kitty window's tab selected (open-dashboard passes it)")
	// Help groups exist so `amx --help` reads by purpose rather than as one
	// alphabetical run of two dozen names. Hidden commands take no group.
	rootCmd.AddGroup(
		&cobra.Group{ID: groupSessions, Title: "Sessions and projects:"},
		&cobra.Group{ID: groupTabs, Title: "Kitty tabs:"},
		&cobra.Group{ID: groupSetup, Title: "Setup and diagnostics:"},
	)
	addGrouped(groupSessions, session.Command(), project.Command(), session.AttachCommand(), worktree.Command(), dashboard.OverviewCommand(), dashboard.OpenDashboardCommand())
	addGrouped(groupTabs, session.FocusNextCommand(), session.FocusNextManagedCommand(), session.FocusPrevManagedCommand(), session.MuteToggleCommand(), session.ResortCommand(), dashboard.ColorPickerCommand(), dashboard.IconPickerCommand(), dashboard.KeybindingsCommand())
	addGrouped(groupSetup, config.Command(), daemon.Command(), debug.Command(), debug.DoctorCommand(), notifications.Command(), transcripts.Command(), overseer.Command())
	rootCmd.AddCommand(hooks.IndicatorCommand(), release.Command(), tuitest.Command(), dashboard.WorkspaceViewCommand())
	completeNoFilesByDefault(rootCmd)
}

// completeNoFilesByDefault gives every command that does not say how its
// arguments complete no argument completion at all. Cobra's default is the
// shell's file completion, so <Tab> after `amx session list` or `amx doctor`
// offered the files in the current directory, none of which either takes. A
// command whose argument really is a path declares a ValidArgsFunction of its
// own, as debug replay and tui-test run do, and is left alone.
func completeNoFilesByDefault(command *cobra.Command) {
	if command.ValidArgsFunction == nil && len(command.ValidArgs) == 0 {
		command.ValidArgsFunction = cobra.NoFileCompletions
	}
	for _, child := range command.Commands() {
		completeNoFilesByDefault(child)
	}
}
