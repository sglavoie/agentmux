package transcripts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// Transcript maintenance commands share the retained command envelope. Keep
// every JSON leaf explicit so adding or removing one requires a contract audit.
func TestTranscriptsJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}

func setTranscriptsJSON(t *testing.T, command *cobra.Command) {
	t.Helper()
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Flags().Set("json", "false") })
}

func TestTranscriptsStatusJSONUsesTextKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "s1")
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","profile":"work","profiles":["work","personal"],"message_count":1}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	setTranscriptsJSON(t, transcriptsStatusCmd)

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var payload struct {
		SchemaVersion int             `json:"schema_version"`
		Data          storeStatusData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != 1 || payload.Data.MetadataPath != path || payload.Data.VisibleEntries != 1 ||
		payload.Data.VisibleProfileMemberships != 2 || payload.Data.MissingSidecars != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if warnings, ok := raw.Data["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want an empty array rather than null", raw.Data["warnings"])
	}
}

func TestTranscriptsCompactJSONReportsBeforeAndAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	if err := os.WriteFile(path, []byte(`{"entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setTranscriptsJSON(t, transcriptsCompactCmd)

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var payload struct {
		Data compactData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Data.Before.MetadataPath != path || payload.Data.After.MetadataPath != path {
		t.Fatalf("payload = %+v", payload.Data)
	}
}
