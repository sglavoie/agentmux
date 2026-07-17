package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hooks"
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
	oldDiagnosticPaths := doctorDiagnosticPaths
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
		doctorDiagnosticPaths = oldDiagnosticPaths
		doctorJSON = oldJSON
		doctorStrict = oldStrict
		doctorCmd.SetOut(oldOut)
	})
	doctorJSON = false
	doctorStrict = false
	diagnosticRoot := filepath.Join(t.TempDir(), "diagnostics")
	doctorDiagnosticPaths = func() (diagnostics.Paths, error) {
		return diagnostics.PathsForRoot(diagnosticRoot), nil
	}
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

func TestDoctorRendererTreatsSharedTabAsConvergedWhenAnyPaneMatches(t *testing.T) {
	snapshot := daemon.EventsExport{WindowMetadata: map[int]daemon.WindowDebugMetadata{
		42: {Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, Looping: false, LoopingSet: true}},
		43: {Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, Looping: true, LoopingSet: true}},
	}}
	liveByID := map[int]daemon.WindowLiveMetadata{
		42: {Found: true, OSWindowID: 1, TabID: 7, IconState: "bolt", Looping: false},
		43: {Found: true, OSWindowID: 1, TabID: 7, IconState: "bolt", Looping: false},
	}

	var out bytes.Buffer
	var failures []string
	if err := writeDoctorRenderer(&out, snapshot, liveByID, true, func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "renderer drift: none") {
		t.Fatalf("shared-tab renderer output = %q, want no drift", out.String())
	}
	if len(failures) != 0 {
		t.Fatalf("shared-tab renderer failures = %v, want none", failures)
	}
}

func TestDoctorReportsVerifiedDisabledHooksAsFailure(t *testing.T) {
	resetDoctorTestState(t)
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), "amx")
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home,
	}}}
	if _, err := hooks.MergeAll(cfg, "", binary); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	doctorLoadConfig = func() (*config.Config, error) { return cfg, nil }
	doctorExecutable = func() (string, error) { return binary, nil }

	var out bytes.Buffer
	var failures []string
	err := writeDoctorHooks(&out, func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("writeDoctorHooks: %v", err)
	}
	if !strings.Contains(out.String(), "activation=failure") {
		t.Fatalf("doctor hook output did not report activation failure:\n%s", out.String())
	}
	if !slices.ContainsFunc(failures, func(message string) bool {
		return strings.Contains(message, "disableAllHooks=true")
	}) {
		t.Fatalf("doctor failures = %v, want disableAllHooks evidence", failures)
	}
}

func TestDoctorHooksSummarizesDroppedDeliveries(t *testing.T) {
	resetDoctorTestState(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A failed attempt that no retry resolves terminalizes into one dropped
	// delivery: the hook.failure fallback plus the terminal hook.delivery share
	// a request and must count exactly once.
	requestID := "11111111-1111-4111-8111-111111111111"
	attempt := diagnostics.NewEnvelope("hook.ipc_attempt")
	attempt.Harness = harness.Claude
	attempt.RequestID = requestID
	attempt.Outcome = "error"
	attempt.Reason = "daemon_unavailable"
	if err := journal.Append(t.Context(), attempt); err != nil {
		t.Fatal(err)
	}
	failure := diagnostics.NewEnvelope("hook.failure")
	failure.Harness = harness.Claude
	failure.RequestID = requestID
	failure.Outcome = "error"
	failure.Reason = "daemon_unavailable"
	if err := journal.Append(t.Context(), failure); err != nil {
		t.Fatal(err)
	}
	delivery := diagnostics.NewEnvelope("hook.delivery")
	delivery.Harness = harness.Claude
	delivery.RequestID = requestID
	delivery.Outcome = "retryable_failure"
	delivery.Reason = "daemon_unavailable"
	if err := journal.Append(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	doctorLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	doctorExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "amx"), nil }

	var out bytes.Buffer
	var warnings []string
	err = writeDoctorHooks(&out, func(format string, args ...any) {
		t.Errorf("unexpected failure finding: %s", fmt.Sprintf(format, args...))
	}, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("writeDoctorHooks: %v", err)
	}
	if !strings.Contains(out.String(), "hooks: 1 delivery failure(s) recorded in the trailing hour") {
		t.Fatalf("doctor hook output missing delivery-failure summary:\n%s", out.String())
	}
	if !slices.ContainsFunc(warnings, func(message string) bool {
		return strings.Contains(message, "deliveries during daemon restarts are dropped")
	}) {
		t.Fatalf("doctor warnings = %v, want dropped-delivery note", warnings)
	}
}

func TestDoctorReportsNativeCoverageAndDefersActivePruning(t *testing.T) {
	resetDoctorTestState(t)
	enabled := true
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		NativeLogging: &enabled, NativeKeepDays: 14, NativeMaxMiB: 1, NativeSessionMaxMiB: 1,
	}}}
	doctorLoadConfig = func() (*config.Config, error) { return cfg, nil }
	paths, err := doctorDiagnosticPaths()
	if err != nil {
		t.Fatal(err)
	}
	launchID := "88888888-8888-4888-8888-888888888888"
	dir, err := diagnostics.EnsureNativeLaunch(paths, launchID)
	if err != nil {
		t.Fatal(err)
	}
	activeLog := filepath.Join(dir, "debug.log")
	if err := os.WriteFile(activeLog, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{Windows: []kitty.Window{{UserVars: kitty.UserVars{Profile: "managed", LaunchID: launchID, NativeCoverage: "managed"}}}},
		{Windows: []kitty.Window{{UserVars: kitty.UserVars{Profile: "external", LaunchID: "99999999-9999-4999-8999-999999999999", NativeCoverage: "external"}}}},
		{Windows: []kitty.Window{{UserVars: kitty.UserVars{Profile: "degraded", LaunchID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", NativeCoverage: "degraded"}}}},
		{Windows: []kitty.Window{{ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"/usr/local/bin/codex"}}}}}},
	}}}
	var out bytes.Buffer
	var warnings []string
	if err := writeDoctorNativeLogs(&out, windows, true, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"native privacy: raw local logs",
		"native coverage: managed=1 external=1 degraded=1 disabled=0 unmanaged=1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("native doctor output missing %q:\n%s", want, out.String())
		}
	}
	if !slices.ContainsFunc(warnings, func(value string) bool { return strings.Contains(value, "pruning is deferred until exit") }) ||
		!slices.ContainsFunc(warnings, func(value string) bool { return strings.Contains(value, "coverage cannot be guaranteed") }) {
		t.Fatalf("warnings = %v", warnings)
	}
	if info, err := os.Stat(activeLog); err != nil || info.Size() != 2<<20 {
		t.Fatalf("active log was mutated: info=%v err=%v", info, err)
	}
}
