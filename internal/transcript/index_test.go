package transcript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// fakeReader implements Reader without parsing any files on disk. It is
// driven by canned per-call output so the index tests can exercise the
// merge/refresh/delete logic without depending on the per-harness
// parsers (which have their own dedicated tests).
type fakeReader struct {
	harness     string
	discover    func(homeDir string) ([]SessionMeta, error)
	readErr     error
	readResult  []Message
	discoverErr error
}

func (f *fakeReader) Harness() string { return f.harness }
func (f *fakeReader) Discover(homeDir string) ([]SessionMeta, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	if f.discover != nil {
		return f.discover(homeDir)
	}
	return nil, nil
}
func (f *fakeReader) ReadMessages(string) ([]Message, error) {
	return f.readResult, f.readErr
}

// writeTranscript writes a non-empty file at path so that Refresh sees
// a real FileSize / FileModTime via os.Stat.
func writeTranscript(t *testing.T, path string, body string) os.FileInfo {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return info
}

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	idx, err := LoadIndexAt(filepath.Join(dir, "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	return idx
}

func TestLoadIndex_MissingFileReturnsEmpty(t *testing.T) {
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if idx.Len() != 0 {
		t.Errorf("expected empty index, got %d entries", idx.Len())
	}
}

func TestRefresh_AddsNewEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "claude-home", "projects", "-tmp-foo", "sess-1.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "claude-home"),
		}},
		Projects: []config.Session{{
			Title:     "Foo",
			Directory: "/tmp/foo",
		}},
	}

	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				CWD:          "/tmp/foo",
				StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 4,
				Snippet:      "hello",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if got.Profile != "work" {
		t.Errorf("Profile = %q, want %q", got.Profile, "work")
	}
	if got.Project != "Foo" {
		t.Errorf("Project = %q, want %q", got.Project, "Foo")
	}
	if got.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4", got.MessageCount)
	}
	if got.Snippet != "hello" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "hello")
	}
}

// TestRefresh_ExpandsTildeInHomeDir guards the integration between the
// config layer (which stores HomeDir verbatim, with a leading "~") and
// each Reader.Discover (which calls os.ReadDir on the raw value).
// Without tilde expansion in Refresh the per-harness readers silently
// return zero entries because "~/.claude" doesn't exist as a literal path.
func TestRefresh_ExpandsTildeInHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir available: %v", err)
	}
	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: "~/transcript-tilde-test-should-not-exist",
		}},
	}
	var got string
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string) ([]SessionMeta, error) {
			got = homeDir
			return nil, nil
		},
	}
	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := filepath.Join(home, "transcript-tilde-test-should-not-exist")
	if got != want {
		t.Errorf("Discover received homeDir = %q, want %q", got, want)
	}
}

func TestRefresh_SkipsUnchangedEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	// First discovery: rich metadata. The reader records call count so
	// we can assert it was invoked twice in total (once per Refresh).
	calls := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string) ([]SessionMeta, error) {
			calls++
			// On the second call, return identical FileSize/FileModTime
			// but blank MessageCount/Snippet/StartedAt/LastAt — the
			// index should refuse to clobber the cached values.
			if calls == 1 {
				return []SessionMeta{{
					ID:           "sess-1",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
					LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
					Path:         transcriptPath,
					FileSize:     info.Size(),
					FileModTime:  info.ModTime(),
					MessageCount: 4,
					Snippet:      "original",
				}}, nil
			}
			return []SessionMeta{{
				ID:          "sess-1",
				Harness:     harness.Claude,
				CWD:         "/tmp/foo",
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
				// Note: MessageCount/Snippet/StartedAt/LastAt all zero —
				// the unchanged-file path must keep the cached values.
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if !ok {
		t.Fatalf("entry missing after second Refresh")
	}
	if got.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4 (cached)", got.MessageCount)
	}
	if got.Snippet != "original" {
		t.Errorf("Snippet = %q, want %q (cached)", got.Snippet, "original")
	}
	if got.StartedAt.IsZero() {
		t.Errorf("StartedAt cleared; expected cached value to survive")
	}
}

func TestRefresh_RefreshesChangedEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	info1 := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	calls := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string) ([]SessionMeta, error) {
			calls++
			if calls == 1 {
				return []SessionMeta{{
					ID:           "sess-1",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					Path:         transcriptPath,
					FileSize:     info1.Size(),
					FileModTime:  info1.ModTime(),
					MessageCount: 2,
					Snippet:      "first",
				}}, nil
			}
			// File grew — index must adopt the new MessageCount/Snippet.
			info2, err := os.Stat(transcriptPath)
			if err != nil {
				return nil, err
			}
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				CWD:          "/tmp/foo",
				Path:         transcriptPath,
				FileSize:     info2.Size(),
				FileModTime:  info2.ModTime(),
				MessageCount: 7,
				Snippet:      "updated",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Mutate the file to bump FileSize/FileModTime.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(transcriptPath, []byte("{}\n{}\n{}\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	got, _ := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if got.MessageCount != 7 {
		t.Errorf("MessageCount = %d, want 7 (updated)", got.MessageCount)
	}
	if got.Snippet != "updated" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "updated")
	}
}

func TestRefresh_PreservesHiddenFromIndex(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-h.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID:          "sess-h",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-h")
	if err := idx.SetHidden(key, true); err != nil {
		t.Fatalf("SetHidden: %v", err)
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	got, _ := idx.Get(key)
	if !got.HiddenFromIndex {
		t.Errorf("HiddenFromIndex cleared by Refresh; expected to survive")
	}
}

func TestDelete_IndexOnlyLeavesFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "transcript.jsonl")
	writeTranscript(t, path, "{}\n")

	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID: "sess-1", Harness: harness.Claude, Path: path,
	}))

	if err := idx.Delete(key, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := idx.Get(key); ok {
		t.Errorf("entry still present after Delete")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("transcript file removed despite alsoFile=false: %v", err)
	}
}

func TestDelete_AlsoFileRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "transcript.jsonl")
	writeTranscript(t, path, "{}\n")

	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID: "sess-1", Harness: harness.Claude, Path: path,
	}))

	if err := idx.Delete(key, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after Delete(alsoFile=true): err=%v", err)
	}
}

func TestDelete_MissingFileIsNotAnError(t *testing.T) {
	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:      "sess-1",
		Harness: harness.Claude,
		Path:    filepath.Join(t.TempDir(), "already-gone.jsonl"),
	}))

	if err := idx.Delete(key, true); err != nil {
		t.Errorf("Delete with missing file returned error: %v", err)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-1",
		Harness:      harness.Claude,
		Profile:      "work",
		Project:      "Foo",
		Snippet:      "hello world",
		MessageCount: 3,
		StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}))
	if err := idx.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should exist and be valid JSON with the expected shape.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var stored indexFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode: %v\nfile: %s", err, data)
	}
	if _, ok := stored.Entries[key]; !ok {
		t.Errorf("saved file missing entry %s; got: %s", key, data)
	}

	loaded, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	got, ok := loaded.Get(key)
	if !ok {
		t.Fatalf("entry lost across save/load")
	}
	if got.Snippet != "hello world" {
		t.Errorf("Snippet = %q after reload", got.Snippet)
	}
}

func TestList_FiltersAndSort(t *testing.T) {
	idx := newTestIndex(t)

	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Profile: "work", Project: "Foo",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("codex:b", SessionMeta{
		ID: "b", Harness: harness.Codex, Profile: "work", Project: "Bar",
		LastAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:c", SessionMeta{
		ID: "c", Harness: harness.Claude, Profile: "home", Project: "Foo",
		LastAt:          time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		HiddenFromIndex: true,
	}))

	all := idx.List(ListFilter{})
	if len(all) != 2 {
		t.Errorf("default List len = %d, want 2 (hidden excluded)", len(all))
	}
	if len(all) > 0 && all[0].ID != "b" {
		t.Errorf("List not sorted by LastAt desc: first = %q", all[0].ID)
	}

	byHarness := idx.List(ListFilter{Harness: []string{harness.Claude}})
	if len(byHarness) != 1 || byHarness[0].ID != "a" {
		t.Errorf("Harness filter returned %+v", byHarness)
	}

	withHidden := idx.List(ListFilter{IncludeHidden: true})
	if len(withHidden) != 3 {
		t.Errorf("IncludeHidden=true len = %d, want 3", len(withHidden))
	}

	byProfile := idx.List(ListFilter{Profile: []string{"home"}, IncludeHidden: true})
	if len(byProfile) != 1 || byProfile[0].ID != "c" {
		t.Errorf("Profile filter returned %+v", byProfile)
	}
}

func TestRefresh_NoReaderForHarnessSkipped(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()},
			{Name: "extra", Harness: harness.Codex, HomeDir: t.TempDir()},
		},
	}
	claudeFR := &fakeReader{
		harness: harness.Claude,
		discover: func(string) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID: "x", Harness: harness.Claude,
			}}, nil
		},
	}
	idx := newTestIndex(t)
	// No codex reader supplied — that profile is silently skipped.
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: claudeFR}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok := idx.Get(IndexKey(harness.Claude, "x")); !ok {
		t.Errorf("claude entry missing after partial-reader Refresh")
	}
}

// SetEntryForTest is exported via this same-package test helper so the
// test cases above can pre-populate the index without going through
// Refresh. Kept out of index.go (production code) on purpose.
func (i *Index) SetEntryForTest(key string, meta SessionMeta) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[key] = meta
	return i.saveLocked()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
