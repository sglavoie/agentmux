package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/cmd/session"
	"github.com/sglavoie/agentmux/internal/kittykeys"
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
		"notifications", "overseer", "overview", "release-gate", "resort",
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
