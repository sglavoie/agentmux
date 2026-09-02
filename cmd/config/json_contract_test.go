package config

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

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
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
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
