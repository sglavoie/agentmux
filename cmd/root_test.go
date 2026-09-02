package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/cmd/session"
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
	if !strings.Contains(stdout, "resort") {
		t.Errorf("root help should list public commands like resort, got:\n%s", stdout)
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
		"notifications", "release-gate", "resort", "session", "transcripts",
		"tui-test", "worktree",
	} {
		if !registered[name] {
			t.Errorf("root command %q is not registered", name)
		}
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
