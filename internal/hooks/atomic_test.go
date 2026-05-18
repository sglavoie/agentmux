package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// readFileOrFatal reads path and fails the test if it does not exist or
// cannot be read.
func readFileOrFatal(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestAtomicWriteFile_NoTempLeftOnSuccess verifies that after a successful
// atomicWriteFile call, no "<name>.tmp" sibling remains in the directory.
func TestAtomicWriteFile_NoTempLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := atomicWriteFile(path, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected temp file gone, stat err=%v", err)
	}
	if got := string(readFileOrFatal(t, path)); got != `{"hooks":{}}` {
		t.Fatalf("unexpected content: %q", got)
	}
}

// TestAtomicWriteFile_FaultDoesNotCorruptOriginal verifies that if the write
// step fails partway through, the existing settings.json is left untouched
// and the temp file does not linger.
func TestAtomicWriteFile_FaultDoesNotCorruptOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	original := []byte(`{"hooks":{"original":true}}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seeding original: %v", err)
	}

	// Inject a fault that simulates a crash mid-write: the file handle
	// receives some bytes, then a sentinel error.
	prev := atomicWriteFn
	t.Cleanup(func() { atomicWriteFn = prev })
	atomicWriteFn = func(f *os.File, data []byte) (int, error) {
		// Write a partial prefix so that a non-atomic implementation would
		// be observable as truncation of the destination file.
		half := len(data) / 2
		if _, err := f.Write(data[:half]); err != nil {
			return 0, err
		}
		return half, errInjectedFault
	}

	err := atomicWriteFile(path, []byte(`{"hooks":{"new":true}}`), 0o644)
	if !errors.Is(err, errInjectedFault) {
		t.Fatalf("expected injected-fault error, got: %v", err)
	}

	// Original must be intact.
	if got := readFileOrFatal(t, path); string(got) != string(original) {
		t.Fatalf("original corrupted: got %q want %q", got, original)
	}
	// Temp file must be cleaned up.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed after fault, stat err=%v", err)
	}
}

// TestMergeViaContract_PreservesBackup is a regression guard: the .bak file
// must continue to be written before the new settings.json replaces the
// original, even after the atomic-write refactor.
func TestMergeViaContract_PreservesBackup(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	// Seed with a non-empty settings.json that has no hooks; MergeAll will
	// rewrite it and must produce a .bak with the original bytes.
	original := []byte(`{"some":"prior-config"}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	bak, err := os.ReadFile(settingsPath + ".bak")
	if err != nil {
		t.Fatalf("expected .bak file, got: %v", err)
	}
	if string(bak) != string(original) {
		t.Fatalf(".bak content mismatch: got %q want %q", bak, original)
	}

	// And the .tmp must not be left behind.
	if _, err := os.Stat(settingsPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no leftover .tmp, stat err=%v", err)
	}
}

// TestMergeViaContract_FaultLeavesOriginalAndBackupIntact end-to-end check:
// when the atomic write fails mid-flight, the original settings.json and the
// freshly-created .bak must both retain their pre-merge content (note that
// in this code path .bak is written before the rename, so a corrupted main
// file could still be recovered from the backup).
func TestMergeViaContract_FaultLeavesOriginalAndBackupIntact(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")
	original := []byte(`{"hooks":{"prior":true}}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fault-inject the write step.
	prev := atomicWriteFn
	t.Cleanup(func() { atomicWriteFn = prev })
	atomicWriteFn = func(_ *os.File, _ []byte) (int, error) {
		return 0, errInjectedFault
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll returned error: %v", err)
	}
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected per-account error in results, got: %+v", results)
	}
	if !errors.Is(results[0].Err, errInjectedFault) {
		t.Fatalf("expected injected-fault error, got: %v", results[0].Err)
	}

	// The destination must still hold the original content untouched.
	if got := readFileOrFatal(t, settingsPath); string(got) != string(original) {
		t.Fatalf("original mutated: got %q want %q", got, original)
	}
	// The .bak must exist and match the original (recovery story preserved).
	bak, err := os.ReadFile(settingsPath + ".bak")
	if err != nil {
		t.Fatalf("expected .bak after merge failure, got: %v", err)
	}
	if string(bak) != string(original) {
		t.Fatalf(".bak content mismatch: got %q want %q", bak, original)
	}
	// No leftover .tmp.
	if _, err := os.Stat(settingsPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no leftover .tmp after fault, stat err=%v", err)
	}
}
