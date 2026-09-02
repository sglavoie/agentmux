package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func assertNoRetiredDatabaseState(t *testing.T, home, surface string) {
	t.Helper()
	dataDir := filepath.Join(home, ".local", "share", "agent-mux")
	for _, name := range []string{
		"agent-mux.db",
		"agent-mux.db-wal",
		"agent-mux.db-shm",
		"agent-mux.db.recovery.lock",
		"agent-mux.db.rebuild",
		"agent-mux.db.rebuild-startup.lock",
		"recovery",
	} {
		if _, err := os.Lstat(filepath.Join(dataDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired database artifact %s exists after %s: %v", name, surface, err)
		}
	}
}
