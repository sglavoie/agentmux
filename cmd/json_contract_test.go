package cmd

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/spf13/cobra"
)

// Retained session commands expose the same local --json contract. Keeping the
// audit explicit prevents a new leaf command from silently printing an
// unversioned ad-hoc object.
func TestSessionJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		sessionListCmd, sessionContinueCmd, sessionForkCmd,
		sessionForkWorktreeCmd, sessionHandoffCmd,
	}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", JSONSchemaVersion)
	}
}

// Debug commands intentionally emit command-specific reports directly rather
// than wrapping them in the retained session command envelope. Audit every
// JSON-producing leaf and every report-family schema together so either kind
// of surface growth requires an explicit contract decision.
func TestDebugJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		debugStatusCmd, debugTailCmd, debugProbeCmd, debugExplainCmd,
		debugCaptureStartCmd, debugCaptureStopCmd, debugCaptureStatusCmd,
		debugBundleCmd, debugReplayCmd,
	}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}

	schemas := []struct {
		name    string
		version int
	}{
		{name: "status", version: debugger.StatusSchemaVersion},
		{name: "tail", version: debugger.TailSchemaVersion},
		{name: "probe", version: debugger.ProbeSchemaVersion},
		{name: "explain", version: debugger.ExplainSchemaVersion},
		{name: "capture", version: debugger.CaptureSchemaVersion},
		{name: "bundle", version: debugger.BundleSchemaVersion},
		{name: "replay", version: debugger.ReplaySchemaVersion},
	}
	for _, schema := range schemas {
		if schema.version != 1 {
			t.Errorf("unexpected debug %s JSON schema version %d", schema.name, schema.version)
		}
	}
}
