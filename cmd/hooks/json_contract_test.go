package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
)

// hooks-sync --json reports each profile's result in the common envelope, and
// a refusal in the common error envelope.
func TestHooksSyncJSONContract(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "claude-home")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	t.Setenv("AMX_CONFIG", path)
	installed := filepath.Join(dir, "bin", "amx")
	oldBinary, oldLookPath, oldJSON, oldThis := hooksBinary, hooksLookPath, hooksSyncJSON, hooksSyncThisBinary
	oldOut := configHooksSyncCmd.OutOrStdout()
	t.Cleanup(func() {
		hooksBinary, hooksLookPath, hooksSyncJSON, hooksSyncThisBinary = oldBinary, oldLookPath, oldJSON, oldThis
		configHooksSyncCmd.SetOut(oldOut)
	})
	hooksBinary = func() (string, error) { return installed, nil }
	hooksLookPath = func(string) (string, error) { return installed, nil }
	hooksSyncJSON, hooksSyncThisBinary = true, false
	if configHooksSyncCmd.Flags().Lookup("json") == nil {
		t.Fatal("hooks-sync declares no --json")
	}

	var out bytes.Buffer
	configHooksSyncCmd.SetOut(&out)
	if err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil); err != nil {
		t.Fatalf("hooks-sync --json: %v\n%s", err, out.String())
	}
	var envelope struct {
		SchemaVersion int           `json:"schema_version"`
		Data          hooksSyncData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if envelope.SchemaVersion != cli.JSONSchemaVersion || envelope.Data.Binary != installed || len(envelope.Data.Profiles) != 1 ||
		envelope.Data.Profiles[0].Status != "created" || envelope.Data.Profiles[0].Profile != "work" {
		t.Fatalf("envelope = %+v", envelope)
	}

	// A build other than the installed amx is refused as structured data.
	hooksBinary = func() (string, error) { return filepath.Join(dir, "checkout", "amx"), nil }
	out.Reset()
	err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil)
	var failure cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal(out.Bytes(), &failure); decodeErr != nil || err == nil || failure.Error.Code != "foreign_binary" {
		t.Fatalf("refusal = %v, %+v (%v)\n%s", err, failure, decodeErr, out.String())
	}
}
