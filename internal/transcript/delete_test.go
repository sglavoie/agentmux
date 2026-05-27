package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

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

func TestDelete_RemovesSearchSidecar(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "transcript.jsonl")
	writeTranscript(t, path, "{}\n")

	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-1")
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID: "sess-1", Harness: harness.Claude, Path: path,
	}))
	if err := idx.WriteSearchText(key, "needle body"); err != nil {
		t.Fatalf("WriteSearchText: %v", err)
	}

	if err := idx.Delete(key, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if exists {
		t.Fatalf("sidecar still exists after Delete")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transcript file removed despite alsoFile=false: %v", err)
	}
}
