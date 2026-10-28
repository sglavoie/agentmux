package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func setupExportOutputTest(t *testing.T, output string) {
	t.Helper()
	resetDaemonExportTestState(t)
	newDaemonExportClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{
			health: ipc.Health{Alive: true, PID: 1, Uptime: time.Second},
			export: func(int) (protocol.EventsExport, error) { return protocol.EventsExport{}, nil },
		}
	}
	newDaemonExportKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportNow = func() time.Time { return time.Unix(100, 0) }
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 0
	daemonExportEventsOutput = output
	daemonExportEventsPretty = false
	daemonExportEventsSchema = protocol.EventsExportSchemaVersion
}

func TestDaemonExportEventsOutputReplacesLooseFilePrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, []byte("old export"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	setupExportOutputTest(t, path)
	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export mode = %o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.SchemaVersion != protocol.EventsExportSchemaVersion {
		t.Fatalf("export = %q, decode error = %v", data, err)
	}
}

func TestDaemonExportEventsOutputRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "unrelated.json")
	if err := os.WriteFile(target, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.json")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink: %v", err)
	}
	setupExportOutputTest(t, path)
	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); !errors.Is(err, atomicfile.ErrNotRegular) {
		t.Fatalf("export error = %v, want symlink refusal", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "unrelated" {
		t.Fatalf("symlink target = %q, %v", got, err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output symlink changed: %v, %v", info, err)
	}
}
