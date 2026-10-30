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
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/sglavoie/agentmux/cmd/config"
	"github.com/sglavoie/agentmux/cmd/daemon"
	"github.com/sglavoie/agentmux/cmd/dashboard"
	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/cmd/hooks"
	"github.com/sglavoie/agentmux/cmd/notifications"
	"github.com/sglavoie/agentmux/cmd/overseer"
	"github.com/sglavoie/agentmux/cmd/plans"
	"github.com/sglavoie/agentmux/cmd/project"
	"github.com/sglavoie/agentmux/cmd/release"
	"github.com/sglavoie/agentmux/cmd/session"
	"github.com/sglavoie/agentmux/cmd/transcripts"
	"github.com/sglavoie/agentmux/cmd/worktree"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const version = "0.9.0"

var (
	dashboardSourceWindowID int
	dashboardBackend        string
	dashboardView           dashboard.View
)

var rootCmd = &cobra.Command{
	Use:   "amx",
	Short: "Agent Mux multi-harness session manager",
	Long: `Agent Mux is a multi-harness session manager. It manages Claude Code,
Copilot, Codex, and other supported harness sessions with an interactive
dashboard, on its own tmux server from any terminal or Agent Mux.app, or as
color-coded Kitty tabs.

Run with no command to open the dashboard. It takes the flags ` + "`amx overview`" + `
documents (--backend, --select, --client, --popup, --window-id).`,
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
		return dashboard.Run(dashboardSourceWindowID, dashboardBackend, dashboardView)
	},
}

func Execute() {
	if code := execute(os.Args[1:], os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// runArgs is the argument list execute is running, which the flag-error
// handler reads to find a misspelled command and whether --json was asked
// for.
var runArgs []string

func invocationArgs() []string { return runArgs }

// execute runs the root command on args and returns the process status,
// writing the one stderr line a failure deserves. Cobra's own error printing
// is silenced on rootCmd so that this is the only place that line comes from:
// left to cobra, a JSON failure that had already written its envelope was
// still followed by "Error: ...", and a non-quiet ExitError was printed twice.
func execute(args []string, stderr io.Writer) int {
	runArgs = args
	rootCmd.SetArgs(args)
	command, err := rootCmd.ExecuteC()
	if err == nil {
		return 0
	}
	if code, ok := invocationFailure(err); ok && jsonRequested(args) {
		message := err.Error()
		var unknown *unknownCommandError
		if errors.As(err, &unknown) {
			message = unknown.summary()
		}
		// WriteJSONError returns its quiet exit once the envelope is written;
		// any other error is the write itself failing.
		writeErr := cli.WriteJSONError(command, cli.JSONError{Code: code, Message: message, Candidates: unknownCommandCandidates(err)})
		if exitErr := (*cli.ExitError)(nil); !errors.As(writeErr, &exitErr) {
			_, _ = fmt.Fprintln(stderr, command.ErrPrefix(), writeErr.Error())
		}
		return 1
	}
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		// The command already emitted its own output (and mapped the
		// failure to a specific code). Only print when it did not present a
		// complete structured result, so JSON runs are not followed by a
		// redundant stderr line.
		if !exitErr.Quiet && exitErr.Err != nil {
			_, _ = fmt.Fprintln(stderr, command.ErrPrefix(), exitErr.Error())
		}
		return exitErr.Code
	}
	_, _ = fmt.Fprintln(stderr, command.ErrPrefix(), err.Error())
	// Cobra adds this pointer only when it could not resolve the command;
	// keep doing the same now that it no longer prints.
	if _, ok := invocationFailure(err); ok {
		var unknown *unknownCommandError
		if errors.As(err, &unknown) && len(unknown.suggestions) == 0 {
			if paths := nestedCommandPaths(command, unknown.name); len(paths) > 0 {
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
func nestedCommandPaths(command *cobra.Command, name string) []string {
	if name == "" {
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
	dashboard.AddViewFlags(rootCmd, &dashboardView, "dashboard only: ")
	// Help groups exist so `amx --help` reads by purpose rather than as one
	// alphabetical run of two dozen names. Hidden commands take no group.
	rootCmd.AddGroup(
		&cobra.Group{ID: groupSessions, Title: "Sessions and projects:"},
		&cobra.Group{ID: groupTabs, Title: "Session controls:"},
		&cobra.Group{ID: groupSetup, Title: "Setup and diagnostics:"},
	)
	addGrouped(groupSessions, session.Command(), project.Command(), session.AttachCommand(), session.SendCommand(), session.CloseCommand(), worktree.Command(), plans.Command(), dashboard.OverviewCommand(), dashboard.PastSessionsCommand(), dashboard.OpenDashboardCommand())
	addGrouped(groupTabs, session.FocusNextCommand(), session.FocusNextManagedCommand(), session.FocusPrevManagedCommand(), session.MuteToggleCommand(), session.NameSessionCommand(), session.PinSessionCommand(), session.ResortCommand(), dashboard.ColorPickerCommand(), dashboard.IconPickerCommand(), dashboard.KeybindingsCommand())
	addGrouped(groupSetup, config.Command(), daemon.Command(), debug.Command(), debug.DoctorCommand(), session.BackendCommand(), notifications.Command(), transcripts.Command(), overseer.Command())
	rootCmd.AddCommand(hooks.IndicatorCommand(), release.Command(), release.DesktopCommand(), scenarioHarnessCommand(), dashboard.WorkspaceViewCommand())
	rootCmd.AddCommand(versionCmd)
	rootCmd.SetFlagErrorFunc(flagError)
	// Added now rather than lazily by Execute, so the guard covers it too.
	rootCmd.InitDefaultCompletionCmd()
	guardInvocations(rootCmd)
	completeNoFilesByDefault(rootCmd)
	completeSharedFlags(rootCmd)
	aliasWindowFlags(rootCmd)
	completeFlagsWithoutFiles(rootCmd)
	wrapLongHelp(rootCmd)
	// The dashboard's own flags are accepted as before but left out of
	// `amx --help`, where they read as flags of every command; the
	// description names them, and `amx overview --help` documents the same
	// ones.
	for _, name := range []string{cli.BackendFlag, "window-id", "select", "client", "popup"} {
		_ = rootCmd.Flags().MarkHidden(name)
	}
}

// helpWidth is the column a command description is wrapped at.
const helpWidth = 80

// wrapLongHelp fills every description paragraph to helpWidth columns, so
// `--help` reads in a terminal of ordinary width whichever way a family wrote
// its description: built from one-line strings, assembled from shared
// sentences, or wrapped by hand at another width.
func wrapLongHelp(command *cobra.Command) {
	command.Long = wrapHelpText(command.Long, helpWidth)
	for _, child := range command.Commands() {
		wrapLongHelp(child)
	}
}

// wrapHelpText refills each paragraph of text — a run of consecutive lines
// that are neither blank nor indented — to width display columns. Breaking
// each long line on its own would leave a short orphan wherever a family's
// line ran a word or two over, so the paragraph is joined and filled as one.
// Blank and indented lines (lists, aligned tables, commands) are kept as
// written and end a paragraph. Width is measured in terminal columns, not
// bytes, so a line with an emoji or an accented letter is not broken early.
func wrapHelpText(text string, width int) string {
	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	var paragraph []string
	flush := func() {
		if len(paragraph) != 0 {
			wrapped = append(wrapped, fillHelpParagraph(helpWords(strings.Join(paragraph, " ")), width)...)
			paragraph = paragraph[:0]
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			flush()
			wrapped = append(wrapped, line)
			continue
		}
		paragraph = append(paragraph, line)
	}
	flush()
	return strings.Join(wrapped, "\n")
}

// fillHelpParagraph lays words out greedily in lines of at most width
// columns; a word wider than width gets a line of its own.
func fillHelpParagraph(words []string, width int) []string {
	var lines []string
	current, columns := "", 0
	for _, word := range words {
		wordColumns := runewidth.StringWidth(word)
		if current != "" && columns+1+wordColumns > width {
			lines = append(lines, current)
			current, columns = "", 0
		}
		if current == "" {
			current, columns = word, wordColumns
			continue
		}
		current += " " + word
		columns += 1 + wordColumns
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// helpWords splits prose at spaces, except inside a `code span`, which stays
// on one line: `amx session list` broken after `amx` reads as two commands.
// Text whose backticks do not pair up is split at every space.
func helpWords(text string) []string {
	var words []string
	var current strings.Builder
	inCode := false
	for _, r := range text {
		switch {
		case r == '`':
			inCode = !inCode
			current.WriteRune(r)
		case unicode.IsSpace(r) && inCode:
			current.WriteRune(' ')
		case unicode.IsSpace(r):
			if current.Len() != 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if inCode {
		return strings.Fields(text)
	}
	if current.Len() != 0 {
		words = append(words, current.String())
	}
	return words
}

// aliasWindowFlags makes --window and --window-id, which name the same Kitty
// window on different commands, both work wherever either does: the other
// spelling is a hidden flag sharing the same value, so help shows one name and
// scripts written with either keep working.
func aliasWindowFlags(command *cobra.Command) {
	for _, flags := range []*pflag.FlagSet{command.LocalNonPersistentFlags(), command.PersistentFlags()} {
		for name, alias := range map[string]string{"window-id": "window", "window": "window-id"} {
			flag := flags.Lookup(name)
			if flag == nil || command.Flags().Lookup(alias) != nil || command.InheritedFlags().Lookup(alias) != nil {
				continue
			}
			target := command.Flags()
			if flags == command.PersistentFlags() {
				target = command.PersistentFlags()
			}
			target.AddFlag(&pflag.Flag{Name: alias, Value: flag.Value, DefValue: flag.DefValue, Usage: "same as --" + name, Hidden: true})
		}
	}
	for _, child := range command.Commands() {
		aliasWindowFlags(child)
	}
}

// completeSharedFlags gives the flags that mean the same thing on every
// command one completion each, wherever a family did not register its own:
// --runtime-session and a surface's --select complete the running runtime
// sessions, and --backend the two backends.
func completeSharedFlags(command *cobra.Command) {
	shared := map[string]cobra.CompletionFunc{
		cli.RuntimeSessionFlag: cli.CompleteRuntimeSessions,
		"select":               cli.CompleteRuntimeSessions,
		cli.BackendFlag:        cli.CompleteBackends,
	}
	for name, complete := range shared {
		if command.NonInheritedFlags().Lookup(name) == nil {
			continue
		}
		if _, ok := command.GetFlagCompletionFunc(name); !ok {
			_ = command.RegisterFlagCompletionFunc(name, complete)
		}
	}
	for _, child := range command.Commands() {
		completeSharedFlags(child)
	}
}

// pathFlags are the flags whose value is a file or directory, which keep the
// shell's file completion.
var pathFlags = map[string]bool{
	"output": true, "file": true, "directory": true, "command": true,
	"evidence-dir": true, "signing-key": true, "asl-root": true, "amx-root": true,
}

// completeFlagsWithoutFiles gives every flag that takes a value and has no
// completion of its own no completion at all, as completeNoFilesByDefault
// does for arguments: cobra's default is the shell's file completion, so
// <Tab> after --limit or --trace offered the files in the current directory.
// A flag that does take a path is named in pathFlags and left alone.
func completeFlagsWithoutFiles(command *cobra.Command) {
	command.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Value.Type() == "bool" || pathFlags[flag.Name] {
			return
		}
		if _, ok := command.GetFlagCompletionFunc(flag.Name); !ok {
			_ = command.RegisterFlagCompletionFunc(flag.Name, cobra.NoFileCompletions)
		}
	})
	for _, child := range command.Commands() {
		completeFlagsWithoutFiles(child)
	}
}

// versionCmd answers `amx version` the way `amx --version` does, since both
// spellings are commonly typed. It is hidden: --version is the documented one.
var versionCmd = &cobra.Command{
	Use:    "version",
	Short:  "Print the amx build identity (same as --version)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s version %s\n", rootCmd.Name(), rootCmd.Version)
		return err
	},
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
