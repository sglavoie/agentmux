package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestFilesystemTranscriptConcurrentLoadRefreshDelete(t *testing.T) {
	t.Parallel()

	cfg, readers, _ := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	seed, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 12; iteration++ {
				idx, loadErr := LoadIndexAt(path)
				if loadErr != nil {
					t.Errorf("worker %d load: %v", worker, loadErr)
					return
				}
				if (worker+iteration)%3 == 0 {
					_ = idx.Delete(IndexKey(harness.Claude, "session"), false)
				} else if refreshErr := idx.Refresh(cfg, readers); refreshErr != nil {
					t.Errorf("worker %d refresh: %v", worker, refreshErr)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("concurrent metadata is malformed: %s", data)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("shared advisory lock file missing: %v", err)
	}
}

func TestFilesystemTranscriptDeleteSidecarFailureKeepsSourceAndCleanState(t *testing.T) {
	t.Parallel()

	cfg, readers, sourcePath := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	key := IndexKey(harness.Claude, "session")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := idx.SearchTextPath(key)
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "block-removal"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := idx.Delete(key, true); err == nil {
		t.Fatal("Delete succeeded despite sidecar removal failure")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source transcript was removed before sidecar failure: %v", err)
	}
	if _, ok := idx.Get(key); !ok {
		t.Fatal("index entry was removed after sidecar failure")
	}
	idx.mu.Lock()
	dirty := idx.filesystemDirty
	idx.mu.Unlock()
	if dirty {
		t.Fatal("sidecar failure changed a clean index to dirty")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("sidecar failure changed metadata\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestFilesystemTranscriptDeleteReportsSourceRemovedWhenPersistFails(t *testing.T) {
	cfg, readers, sourcePath := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	key := IndexKey(harness.Claude, "session")

	persistFailure := errors.New("disk full")
	originalWrite := writeTranscriptIndex
	writeTranscriptIndex = func(string, []byte, os.FileMode) error { return persistFailure }
	t.Cleanup(func() { writeTranscriptIndex = originalWrite })

	err = idx.Delete(key, true)
	var partial *SourceDeletedError
	if !errors.As(err, &partial) {
		t.Fatalf("Delete error = %v, want SourceDeletedError", err)
	}
	if partial.Path != sourcePath || !errors.Is(partial, persistFailure) {
		t.Fatalf("partial delete = %+v, want path %q wrapping persistence failure", partial, sourcePath)
	}
	if _, statErr := os.Stat(sourcePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source file still exists after partial delete: %v", statErr)
	}
	if _, ok := idx.Get(key); ok {
		t.Fatal("in-memory entry survived partial delete")
	}
	if loaded, loadErr := LoadIndexAt(path); loadErr != nil {
		t.Fatal(loadErr)
	} else if _, ok := loaded.Get(key); !ok {
		t.Fatal("on-disk cache unexpectedly changed despite injected persistence failure")
	}

	writeTranscriptIndex = originalWrite
	if err := idx.Save(); err != nil {
		t.Fatalf("retrying partial delete persistence: %v", err)
	}
	if loaded, loadErr := LoadIndexAt(path); loadErr != nil {
		t.Fatal(loadErr)
	} else if _, ok := loaded.Get(key); ok {
		t.Fatal("retry did not remove stale on-disk cache entry")
	}
}

func TestFilesystemSaveRetainsInlineSearchWhenSidecarMigrationFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	key := IndexKey(harness.Claude, "legacy")
	data, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{
		key: {ID: "legacy", Harness: harness.Claude, SearchText: "inline needle"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	searchDir := newFilesystemIndex(path).SearchStoreDir()
	if err := os.WriteFile(searchDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("LoadIndexAt = %#v, %v; want usable index plus migration warning", idx, warning)
	}
	idx.mu.Lock()
	meta := idx.entries[key]
	meta.HiddenFromIndex = true
	idx.entries[key] = meta
	idx.filesystemDirty = true
	idx.mu.Unlock()

	if err := idx.Save(); err == nil {
		t.Fatal("Save hid sidecar migration warning")
	}
	written, err := loadRawTranscriptIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	got := written.Entries[key]
	if !got.HiddenFromIndex || got.SearchText != "inline needle" {
		t.Fatalf("persisted legacy metadata = %+v, want hidden flag plus retained inline text", got)
	}

	if err := os.Remove(searchDir); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(); err != nil {
		t.Fatalf("Save after sidecar repair: %v", err)
	}
	written, err = loadRawTranscriptIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := written.Entries[key]; !got.HiddenFromIndex || got.SearchText != "" {
		t.Fatalf("metadata after migration retry = %+v", got)
	}
	if got, err := idx.ReadSearchText(key); err != nil || got != "inline needle" {
		t.Fatalf("migrated sidecar = %q, %v", got, err)
	}
}

func loadRawTranscriptIndex(path string) (indexFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return indexFile{}, err
	}
	var decoded indexFile
	err = json.Unmarshal(data, &decoded)
	return decoded, err
}

func TestFilesystemTranscriptAtomicReplacementLeavesNoTemporaryFiles(t *testing.T) {
	t.Parallel()

	cfg, readers, _ := filesystemTranscriptFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 3; iteration++ {
		if err := idx.Refresh(cfg, readers); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if matched, _ := filepath.Match(".transcripts.json.*.tmp", entry.Name()); matched {
			t.Fatalf("atomic replacement left temporary file %s", entry.Name())
		}
	}
}

func TestFilesystemTranscriptPruneReloadsLatestSnapshot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	idx.mu.Lock()
	idx.entries["claude:old"] = SessionMeta{ID: "old", Harness: harness.Claude, LastAt: time.Now().Add(-48 * time.Hour)}
	idx.filesystemDirty = true
	idx.mu.Unlock()
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	other, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 1
	if hidden, err := Prune(other, cfg, time.Now()); err != nil || hidden != 1 {
		t.Fatalf("Prune = %d, %v", hidden, err)
	}
	if meta, ok := other.Get("claude:old"); !ok || !meta.HiddenFromIndex {
		t.Fatalf("pruned metadata = %+v, %v", meta, ok)
	}
}

func TestFilesystemTranscriptUnreadableCacheAbortsMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Index, *config.Config, map[string]Reader, string) error
	}{
		{name: "Save", mutate: func(idx *Index, _ *config.Config, _ map[string]Reader, _ string) error {
			return idx.Save()
		}},
		{name: "Delete", mutate: func(idx *Index, _ *config.Config, _ map[string]Reader, key string) error {
			return idx.Delete(key, false)
		}},
		{name: "SetHidden", mutate: func(idx *Index, _ *config.Config, _ map[string]Reader, key string) error {
			return idx.SetHidden(key, true)
		}},
		{name: "Prune", mutate: func(idx *Index, cfg *config.Config, _ map[string]Reader, _ string) error {
			_, err := Prune(idx, cfg, time.Now())
			return err
		}},
		{name: "Refresh", mutate: func(idx *Index, cfg *config.Config, readers map[string]Reader, _ string) error {
			return idx.Refresh(cfg, readers)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, cfg, readers, key, path, before, restore := staleUnreadableTranscriptCache(t)
			if err := tt.mutate(idx, cfg, readers, key); err == nil {
				t.Fatalf("%s succeeded with an unreadable cache", tt.name)
			}
			restore()
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("%s rewrote an unreadable cache\nbefore: %s\nafter:  %s", tt.name, before, after)
			}
			exists, err := idx.SearchTextExists(key)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("%s removed a search sidecar before aborting", tt.name)
			}
		})
	}
}

func staleUnreadableTranscriptCache(t *testing.T) (*Index, *config.Config, map[string]Reader, string, string, []byte, func()) {
	t.Helper()
	cfg, readers, _ := filesystemTranscriptFixture(t)
	cfg.Defaults.SessionArchive.KeepDays = 1
	path := filepath.Join(t.TempDir(), "transcripts.json")
	seed, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	key := IndexKey(harness.Claude, "session")
	seed.mu.Lock()
	old := seed.entries[key]
	old.LastAt = time.Now().Add(-48 * time.Hour)
	seed.entries[key] = old
	seed.filesystemDirty = true
	seed.mu.Unlock()
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	stale, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	newer.mu.Lock()
	newer.entries[IndexKey(harness.Claude, "concurrent")] = SessionMeta{
		ID: "concurrent", Harness: harness.Claude, MessageCount: 1,
	}
	newer.filesystemDirty = true
	newer.mu.Unlock()
	if err := newer.Save(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if err := os.Chmod(path, 0o644); err != nil {
			t.Errorf("restoring cache permissions: %v", err)
		}
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	if file, openErr := os.Open(path); openErr == nil {
		_ = file.Close()
		t.Skip("file permissions do not make the cache unreadable for this user")
	}
	return stale, cfg, readers, key, path, before, restore
}
