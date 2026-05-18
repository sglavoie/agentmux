package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewFileLogger_DefaultDiscardsAndAvoidsCreatingFile pins the security
// behavior of the debug logger: when amx_DEBUG is unset, calling
// newFileLogger must not create or open the log file. Prior versions of this
// helper unconditionally opened /tmp/amx-debug.log with mode 0644, exposing
// session metadata (titles, directories, env) on shared hosts to any local
// user who could read /tmp.
func TestNewFileLogger_DefaultDiscardsAndAvoidsCreatingFile(t *testing.T) {
	t.Setenv("amx_DEBUG", "")
	t.Setenv("AMX_DEBUG", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-exist.log")

	logger := newFileLogger(path, "[test] ")
	logger.Printf("nothing should be written here")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("debug log file unexpectedly created at %s: stat err=%v", path, err)
	}
}

// TestNewFileLogger_EnabledWritesWithOwnerOnlyPerms verifies that when
// amx_DEBUG=1 the logger opens the requested file, and that the file is
// created with owner-only permissions so other local users cannot harvest
// session metadata from it.
func TestNewFileLogger_EnabledWritesWithOwnerOnlyPerms(t *testing.T) {
	t.Setenv("amx_DEBUG", "1")

	dir := t.TempDir()
	path := filepath.Join(dir, "amx-debug.log")

	logger := newFileLogger(path, "[test] ")
	logger.Printf("hello %s", "world")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("debug log mode = %v, want owner-only (no group/world bits)", mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatal("expected log entries written when amx_DEBUG=1")
	}
}

// TestDebugEnabled_AcceptedTruthyValues guards the truthy-value lexicon so a
// downstream refactor cannot silently shrink it.
func TestDebugEnabled_AcceptedTruthyValues(t *testing.T) {
	t.Setenv("AMX_DEBUG", "")
	for _, v := range []string{"1", "true", "yes", "on", " TRUE ", "On"} {
		t.Setenv("amx_DEBUG", v)
		if !debugEnabled() {
			t.Errorf("debugEnabled() = false for amx_DEBUG=%q, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "off", "no", "maybe"} {
		t.Setenv("amx_DEBUG", v)
		if debugEnabled() {
			t.Errorf("debugEnabled() = true for amx_DEBUG=%q, want false", v)
		}
	}
}
