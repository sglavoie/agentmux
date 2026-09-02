package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestAcceptanceDoctorDoesNotCreateRetiredDatabaseState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	build := doctorTestBuild
	setUpDoctorFixture(t, ipc.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build,
	}, protocol.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("strict doctor: %v", err)
	}
	if !strings.Contains(out.String(), "overall: clean") {
		t.Fatalf("doctor output missing clean result:\n%s", out.String())
	}
	assertNoRetiredDatabaseState(t, home, "doctor")
}

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
