package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func filesystemTranscriptFixture(t *testing.T) (*config.Config, map[string]Reader, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	path := filepath.Join(home, "projects", "fixture", "session.jsonl")
	info := writeTranscript(t, path, "{}\n")
	cfg := &config.Config{Profiles: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}
	reader := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID: "session", Harness: harness.Claude, Path: path,
				FileSize: info.Size(), FileModTime: info.ModTime(),
				MessageCount: 1, Snippet: "fixture", SearchText: "fixture needle",
			}}, nil
		},
		readResult: []Message{{Role: "user", Text: "fixture needle"}},
	}
	return cfg, map[string]Reader{harness.Claude: reader}, path
}

func TestCloneSessionEntriesDeepCopiesMemberships(t *testing.T) {
	t.Parallel()

	original := map[string]SessionMeta{
		"claude:shared": {
			Profiles: []string{"work", "personal"},
			Projects: []string{"SS", "streamutil"},
		},
	}
	cloned := cloneSessionEntries(original)
	meta := cloned["claude:shared"]
	meta.Profiles[0] = "changed-profile"
	meta.Projects[0] = "changed-project"
	cloned["claude:shared"] = meta

	if got := original["claude:shared"].Profiles[0]; got != "work" {
		t.Fatalf("original Profiles[0] = %q after clone mutation, want work", got)
	}
	if got := original["claude:shared"].Projects[0]; got != "SS" {
		t.Fatalf("original Projects[0] = %q after clone mutation, want SS", got)
	}
}

func TestFilesystemLoadStrictDiagnosticsAndMalformedRecovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatal(err)
	}

	if idx, err := loadIndexAtStrict(path); err == nil || idx != nil {
		t.Fatalf("strict load = %#v, %v; want nil decode failure", idx, err)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
		t.Fatalf("strict loader quarantined metadata: %v", matches)
	}

	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("recovering load = %#v, %v; want usable index plus warning", idx, warning)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 1 {
		t.Fatalf("quarantine files = %v, want one", matches)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed metadata still at original path: %v", err)
	}

	cfg, readers, _ := filesystemTranscriptFixture(t)
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatalf("Refresh after quarantine: %v", err)
	}
	if idx.Len() != 1 {
		t.Fatalf("rebuilt Len = %d, want 1", idx.Len())
	}
	if _, err := loadIndexAtStrict(path); err != nil {
		t.Fatalf("rebuilt metadata is not strict-loadable: %v", err)
	}
}

func TestFilesystemLoadDoesNotQuarantineValidMetadataOnSidecarFailure(t *testing.T) {
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
	sidecar := newFilesystemIndex(path).store.Path(key)
	if err := os.MkdirAll(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "block-removal"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("LoadIndexAt = %#v, %v; want metadata plus sidecar warning", idx, warning)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
		t.Fatalf("valid metadata was quarantined: %v", matches)
	}
	meta, ok := idx.Get(key)
	if !ok || meta.SearchText != "inline needle" {
		t.Fatalf("inline last-good search text = %+v, %v", meta, ok)
	}
	if err := os.RemoveAll(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(); err != nil {
		t.Fatalf("Save did not retry sidecar migration: %v", err)
	}
	if got, err := idx.ReadSearchText(key); err != nil || got != "inline needle" {
		t.Fatalf("repaired sidecar = %q, %v", got, err)
	}
}

func TestFilesystemLoadReadFailureDoesNotQuarantineValidMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	want, err := json.Marshal(indexFile{Entries: map[string]SessionMeta{
		IndexKey(harness.Claude, "valid"): {ID: "valid", Harness: harness.Claude},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}

	originalRead := readTranscriptIndexFile
	readTranscriptIndexFile = func(gotPath string) ([]byte, error) {
		data, readErr := os.ReadFile(gotPath)
		if readErr != nil {
			return nil, readErr
		}
		// A whole-file read can return bytes together with an I/O error when
		// the failure happens after reading has begun. The loader must honor
		// the read error instead of attempting to decode the partial bytes.
		return data[:len(data)/2], syscall.EIO
	}
	t.Cleanup(func() { readTranscriptIndexFile = originalRead })

	quarantineCalls := 0
	originalQuarantine := quarantineTranscriptIndex
	quarantineTranscriptIndex = func(string) (string, error) {
		quarantineCalls++
		return "", errors.New("unexpected quarantine")
	}
	t.Cleanup(func() { quarantineTranscriptIndex = originalQuarantine })

	idx, loadErr := LoadIndexAt(path)
	if idx == nil || !errors.Is(loadErr, syscall.EIO) {
		t.Fatalf("LoadIndexAt = %#v, %v; want usable empty index plus EIO", idx, loadErr)
	}
	var decodeErr *indexDecodeError
	if errors.As(loadErr, &decodeErr) {
		t.Fatalf("read error was classified as malformed JSON: %v", loadErr)
	}
	if quarantineCalls != 0 {
		t.Fatalf("quarantine calls = %d, want 0", quarantineCalls)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
		t.Fatalf("valid metadata was quarantined: %v", matches)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatalf("valid metadata changed after read failure\nwant: %s\ngot:  %s", want, after)
	}
}

func TestFilesystemRefreshReturnsSnapshotWhenQuarantineOrPersistenceFails(t *testing.T) {
	cfg, readers, _ := filesystemTranscriptFixture(t)
	path := filepath.Join(t.TempDir(), "transcripts.json")
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatal(err)
	}
	originalQuarantine := quarantineTranscriptIndex
	quarantineTranscriptIndex = func(string) (string, error) { return "", errors.New("quarantine denied") }
	t.Cleanup(func() { quarantineTranscriptIndex = originalQuarantine })

	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("LoadIndexAt = %#v, %v", idx, warning)
	}
	if err := idx.Refresh(cfg, readers); err == nil {
		t.Fatal("Refresh succeeded without quarantine warning")
	}
	if idx.Len() != 1 {
		t.Fatalf("rebuilt in-memory Len = %d, want 1", idx.Len())
	}
	// Atomic replacement can repair the original even when the best-effort
	// quarantine rename failed.
	if _, err := loadIndexAtStrict(path); err != nil {
		t.Fatalf("metadata was not repaired after quarantine failure: %v", err)
	}

	originalWrite := writeTranscriptIndex
	writeTranscriptIndex = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { writeTranscriptIndex = originalWrite })
	secondPath := filepath.Join(t.TempDir(), "transcripts.json")
	second, err := LoadIndexAt(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Refresh(cfg, readers); err == nil {
		t.Fatal("Refresh succeeded despite injected persistence failure")
	}
	if second.Len() != 1 {
		t.Fatalf("snapshot withheld after persistence failure: Len=%d", second.Len())
	}
	writeTranscriptIndex = originalWrite
	if err := second.Save(); err != nil {
		t.Fatalf("retrying dirty snapshot Save: %v", err)
	}
	if loaded, err := LoadIndexAt(secondPath); err != nil || loaded.Len() != 1 {
		t.Fatalf("saved retry = %#v, %v", loaded, err)
	}
}

func TestFilesystemRefreshKeepsPartialTranscriptDiscoveries(t *testing.T) {
	t.Parallel()

	cfg, readers, path := filesystemTranscriptFixture(t)
	reader := readers[harness.Claude].(*fakeReader)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reader.discover = func(string, DiscoverOptions) ([]SessionMeta, error) {
		return []SessionMeta{{
			ID: "partial", Harness: harness.Claude, Path: path,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
		}}, errors.New("one transcript could not be read")
	}
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err == nil {
		t.Fatal("Refresh did not surface partial discovery warning")
	}
	if _, ok := idx.Get(IndexKey(harness.Claude, "partial")); !ok {
		t.Fatal("useful discovery returned with an error was dropped")
	}
}

func TestFilesystemTranscriptSearchRepairsUnreadableSidecar(t *testing.T) {
	t.Parallel()

	cfg, readers, _ := filesystemTranscriptFixture(t)
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatal(err)
	}
	key := IndexKey(harness.Claude, "session")
	sidecar := idx.SearchTextPath(key)
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	results, err := Search(idx, readers, Query{Text: "needle"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %+v, %v", results, err)
	}
	if info, err := os.Stat(sidecar); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("sidecar repair mode = %v, %v", info, err)
	}
}
