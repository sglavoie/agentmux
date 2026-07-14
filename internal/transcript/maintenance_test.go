package transcript

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"golang.org/x/sys/unix"
)

func TestStatusReportsCountsAndMissingSidecars(t *testing.T) {
	idx := newTestIndex(t)
	idx.entries[IndexKey(harness.Claude, "visible-ok")] = SessionMeta{
		ID: "visible-ok", Harness: harness.Claude, Profile: "work", Profiles: []string{"work", "personal"}, MessageCount: 1,
	}
	idx.entries[IndexKey(harness.Codex, "visible-missing")] = SessionMeta{
		ID: "visible-missing", Harness: harness.Codex, Profile: "legacy", MessageCount: 2,
	}
	idx.entries[IndexKey(harness.Copilot, "hidden")] = SessionMeta{
		ID: "hidden", Harness: harness.Copilot, Profile: "hidden", MessageCount: 3, HiddenFromIndex: true,
	}
	if err := idx.WriteSearchText(IndexKey(harness.Claude, "visible-ok"), "body"); err != nil {
		t.Fatalf("WriteSearchText: %v", err)
	}
	if err := idx.WriteSearchText(IndexKey(harness.Claude, "orphan"), "unused"); err != nil {
		t.Fatalf("WriteSearchText orphan: %v", err)
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
	if status.VisibleProfileMemberships != 3 || status.HiddenProfileMemberships != 1 || status.SharedProfileEntries != 1 {
		t.Fatalf("profile counts = visible memberships %d hidden memberships %d shared entries %d, want 3/1/1",
			status.VisibleProfileMemberships, status.HiddenProfileMemberships, status.SharedProfileEntries)
	}
	if status.SidecarCount != 2 || status.SidecarSize != int64(len("body")+len("unused")) {
		t.Fatalf("sidecar stats = count %d size %d, want 2/%d", status.SidecarCount, status.SidecarSize, len("body")+len("unused"))
	}
	if status.OrphanSidecars != 1 || status.OrphanSidecarSize != int64(len("unused")) {
		t.Fatalf("orphan stats = count %d size %d, want 1/%d", status.OrphanSidecars, status.OrphanSidecarSize, len("unused"))
	}
	if status.MissingSidecars != 1 || len(status.MissingSidecarKeys) != 1 ||
		status.MissingSidecarKeys[0] != IndexKey(harness.Codex, "visible-missing") {
		t.Fatalf("missing sidecars = %d %v", status.MissingSidecars, status.MissingSidecarKeys)
	}
}

func TestStatusMalformedMetadataQuarantinesAndReportsWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	writeMalformedMetadata(t, path)

	status, err := Status(path)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	requireQuarantineWarning(t, status.Warnings)
	if status.MetadataPath != path || status.VisibleEntries != 0 || status.HiddenEntries != 0 {
		t.Fatalf("status after recovery = %+v", status)
	}
	requireQuarantinedMetadata(t, path)
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

func TestCompactMalformedMetadataQuarantinesAndRepairsInOneRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	writeMalformedMetadata(t, path)

	result, err := Compact(path)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	requireQuarantineWarning(t, result.Warnings)
	requireQuarantinedMetadata(t, path)
	if result.LegacyFields != 0 || result.StatusAfter.MetadataSize == 0 {
		t.Fatalf("compact result after recovery = %+v", result)
	}
	if idx, err := loadIndexAtStrict(path); err != nil || idx == nil {
		t.Fatalf("strict load after compact = %#v, %v", idx, err)
	}
}

func TestCompactRemovesOnlyOrphanRegularSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	sourcePath := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("authoritative transcript"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	referencedKey := IndexKey(harness.Claude, "referenced")
	idx.entries[referencedKey] = SessionMeta{
		ID: "referenced", Harness: harness.Claude, Path: sourcePath, MessageCount: 1,
	}
	if err := idx.WriteSearchText(referencedKey, "referenced body"); err != nil {
		t.Fatal(err)
	}
	orphanKey := IndexKey(harness.Codex, "orphan")
	if err := idx.WriteSearchText(orphanKey, "orphan body"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	notesPath := filepath.Join(idx.SearchStoreDir(), "notes.md")
	if err := os.WriteFile(notesPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	temporaryPath := idx.SearchTextPath("temporary") + ".tmp"
	if err := os.WriteFile(temporaryPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	directoryPath := idx.SearchTextPath("directory")
	if err := os.Mkdir(directoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "symlink-target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkPath := idx.SearchTextPath("symlink")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Skipf("creating symlink: %v", err)
	}

	result, err := Compact(path)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.RemovedOrphanSidecars != 1 || result.RemovedOrphanSidecarSize != int64(len("orphan body")) {
		t.Fatalf("removed orphan stats = %d/%d, want 1/%d", result.RemovedOrphanSidecars, result.RemovedOrphanSidecarSize, len("orphan body"))
	}
	if result.StatusBefore.OrphanSidecars != 1 || result.StatusAfter.OrphanSidecars != 0 {
		t.Fatalf("orphan status before/after = %d/%d, want 1/0", result.StatusBefore.OrphanSidecars, result.StatusAfter.OrphanSidecars)
	}
	if result.StatusAfter.SidecarCount != 1 {
		t.Fatalf("sidecar count after = %d, want 1", result.StatusAfter.SidecarCount)
	}
	for _, preserved := range []string{idx.SearchTextPath(referencedKey), sourcePath, notesPath, temporaryPath, directoryPath, symlinkPath, target} {
		if _, err := os.Lstat(preserved); err != nil {
			t.Errorf("preserved artifact %s: %v", preserved, err)
		}
	}
	if _, err := os.Stat(idx.SearchTextPath(orphanKey)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan sidecar still exists or stat failed: %v", err)
	}
}

func TestCompactPrunesAfterPersistenceWhileHoldingAdvisoryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	key := IndexKey(harness.Claude, "legacy")
	body, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{
		key: {ID: "legacy", Harness: harness.Claude, MessageCount: 1, SearchText: "legacy body"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	idx := newFilesystemIndex(path)
	if err := idx.WriteSearchText(IndexKey(harness.Codex, "orphan"), "orphan"); err != nil {
		t.Fatal(err)
	}

	removedUnderLock := false
	metadataPersisted := false
	originalRemove := removeTranscriptSearchSidecar
	removeTranscriptSearchSidecar = func(sidecarPath string) error {
		lockFile, openErr := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = lockFile.Close() }()
		lockErr := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if lockErr == nil {
			_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
			return errors.New("transcript orphan sidecar removed without advisory lock")
		}
		if !errors.Is(lockErr, unix.EWOULDBLOCK) && !errors.Is(lockErr, unix.EAGAIN) {
			return lockErr
		}
		removedUnderLock = true
		metadata, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(metadata), "search_text") {
			return errors.New("orphan pruning ran before legacy metadata migration persisted")
		}
		metadataPersisted = true
		return os.Remove(sidecarPath)
	}
	t.Cleanup(func() { removeTranscriptSearchSidecar = originalRemove })

	result, err := Compact(path)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !removedUnderLock || !metadataPersisted {
		t.Fatalf("compact removal lock/persistence checks = %v/%v, want true/true", removedUnderLock, metadataPersisted)
	}
	if result.LegacyFields != 1 || result.RemovedOrphanSidecars != 1 {
		t.Fatalf("compact result = legacy %d removed %d, want 1/1", result.LegacyFields, result.RemovedOrphanSidecars)
	}
}

func TestCompactFailuresBeforePruningPreserveOrphans(t *testing.T) {
	t.Run("initial load", func(t *testing.T) {
		path, orphanPath := compactOrphanFixture(t)
		originalRead := readTranscriptIndexFile
		readTranscriptIndexFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
		t.Cleanup(func() { readTranscriptIndexFile = originalRead })

		if _, err := Compact(path); err == nil {
			t.Fatal("Compact succeeded after initial load failure")
		}
		if _, err := os.Stat(orphanPath); err != nil {
			t.Fatalf("orphan removed after initial load failure: %v", err)
		}
	})

	t.Run("locked reload", func(t *testing.T) {
		path, orphanPath := compactOrphanFixture(t)
		originalRead := readTranscriptIndexFile
		reads := 0
		readTranscriptIndexFile = func(readPath string) ([]byte, error) {
			reads++
			if reads == 2 {
				return nil, errors.New("reload failed")
			}
			return originalRead(readPath)
		}
		t.Cleanup(func() { readTranscriptIndexFile = originalRead })

		if _, err := Compact(path); err == nil {
			t.Fatal("Compact succeeded after locked reload failure")
		}
		if _, err := os.Stat(orphanPath); err != nil {
			t.Fatalf("orphan removed after locked reload failure: %v", err)
		}
	})

	t.Run("metadata write", func(t *testing.T) {
		path, orphanPath := compactOrphanFixture(t)
		originalWrite := writeTranscriptIndex
		writeTranscriptIndex = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
		t.Cleanup(func() { writeTranscriptIndex = originalWrite })

		if _, err := Compact(path); err == nil {
			t.Fatal("Compact succeeded after metadata write failure")
		}
		if _, err := os.Stat(orphanPath); err != nil {
			t.Fatalf("orphan removed after metadata write failure: %v", err)
		}
	})
}

func TestCompactPartialDeletionReturnsActualRemovalAndPreservesReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	referencedKey := IndexKey(harness.Claude, "referenced")
	idx.entries[referencedKey] = SessionMeta{ID: "referenced", Harness: harness.Claude, MessageCount: 1}
	if err := idx.WriteSearchText(referencedKey, "referenced"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	orphanKeys := []string{IndexKey(harness.Codex, "orphan-a"), IndexKey(harness.Copilot, "orphan-b")}
	for _, key := range orphanKeys {
		if err := idx.WriteSearchText(key, "orphan"); err != nil {
			t.Fatal(err)
		}
	}

	removeCalls := 0
	originalRemove := removeTranscriptSearchSidecar
	removeTranscriptSearchSidecar = func(sidecarPath string) error {
		removeCalls++
		if removeCalls == 2 {
			return errors.New("injected removal failure")
		}
		return os.Remove(sidecarPath)
	}
	t.Cleanup(func() { removeTranscriptSearchSidecar = originalRemove })

	result, err := Compact(path)
	if err == nil || !strings.Contains(err.Error(), "injected removal failure") {
		t.Fatalf("Compact error = %v, want injected removal failure", err)
	}
	if result.RemovedOrphanSidecars != 1 || result.RemovedOrphanSidecarSize != int64(len("orphan")) {
		t.Fatalf("partial removed stats = %d/%d, want 1/%d", result.RemovedOrphanSidecars, result.RemovedOrphanSidecarSize, len("orphan"))
	}
	if ok, err := idx.SearchTextExists(referencedKey); err != nil || !ok {
		t.Fatalf("referenced sidecar preserved = %v, %v", ok, err)
	}
	remaining := 0
	for _, key := range orphanKeys {
		if _, err := os.Stat(idx.SearchTextPath(key)); err == nil {
			remaining++
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if remaining != 1 {
		t.Fatalf("remaining orphan sidecars = %d, want 1", remaining)
	}
}

func compactOrphanFixture(t *testing.T) (path string, orphanPath string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	orphanKey := IndexKey(harness.Claude, "orphan")
	if err := idx.WriteSearchText(orphanKey, "orphan"); err != nil {
		t.Fatal(err)
	}
	return path, idx.SearchTextPath(orphanKey)
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
	if result.Status.SidecarCount != 1 || result.Status.MissingSidecars != 0 || result.Status.OrphanSidecars != 0 {
		t.Fatalf("status after rebuild = sidecars %d missing %d orphans %d", result.Status.SidecarCount, result.Status.MissingSidecars, result.Status.OrphanSidecars)
	}
	if _, err := os.Stat(idx.SearchTextPath(staleKey)); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar still exists or stat failed: %v", err)
	}
}

func TestRebuildReparsesCachedTranscriptsBeforeResettingSidecars(t *testing.T) {
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
