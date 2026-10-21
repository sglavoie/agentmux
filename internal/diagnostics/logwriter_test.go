package diagnostics

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonLogWriterRetentionProtectsActiveBootstrapLog(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-30 * 24 * time.Hour)
	active, activePath, err := OpenBootstrapLog(paths, "active")
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestBootstrapLogDescriptorLockHelper$")
	helper.Env = append(os.Environ(), "AMX_TEST_BOOTSTRAP_LOCK_HELPER=1")
	helper.Stdout = active
	helper.Stderr = active
	helperInput, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helperInput.Close()
		if helper.Process != nil {
			_ = helper.Process.Kill()
		}
		_ = helper.Wait()
	})
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(paths.DaemonDir, "bootstrap-stale.log")
	if err := os.WriteFile(stalePath, []byte("stale\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{activePath, stalePath} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	options := DaemonLogOptions{KeepDays: 14, MaxBytes: 1 << 20, Now: func() time.Time { return now }}
	if _, err := NewDaemonLogWriter(paths, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active bootstrap log was removed: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("inactive bootstrap log survived retention: %v", err)
	}
	if err := helperInput.Close(); err != nil {
		t.Fatal(err)
	}
	if err := helper.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDaemonLogWriter(paths, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("closed bootstrap log survived retention: %v", err)
	}
}

func TestBootstrapLogDescriptorLockHelper(t *testing.T) {
	if os.Getenv("AMX_TEST_BOOTSTRAP_LOCK_HELPER") != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}
