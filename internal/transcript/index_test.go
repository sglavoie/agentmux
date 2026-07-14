package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// fakeReader implements Reader without parsing any files on disk. It is
// driven by canned per-call output so the index tests can exercise the
// merge/refresh/delete logic without depending on the per-harness
// parsers (which have their own dedicated tests).
type fakeReader struct {
	harness     string
	discover    func(homeDir string, opts DiscoverOptions) ([]SessionMeta, error)
	readErr     error
	readResult  []Message
	discoverErr error
}

func (f *fakeReader) Harness() string { return f.harness }
func (f *fakeReader) Discover(homeDir string, opts DiscoverOptions) ([]SessionMeta, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	if f.discover != nil {
		return f.discover(homeDir, opts)
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

func TestSetHiddenRemovesSearchSidecar(t *testing.T) {
	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-1",
		Harness:      harness.Claude,
		MessageCount: 1,
		SearchText:   "needle body",
	}))
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists before SetHidden: %v", err)
	}
	if !exists {
		t.Fatalf("sidecar missing before SetHidden")
	}

	if err := idx.SetHidden(key, true); err != nil {
		t.Fatalf("SetHidden: %v", err)
	}

	got, ok := idx.Get(key)
	if !ok {
		t.Fatalf("entry missing after SetHidden")
	}
	if !got.HiddenFromIndex {
		t.Fatalf("HiddenFromIndex = false, want true")
	}
	if got.SearchText != "" {
		t.Fatalf("SearchText retained after SetHidden: %q", got.SearchText)
	}
	exists, err = idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists after SetHidden: %v", err)
	}
	if exists {
		t.Fatalf("sidecar still exists after SetHidden")
	}
}

// SetEntryForTest is exported via this same-package test helper so the
// test cases above can pre-populate the index without going through
// Refresh. Kept out of index.go (production code) on purpose.
func (i *Index) SetEntryForTest(key string, meta SessionMeta) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[key] = meta
	warning, persistErr := i.saveLocked()
	return errors.Join(warning, persistErr)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
