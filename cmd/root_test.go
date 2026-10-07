package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/cmd/session"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runRootArgs drives rootCmd exactly like the real binary (so subcommand and
// flag resolution match production), captures stdout/stderr, and returns the
// error rootCmd.Execute produced.
func runRootArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRootHelp(t *testing.T) {
	stdout, _, err := runRootArgs(t, "--help")
	if err != nil {
		t.Fatalf("help returned error: %v", err)
	}
	if strings.Contains(stdout, "tui-test") {
		t.Errorf("root help must not list the hidden tui-test command, got:\n%s", stdout)
	}
	// Harness hooks call hook-indicator through the binary; an operator never
	// types it, so it must not crowd the help.
	if strings.Contains(stdout, "hook-indicator") {
		t.Errorf("root help must not list the internal hook-indicator command, got:\n%s", stdout)
	}
	for _, group := range []string{"Sessions and projects:", "Kitty tabs:", "Setup and diagnostics:"} {
		if !strings.Contains(stdout, group) {
			t.Errorf("root help is missing the %q group, got:\n%s", group, stdout)
		}
	}
	// A public command must still be visible so we know help itself rendered.
	for _, public := range []string{"resort", "overview"} {
		if !strings.Contains(stdout, public) {
			t.Errorf("root help should list the public command %q, got:\n%s", public, stdout)
		}
	}
	if !strings.Contains(stdout, "Open or focus the live session overview") {
		t.Errorf("root help does not describe the overview command:\n%s", stdout)
	}
	for _, removed := range []string{"index", "plan", "search", "workspace"} {
		if strings.Contains(stdout, "  "+removed+" ") {
			t.Errorf("root help must not list removed command %q, got:\n%s", removed, stdout)
		}
	}
	if !strings.Contains(stdout, "multi-harness session manager") {
		t.Errorf("root help has stale product description:\n%s", stdout)
	}
}

// Root is now assembly only, so "did every family reach the user" is the one
// thing that can silently break here: a child package can register its own
// leaves perfectly and still be invisible if root forgets to add it. This is
// the single root-owned registration test for that, rather than one file per
// family.
func TestCommandFamiliesAreRegisteredUnderRoot(t *testing.T) {
	registered := make(map[string]bool)
	for _, command := range rootCmd.Commands() {
		registered[command.Name()] = true
	}
	for _, name := range []string{
		"color-picker", "config", "daemon", "debug", "doctor",
		"focus-next", "focus-next-managed", "focus-prev-managed",
		"hook-indicator", "icon-picker", "keybindings", "mute-toggle",
		"notifications", "open-dashboard", "overseer", "overview", "project",
		"release-gate", "resort",
		"session", "transcripts", "tui-test", "worktree",
	} {
		if !registered[name] {
			t.Errorf("root command %q is not registered", name)
		}
	}
}

// A Kitty binding is argv the operator never types and the compiler never sees,
// so a renamed command or a dropped flag turns a keypress into an error message
// inside a window that closes. Root is where the two halves meet, so this is
// where the catalog is checked against the commands it actually launches.
func TestEveryKittyBindingLaunchesARegisteredCommand(t *testing.T) {
	for _, action := range kittykeys.Actions {
		if action.LaunchType == "" {
			continue // A native Kitty action; amx is not involved.
		}
		t.Run(action.Key, func(t *testing.T) {
			args := strings.Fields(action.AmxCommand)

			target := rootCmd
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				found, remaining, err := rootCmd.Find(args)
				if err != nil {
					t.Fatalf("%s launches %q, which root cannot resolve: %v", action.Key, action.AmxCommand, err)
				}
				if found == rootCmd {
					t.Fatalf("%s launches %q, which resolved to the root command", action.Key, action.AmxCommand)
				}
				target, args = found, remaining
			}

			for _, arg := range args {
				if !strings.HasPrefix(arg, "--") {
					continue
				}
				name := strings.TrimPrefix(strings.SplitN(arg, "=", 2)[0], "--")
				if target.Flags().Lookup(name) == nil && target.InheritedFlags().Lookup(name) == nil {
					t.Fatalf("%s passes --%s, which %q does not accept", action.Key, name, target.CommandPath())
				}
			}
		})
	}
}

// F2 is the overview's only keyboard entry point, so the binding and the command
// are asserted together: the catalog's subcommand is the registered one, and the
// source window it passes is a flag that command really has.
func TestTheOverviewBindingMatchesTheRegisteredCommand(t *testing.T) {
	var overview kittykeys.Action
	for _, action := range kittykeys.Actions {
		if action.Key == "f2" {
			overview = action
		}
	}
	if overview.Key == "" {
		t.Fatal("the shared binding catalog has no f2 overview binding")
	}

	found, remaining, err := rootCmd.Find(strings.Fields(overview.AmxCommand))
	if err != nil {
		t.Fatalf("f2 launches %q, which root cannot resolve: %v", overview.AmxCommand, err)
	}
	if found.Name() != "overview" {
		t.Fatalf("f2 resolves to %q, want the overview command", found.CommandPath())
	}
	if len(remaining) != 2 || remaining[0] != "--window-id" || remaining[1] != "@active-kitty-window-id" {
		t.Fatalf("f2 passes %v, want the active Kitty window ID as --window-id", remaining)
	}
	if found.Flags().Lookup("window-id") == nil {
		t.Fatal("amx overview has no --window-id flag for F2 to pass")
	}
}

// The overview is a singleton, so the CLI and the detector have to agree on
// exactly which argv forms are a running overview. Cobra inherits the root's
// persistent --debug into every subcommand, which makes `amx overview --debug`
// as runnable as `amx --debug overview`; a detector that refused the trailing
// spelling would miss the live instance and let F2 open a second one. This is
// asserted here rather than in either package alone because only the assembled
// root knows which flags a subcommand really inherits.
func TestTheOverviewDetectorAcceptsEveryRunnableOverviewArgv(t *testing.T) {
	runnable := [][]string{
		{"overview"},
		{"overview", "--window-id", "101"},
		{"--debug", "overview"},
		{"overview", "--debug"},
		{"overview", "--debug=true"},
		{"overview", "--debug=false"},
		{"overview", "--window-id=101", "--debug=true"},
		{"overview", "--debug", "--window-id", "101"},
	}

	// Parsing writes into the flag targets the real command shares; restore the
	// defaults so a later test in this binary sees an unparsed root.
	t.Cleanup(func() {
		found, _, err := rootCmd.Find([]string{"overview"})
		if err != nil {
			return
		}
		_ = found.ParseFlags([]string{"--debug=false", "--window-id=0"})
	})

	for _, args := range runnable {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			found, remaining, err := rootCmd.Find(args)
			if err != nil {
				t.Fatalf("root cannot resolve %v: %v", args, err)
			}
			if found.Name() != "overview" {
				t.Fatalf("%v resolves to %q, want the overview command", args, found.CommandPath())
			}
			if err := found.ParseFlags(remaining); err != nil {
				t.Fatalf("amx %s is not a runnable invocation: %v", strings.Join(args, " "), err)
			}
			if positional := found.Flags().Args(); len(positional) != 0 {
				t.Fatalf("amx %s leaves positional arguments %v, which the command refuses", strings.Join(args, " "), positional)
			}

			cmdline := append([]string{"/usr/local/bin/amx"}, args...)
			windows := []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 88, Windows: []kitty.Window{{
				ID:                  880,
				ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: cmdline}},
			}}}}}}
			if _, found := kitty.FindOverviewWindow(windows, 0); !found {
				t.Fatalf("the runnable argv %v is not detected as an open overview", cmdline)
			}
		})
	}
}

func TestRemovedCommandsAreNotRegistered(t *testing.T) {
	rootNames := make(map[string]bool)
	for _, command := range rootCmd.Commands() {
		rootNames[command.Name()] = true
	}
	for _, removed := range []string{"index", "plan", "search", "workspace"} {
		if rootNames[removed] {
			t.Errorf("root command %q remains registered", removed)
		}
	}
	sessionNames := make(map[string]bool)
	for _, command := range session.Command().Commands() {
		sessionNames[command.Name()] = true
	}
	for _, removed := range []string{"launch", "focus"} {
		if sessionNames[removed] {
			t.Errorf("session command %q remains registered", removed)
		}
	}
}

// Flag help reads as one voice across every family: a description starts in
// lower case, as cobra's own "help for …" does, unless it opens with a name
// or an acronym.
func TestFlagDescriptionsStartInLowerCase(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		command.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			first, _ := utf8.DecodeRuneInString(flag.Usage)
			word, _, _ := strings.Cut(flag.Usage, " ")
			acronym := len(word) > 1 && strings.ToUpper(word) == word
			if unicode.IsUpper(first) && !acronym && !strings.HasPrefix(flag.Usage, "Kitty ") && !strings.HasPrefix(flag.Usage, "Agent Mux ") {
				t.Errorf("%s --%s: description %q starts in upper case", command.CommandPath(), flag.Name, flag.Usage)
			}
		})
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
}

// Shell completion is what <Tab> offers. Cobra's default for a command that
// does not say is the shell's file completion, which offered the current
// directory's files after commands that take no path at all; root assembly
// now turns that off, and the commands whose argument is a path, a snooze, or
// a debug selector say what to offer instead.
func TestShellCompletionOffersWhatEachCommandTakes(t *testing.T) {
	t.Setenv("amx_CONFIG", writeCompletionConfig(t))
	for _, tc := range []struct {
		args      []string
		directive cobra.ShellCompDirective
		want      []string
	}{
		{[]string{"session", "list", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"doctor", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"worktree", "list", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"transcripts", "status", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"debug", "replay", ""}, cobra.ShellCompDirectiveDefault, nil},
		{[]string{"tui-test", "run", ""}, cobra.ShellCompDirectiveDefault, nil},
		{[]string{"notifications", "snooze", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"30m", "1h", "until", "indefinite", "off"}},
		{[]string{"notifications", "snooze", "until", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"debug", "tail", "--profile", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"work"}},
		{[]string{"debug", "tail", "--harness", "co"}, cobra.ShellCompDirectiveNoFileComp, []string{"copilot", "codex"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, _, err := runRootArgs(t, append([]string{cobra.ShellCompNoDescRequestCmd}, tc.args...)...)
			if err != nil {
				t.Fatalf("completion: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(stdout), "\n")
			directive := lines[len(lines)-1]
			if want := fmt.Sprintf(":%d", tc.directive); directive != want {
				t.Fatalf("directive = %s, want %s\n%s", directive, want, stdout)
			}
			got := lines[:len(lines)-1]
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("candidates = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeCompletionConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[[profiles]]\nname = \"work\"\nharness = \"claude\"\nhome_dir = \"" + t.TempDir() + "\"\ncolor = \"blue\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
