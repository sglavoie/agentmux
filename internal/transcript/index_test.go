package transcript

import (
	"os"
	"path/filepath"
	"testing"
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
