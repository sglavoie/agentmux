package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestStatusReportsCountsAndMissingSidecars(t *testing.T) {
	idx := newTestIndex(t)
	idx.entries[IndexKey(harness.Claude, "visible-ok")] = SessionMeta{
		ID: "visible-ok", Harness: harness.Claude, MessageCount: 1,
	}
	idx.entries[IndexKey(harness.Codex, "visible-missing")] = SessionMeta{
		ID: "visible-missing", Harness: harness.Codex, MessageCount: 2,
	}
	idx.entries[IndexKey(harness.Copilot, "hidden")] = SessionMeta{
		ID: "hidden", Harness: harness.Copilot, MessageCount: 3, HiddenFromIndex: true,
	}
	if err := idx.WriteSearchText(IndexKey(harness.Claude, "visible-ok"), "body"); err != nil {
		t.Fatalf("WriteSearchText: %v", err)
	}
	if err := idx.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	status, err := Status(idx.Path())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.VisibleEntries != 2 || status.HiddenEntries != 1 {
		t.Fatalf("entry counts = visible %d hidden %d, want 2/1", status.VisibleEntries, status.HiddenEntries)
	}
	if status.SidecarCount != 1 || status.SidecarSize != int64(len("body")) {
		t.Fatalf("sidecar stats = count %d size %d, want 1/%d", status.SidecarCount, status.SidecarSize, len("body"))
	}
	if status.MissingSidecars != 1 || len(status.MissingSidecarKeys) != 1 ||
		status.MissingSidecarKeys[0] != IndexKey(harness.Codex, "visible-missing") {
		t.Fatalf("missing sidecars = %d %v", status.MissingSidecars, status.MissingSidecarKeys)
	}
}

func TestCompactMigratesLegacySearchTextAndRewritesMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	key := IndexKey(harness.Claude, "legacy")
	body, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{
		key: {
			ID: "legacy", Harness: harness.Claude, MessageCount: 1, SearchText: "legacy searchable body",
		},
	}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := Compact(path)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.LegacyFields != 1 {
		t.Fatalf("LegacyFields = %d, want 1", result.LegacyFields)
	}

	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(rewritten), "search_text") {
		t.Fatalf("compact left search_text in metadata: %s", rewritten)
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	searchText, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if searchText != "legacy searchable body" {
		t.Fatalf("search sidecar = %q", searchText)
	}
}

func TestRebuildDeletesOldSidecarsAndHonorsAllOption(t *testing.T) {
	tmp := t.TempDir()
	idx, err := LoadIndexAt(filepath.Join(tmp, "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	staleKey := IndexKey(harness.Copilot, "stale")
	if err := idx.WriteSearchText(staleKey, "stale"); err != nil {
		t.Fatalf("WriteSearchText stale: %v", err)
	}

	transcriptPath := filepath.Join(tmp, "home", "session.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	var gotMinTime time.Time
	reader := &fakeReader{harness: harness.Claude, discover: func(homeDir string, opts DiscoverOptions) ([]SessionMeta, error) {
		gotMinTime = opts.MinTime
		return []SessionMeta{{
			ID: "fresh", Harness: harness.Claude, Path: transcriptPath,
			FileSize: info.Size(), FileModTime: info.ModTime(),
			MessageCount: 1, SearchText: "fresh body",
		}}, nil
	}}
	cfg := &config.Config{
		Defaults: config.Defaults{SessionArchive: config.SessionArchiveConfig{ScanMaxAgeDays: 30}},
		Profiles: []config.Account{{
			Name: "work", Harness: harness.Claude, HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	result, err := Rebuild(idx.Path(), cfg, map[string]Reader{harness.Claude: reader}, true)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !gotMinTime.IsZero() {
		t.Fatalf("Rebuild --all MinTime = %v, want zero", gotMinTime)
	}
	if result.Status.SidecarCount != 1 || result.Status.MissingSidecars != 0 {
		t.Fatalf("status after rebuild = sidecars %d missing %d", result.Status.SidecarCount, result.Status.MissingSidecars)
	}
	if _, err := os.Stat(idx.SearchTextPath(staleKey)); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar still exists or stat failed: %v", err)
	}
}
