package searchstore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "index.json"))
}

func TestNew_DirDerivation(t *testing.T) {
	cases := []struct {
		name      string
		indexPath string
		wantDir   string
	}{
		{
			name:      "with extension",
			indexPath: "/tmp/foo/transcripts.json",
			wantDir:   "/tmp/foo/transcripts.search",
		},
		{
			name:      "no extension",
			indexPath: "/tmp/foo/transcripts",
			wantDir:   "/tmp/foo/transcripts.search",
		},
		{
			name:      "hidden file with no extension",
			indexPath: "/tmp/foo/.transcripts",
			wantDir:   "/tmp/foo/.transcripts.search",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.indexPath)
			if s.Dir() != tc.wantDir {
				t.Errorf("Dir = %q, want %q", s.Dir(), tc.wantDir)
			}
		})
	}
}

func TestPath_HexEncodedSha256(t *testing.T) {
	s := New("/tmp/foo/transcripts.json")
	key := "claude:abc123"
	sum := sha256.Sum256([]byte(key))
	want := filepath.Join("/tmp/foo/transcripts.search", hex.EncodeToString(sum[:])+".txt")
	if got := s.Path(key); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestWriteRead_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	const key = "claude:session-1"
	const text = "the quick brown fox\nüñîçødé line\n"

	if err := s.Write(key, text); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.Read(key)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != text {
		t.Errorf("Read = %q, want %q", got, text)
	}
}

func TestWrite_OverwritesAtomically(t *testing.T) {
	s := newTestStore(t)
	const key = "k"
	if err := s.Write(key, "first"); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := s.Write(key, "second"); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	got, err := s.Read(key)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "second" {
		t.Errorf("Read = %q, want %q", got, "second")
	}

	// No leftover .tmp file from the atomic write
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestExists_MissingReturnsFalseNil(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.Exists("never-written")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Errorf("Exists = true, want false")
	}
}

func TestExists_AfterWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("k", "v"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ok, err := s.Exists("k")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Errorf("Exists = false, want true")
	}
}

func TestDelete_IdempotentOnMissing(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete("never-written"); err != nil {
		t.Errorf("Delete on missing key: %v", err)
	}
}

func TestDelete_RemovesExisting(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("k", "v"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, err := s.Exists("k")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Errorf("Exists after Delete = true, want false")
	}
}

func TestStats_EmptyDirReturnsZero(t *testing.T) {
	s := newTestStore(t)
	count, size, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 0 || size != 0 {
		t.Errorf("Stats on missing dir = (%d, %d), want (0, 0)", count, size)
	}
}

func TestStats_CountsRegularTxtFilesOnly(t *testing.T) {
	s := newTestStore(t)
	// Two real sidecars
	if err := s.Write("a", "aaa"); err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if err := s.Write("b", "bbbbb"); err != nil {
		t.Fatalf("Write b: %v", err)
	}
	// A nested directory inside the store
	if err := os.MkdirAll(filepath.Join(s.Dir(), "subdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	// A non-.txt file
	if err := os.WriteFile(filepath.Join(s.Dir(), "notes.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	count, size, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if size != int64(len("aaa")+len("bbbbb")) {
		t.Errorf("size = %d, want %d", size, len("aaa")+len("bbbbb"))
	}
}
