package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The daemon command family lives in cmd/daemon; what root assembly owns is
// that it is registered here under the name `daemon`, with every leaf the
// documented operator procedures type. Asserting registration on a locally
// built parent would be tautological, so the check runs against rootCmd.
func TestDaemonFamilyIsRegistered(t *testing.T) {
	daemonCommand := findCommandNamed(rootCmd, "daemon")
	if daemonCommand == nil {
		t.Fatal("root command has no daemon command")
	}
	for _, leaf := range []string{"start", "stop", "status", "restart", "export-events", "audit"} {
		if findCommandNamed(daemonCommand, leaf) == nil {
			t.Errorf("amx daemon has no %s command", leaf)
		}
	}
}

// TestDaemonExportEventsRejectsPositionalArgs pins the user-facing refusal for
// `amx daemon export-events events.json`, which readers reach for instead of
// --output. The message names the full command path, so it can only be
// asserted through the registered tree.
func TestDaemonExportEventsRejectsPositionalArgs(t *testing.T) {
	_, _, err := runRootArgs(t, "daemon", "export-events", "events.json")
	if err == nil {
		t.Fatal("Execute error = nil; want positional argument error")
	}
	if got := err.Error(); !strings.Contains(got, `amx daemon export-events accepts no arguments, got "events.json"`) {
		t.Fatalf("Execute error = %q; want unknown positional argument error", got)
	}
}

func findCommandNamed(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
	}
	return nil
}
