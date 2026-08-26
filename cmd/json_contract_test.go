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

// Worktree commands share the retained command envelope. Keep every JSON leaf
// explicit so adding or removing a worktree surface requires a contract audit.
func TestWorktreeJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		worktreeListCmd, worktreeShowCmd, worktreeReopenCmd,
		worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd,
		worktreeForgetCmd,
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

// `config asl-sync` is the first config leaf to declare --json, so the family
// gets its own audit entry rather than joining one of the others: it emits the
// shared schema-v1 envelope, and its failures use the common structured error
// envelope with codes that are themselves a consumer contract.
func TestConfigJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{configASLSyncCmd}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", JSONSchemaVersion)
	}
	// Each code names a distinct condition with a distinct remediation. Adding
	// one is additive; changing or removing one breaks a script that branches
	// on it.
	codes := []string{
		codeASLNotFound, codeASLProbeFailed, codeASLContractMismatch,
		codeASLStateRoot, codeASLActiveRuns, codeASLMigrationFailed,
		codeASLConfigAmbiguous, codeASLConfigWrite, codeASLDaemonRestart,
		codeASLVerifyFailed,
	}
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Error("an empty structured error code is not a contract")
		}
		if seen[code] {
			t.Errorf("duplicate error code %q", code)
		}
		seen[code] = true
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

	// Additive fields carried by existing schema versions (no bump required):
	// status/tail v2 health gained `earliest_observed_at`, the shared
	// `journal_snapshot` object gained `promoted_spool_files_read`, the
	// status v2 hooks report gained `delivery_failures` {count, latest_at}
	// beside the stable `hook_delivery_failures` warning finding, and the
	// bundle v2 `doctor.json` member gained the optional `daemon.asl`
	// capability block, which a daemon without the integration omits entirely.
	schemas := []struct {
		name          string
		version, want int
	}{
		{name: "status", version: debugger.StatusSchemaVersion, want: 2},
		{name: "tail", version: debugger.TailSchemaVersion, want: 2},
		{name: "probe", version: debugger.ProbeSchemaVersion, want: 1},
		{name: "explain", version: debugger.ExplainSchemaVersion, want: 2},
		{name: "capture", version: debugger.CaptureSchemaVersion, want: 1},
		{name: "bundle", version: debugger.BundleSchemaVersion, want: 2},
		{name: "replay", version: debugger.ReplaySchemaVersion, want: 1},
	}
	for _, schema := range schemas {
		if schema.version != schema.want {
			t.Errorf("unexpected debug %s JSON schema version %d; want %d", schema.name, schema.version, schema.want)
		}
	}
}
