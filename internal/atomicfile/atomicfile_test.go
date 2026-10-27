package atomicfile

// What an authoritative file's rewrite must guarantee: the reader either sees
// the old contents or the new ones, the mode the operator chose survives, and a
// failed attempt leaves the directory exactly as it found it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesTheFileWithTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Write(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("contents = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// Missing parents are created, because the first pairing on a fresh machine
// writes a config into a directory nobody has made yet.
func TestWriteCreatesMissingParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "config.toml")
	if err := Write(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("stat: %v", err)
	}
}

// An operator who tightened config.toml to 0600 did so deliberately, and a
// rewrite that widened it back would be a quiet regression in a file that can
// name private paths.
func TestReplacePreservesAnExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("Replace() = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the 0600 the operator set", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("contents = %q", data)
	}
}

// A config.toml linked in from elsewhere keeps its link: the new contents land
// in the file it points at, with that file's mode, and no temp is left beside
// either of them.
func TestReplaceWritesThroughASymlinkAndKeepsIt(t *testing.T) {
	linkDir, targetDir := t.TempDir(), t.TempDir()
	target := filepath.Join(targetDir, "config.toml")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(linkDir, "config.toml")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := Replace(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("Replace() = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("path mode = %v, want the symlink kept", info.Mode())
	}
	if got, _ := os.Readlink(path); got != target {
		t.Errorf("link points at %q, want %q", got, target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("target contents = %q", data)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfo.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v, want the 0600 it had", targetInfo.Mode().Perm())
	}
	for _, dir := range []string{linkDir, targetDir} {
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s holds %d entries, want 1", dir, len(entries))
		}
	}
}

// The fallback applies only when there is nothing to preserve.
func TestReplaceUsesTheFallbackModeForANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Replace(path, []byte("new"), 0o640); err != nil {
		t.Fatalf("Replace() = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the fallback 0640", info.Mode().Perm())
	}
}

// A failed replacement must leave nothing behind. A stale temp beside an
// authoritative file is worse than the failure: the next operator to look finds
// two plausible configs and no way to tell which one is live.
func TestAFailedWriteLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be makes the rename fail after the temp
	// has already been written and synced, which is the interesting moment.
	path := filepath.Join(dir, "config.toml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("x"), 0o644); err == nil {
		t.Fatal("Write() = nil, want a rename failure")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("a temporary file survived the failure: %s", entry.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory = %v, want only the original entry", entries)
	}
}

// The replacement is one step: a reader never sees a truncated file, and the
// previous contents stay readable right up to the rename.
func TestTheOldContentsSurviveUntilTheReplacementLands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Opened before the write and read after it: the handle still refers to the
	// replaced inode, which is what makes the swap invisible to a reader mid-flight.
	before, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = before.Close() }()

	if err := Write(path, []byte("replacement"), 0o644); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	buf := make([]byte, len("original"))
	if _, err := before.Read(buf); err != nil {
		t.Fatalf("reading the pre-existing handle: %v", err)
	}
	if string(buf) != "original" {
		t.Errorf("an open reader observed %q; the swap was not atomic", buf)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "replacement" {
		t.Errorf("contents = %q", after)
	}
}

// Empty is a legitimate value, not a no-op.
func TestWritingEmptyContentTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("something"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, nil, 0o644); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("contents = %q, want empty", data)
	}
}
