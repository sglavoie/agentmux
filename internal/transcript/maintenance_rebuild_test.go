package transcript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"golang.org/x/sys/unix"
)

func TestRebuildDeletesOldSidecarsAndHonorsAllOption(t *testing.T) {
	t.Parallel()

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
	if result.Status.SidecarCount != 1 || result.Status.MissingSidecars != 0 || result.Status.OrphanSidecars != 0 {
		t.Fatalf("status after rebuild = sidecars %d missing %d orphans %d", result.Status.SidecarCount, result.Status.MissingSidecars, result.Status.OrphanSidecars)
	}
	if _, err := os.Stat(idx.SearchTextPath(staleKey)); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar still exists or stat failed: %v", err)
	}
}

func TestRebuildReparsesCachedTranscriptsBeforeResettingSidecars(t *testing.T) {
	t.Parallel()

	cfg, readers, transcriptPath := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}

	usedCachedEntry := false
	parseCount := 0
	reader := readers[harness.Claude].(*fakeReader)
	reader.discover = func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
		if cached, ok := opts.Cached(transcriptPath, info.Size(), info.ModTime()); ok {
			usedCachedEntry = true
			return []SessionMeta{cached}, nil
		}
		parseCount++
		return []SessionMeta{{
			ID: "session", Harness: harness.Claude, Path: transcriptPath,
			FileSize: info.Size(), FileModTime: info.ModTime(),
			MessageCount: 1, SearchText: "rebuilt needle",
		}}, nil
	}

	if _, err := Rebuild(path, cfg, readers, true); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if usedCachedEntry || parseCount != 1 {
		t.Fatalf("rebuild discovery used cached entry = %v, parse count = %d; want false/1", usedCachedEntry, parseCount)
	}
	if got, err := idx.ReadSearchText(IndexKey(harness.Claude, "session")); err != nil || got != "rebuilt needle" {
		t.Fatalf("rebuilt sidecar = %q, %v", got, err)
	}
}

func TestRebuildRepairsBlankCachedCurrentCodexTranscript(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	home := filepath.Join(tmp, "codex-home")
	sourcePath := filepath.Join(home, "sessions", "2026", "08", "25",
		"rollout-2026-08-25T13-18-17-01a03a5b-ea47-7ae3-b7f3-68f944d707b5.jsonl")
	fixturePath := filepath.Join(currentCodexFixtureRoot, "sessions", "2026", "08", "25",
		"rollout-2026-08-25T13-18-17-01a03a5b-ea47-7ae3-b7f3-68f944d707b5.jsonl")
	source, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile fixture: %v", err)
	}
	info := writeTranscript(t, sourcePath, string(source))
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile source before rebuild: %v", err)
	}

	cachePath := filepath.Join(tmp, "transcripts.json")
	key := IndexKey(harness.Codex, "01a03a5b-ea47-7ae3-b7f3-68f944d707b5")
	// Stamped with the current reader version, so only the rebuild — not
	// the once-per-rules reparse an older entry gets — may repair it.
	blank := SessionMeta{
		ID: "01a03a5b-ea47-7ae3-b7f3-68f944d707b5", Harness: harness.Codex,
		Path: sourcePath, FileSize: info.Size(), FileModTime: info.ModTime(),
		ReaderVersion: codexReaderVersion,
	}
	cache, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{key: blank}})
	if err != nil {
		t.Fatalf("Marshal cache: %v", err)
	}
	if err := os.WriteFile(cachePath, cache, 0o644); err != nil {
		t.Fatalf("WriteFile cache: %v", err)
	}

	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Codex, HomeDir: home,
	}}}
	readers := map[string]Reader{harness.Codex: NewCodexReader()}
	idx, err := LoadIndexAt(cachePath)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatalf("Refresh unchanged cache: %v", err)
	}
	if got, ok := idx.Get(key); !ok || got.MessageCount != 0 || got.Snippet != "" {
		t.Fatalf("ordinary refresh changed blank cached entry: %+v, found=%t", got, ok)
	}

	if _, err := Rebuild(cachePath, cfg, readers, false); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	rebuilt, err := LoadIndexAt(cachePath)
	if err != nil {
		t.Fatalf("LoadIndexAt rebuilt cache: %v", err)
	}
	meta, ok := rebuilt.Get(key)
	if !ok {
		t.Fatal("rebuilt Codex entry is missing")
	}
	if meta.MessageCount != 2 || meta.Snippet != "Review which previous features should be added back and restore the best ones." {
		t.Fatalf("rebuilt metadata = %+v", meta)
	}
	searchText, err := rebuilt.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if !strings.Contains(searchText, "review which previous features") || strings.Contains(searchText, "agents injected") {
		t.Fatalf("rebuilt search sidecar = %q", searchText)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile source after rebuild: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("rebuild modified the authoritative Codex JSONL")
	}
}

func TestRebuildRemovesSidecarsWhileHoldingAdvisoryLock(t *testing.T) {
	cfg, readers, _ := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}

	removedUnderLock := false
	originalRemove := removeTranscriptSearchStore
	removeTranscriptSearchStore = func(dir string) error {
		lockFile, openErr := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = lockFile.Close() }()
		lockErr := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if lockErr == nil {
			_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
			return errors.New("transcript search sidecars removed without advisory lock")
		}
		if !errors.Is(lockErr, unix.EWOULDBLOCK) && !errors.Is(lockErr, unix.EAGAIN) {
			return lockErr
		}
		removedUnderLock = true
		return os.RemoveAll(dir)
	}
	t.Cleanup(func() { removeTranscriptSearchStore = originalRemove })

	if _, err := Rebuild(path, cfg, readers, true); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !removedUnderLock {
		t.Fatal("Rebuild did not reset search sidecars")
	}
}

func TestRebuildMalformedMetadataQuarantinesAndRepairsInOneRun(t *testing.T) {
	t.Parallel()

	cfg, readers, _ := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	writeMalformedMetadata(t, path)

	result, err := Rebuild(path, cfg, readers, true)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	requireQuarantineWarning(t, result.Warnings)
	requireQuarantinedMetadata(t, path)
	if result.Status.VisibleEntries != 1 || result.Status.SidecarCount != 1 || result.Status.MissingSidecars != 0 {
		t.Fatalf("status after rebuild = %+v", result.Status)
	}
	if idx, err := loadIndexAtStrict(path); err != nil || idx == nil {
		t.Fatalf("strict load after rebuild = %#v, %v", idx, err)
	}
}

func writeMalformedMetadata(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatalf("WriteFile malformed metadata: %v", err)
	}
}

func requireQuarantineWarning(t *testing.T, warnings []string) {
	t.Helper()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "transcript cache quarantined as") {
		t.Fatalf("warnings = %q, want one quarantine warning", warnings)
	}
}

func requireQuarantinedMetadata(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatalf("Glob quarantine: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantine files = %v, want one", matches)
	}
}
