package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func resetTranscriptsCommandTestState(t *testing.T, indexPath string) {
	t.Helper()
	oldIndexPath := transcriptsIndexPath
	oldLoadConfig := transcriptsLoadConfig
	oldRebuildAll := transcriptsRebuildAll
	oldStatusOut := transcriptsStatusCmd.OutOrStdout()
	oldCompactOut := transcriptsCompactCmd.OutOrStdout()
	oldRebuildOut := transcriptsRebuildCmd.OutOrStdout()

	transcriptsIndexPath = func() string { return indexPath }
	t.Cleanup(func() {
		transcriptsIndexPath = oldIndexPath
		transcriptsLoadConfig = oldLoadConfig
		transcriptsRebuildAll = oldRebuildAll
		transcriptsStatusCmd.SetOut(oldStatusOut)
		transcriptsCompactCmd.SetOut(oldCompactOut)
		transcriptsRebuildCmd.SetOut(oldRebuildOut)
	})
}

func TestTranscriptsStatusCommandPrintsStoreStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "s1")
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","message_count":1}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"metadata_path: " + path,
		"visible_entries: 1",
		"hidden_entries: 0",
		"missing_sidecars: 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestTranscriptsCompactCommandPrintsSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "legacy")
	body := `{"entries":{"` + key + `":{"id":"legacy","harness":"claude","message_count":1,"search_text":"body"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "compacted transcript index") ||
		!strings.Contains(got, "legacy_search_text_entries: 1") ||
		!strings.Contains(got, "sidecar_count: 1") {
		t.Fatalf("compact output did not include expected summary:\n%s", got)
	}
}
