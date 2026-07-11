package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func resetDoctorTestState(t *testing.T) {
	t.Helper()
	oldClient := newDoctorDaemonClient
	oldKitty := newDoctorKitty
	oldConfig := doctorLoadConfig
	oldExecutable := doctorExecutable
	oldSocketPath := doctorSocketPath
	oldStat := doctorStat
	oldNow := doctorNow
	oldCLIBuild := doctorCLIBuild
	oldJSON := doctorJSON
	oldStrict := doctorStrict
	oldOut := doctorCmd.OutOrStdout()
	t.Cleanup(func() {
		newDoctorDaemonClient = oldClient
		newDoctorKitty = oldKitty
		doctorLoadConfig = oldConfig
		doctorExecutable = oldExecutable
		doctorSocketPath = oldSocketPath
		doctorStat = oldStat
		doctorNow = oldNow
		doctorCLIBuild = oldCLIBuild
		doctorJSON = oldJSON
		doctorStrict = oldStrict
		doctorCmd.SetOut(oldOut)
	})
	doctorJSON = false
	doctorStrict = false
}

// doctorTestBuild is a matching CLI/daemon artifact identity for tests that
// exercise a healthy deployment.
var doctorTestBuild = buildinfo.Info{Version: "1.2.3", Revision: "cafe0123", GoVersion: "go1.99", BinaryHash: "hash-a"}

func TestDoctorReportsDaemonRendererHookAndSuppressionHealth(t *testing.T) {
	resetDoctorTestState(t)
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	binary := filepath.Join(root, "amx")
	socket := filepath.Join(root, "daemon.sock")
	if err := os.WriteFile(binary, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binary, now.Add(-2*time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(socket, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	build := doctorTestBuild
	doctorCLIBuild = func() buildinfo.Info { return build }
	newDoctorDaemonClient = func() daemonExportClient {
		return &fakeDaemonExportClient{
			health: daemon.Health{Alive: true, PID: 4242, Uptime: 2 * time.Minute, Ready: true, Build: &build},
			export: func(int) (daemon.EventsExport, error) {
				return daemon.EventsExport{
					EventsByWindow: map[int][]daemon.SessionEvent{42: {{
						At: now.Unix(), Suppressed: true, SuppressionReason: "guard_blocked",
					}}},
					WindowMetadata: map[int]daemon.WindowDebugMetadata{42: {
						Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, KittyListenOn: "unix:/tmp/kitty-42"},
					}},
				}, nil
			},
		}
	}
	newDoctorKitty = func() kittyWindowLister {
		return fakeKittyWindowLister{windows: []kitty.OSWindow{{
			Tabs: []kitty.Tab{{
				Title: "⚡api",
				Windows: []kitty.Window{{
					ID:       42,
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
				}},
			}},
		}}}
	}
	doctorLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	doctorExecutable = func() (string, error) { return binary, nil }
	doctorSocketPath = func() string { return socket }
	doctorNow = func() time.Time { return now }

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"daemon: running (pid 4242, uptime 2m0s)",
		"socket: " + socket + " (current)",
		"kitty: ok (1 OS windows)",
		"kitty sockets: unix:/tmp/kitty-42",
		"hooks: no configured profiles",
		"renderer drift: none",
		"- guard_blocked: 1",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}
