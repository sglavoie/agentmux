package debug

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/debugger"
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
	oldDoctorLookPath := doctorLookPath
	oldSocketPath := doctorSocketPath
	oldStat := doctorStat
	oldNow := doctorNow
	oldCLIBuild := doctorCLIBuild
	oldDiagnosticPaths := doctorDiagnosticPaths
	oldConfigModTime := doctorConfigModTime
	oldJSON := doctorJSON
	oldStrict := doctorStrict
	oldVerbose := doctorVerbose
	oldLookPath := aslDoctorLookPath
	oldASLStat := aslDoctorStat
	oldBridgeFailure := doctorLastASLBridgeFailure
	oldOut := doctorCmd.OutOrStdout()
	t.Cleanup(func() {
		aslDoctorLookPath = oldLookPath
		aslDoctorStat = oldASLStat
		doctorLastASLBridgeFailure = oldBridgeFailure
		newDoctorDaemonClient = oldClient
		newDoctorKitty = oldKitty
		doctorLoadConfig = oldConfig
		doctorExecutable = oldExecutable
		doctorLookPath = oldDoctorLookPath
		doctorSocketPath = oldSocketPath
		doctorStat = oldStat
		doctorNow = oldNow
		doctorCLIBuild = oldCLIBuild
		doctorDiagnosticPaths = oldDiagnosticPaths
		doctorConfigModTime = oldConfigModTime
		doctorJSON = oldJSON
		doctorStrict = oldStrict
		doctorVerbose = oldVerbose
		doctorCmd.SetOut(oldOut)
	})
	doctorJSON = false
	doctorStrict = false
	doctorVerbose = false
	// The operator's real config.toml must not decide whether a test sees the
	// config-freshness warning; tests that exercise it install their own.
	doctorConfigModTime = func() (time.Time, error) { return time.Time{}, os.ErrNotExist }
	// The ASL integration section reads the environment doctor was started in.
	// Pinned here so a machine that happens to have `asl` installed and one
	// that does not produce the same report.
	aslDoctorLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	aslDoctorStat = func(string) error { return os.ErrNotExist }
	// Likewise the amx on PATH: an installed amx must not make every test
	// binary read as a foreign one.
	doctorLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	diagnosticRoot := filepath.Join(t.TempDir(), "diagnostics")
	doctorDiagnosticPaths = func() (diagnostics.Paths, error) {
		return diagnostics.PathsForRoot(diagnosticRoot), nil
	}
}

// doctorTestBuild is a matching CLI/daemon artifact identity for tests that
// exercise a healthy deployment.
var doctorTestBuild = buildinfo.Info{Version: "1.2.3", Revision: "cafe0123", GoVersion: "go1.99", BinaryHash: "hash-a"}
var doctorTestNotification = protocol.NotificationCapability{
	Enabled: true, Available: true, Interactive: true, Backend: "alerter",
	Executable: "/opt/homebrew/bin/alerter", Version: "26.5",
}

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
	newDoctorDaemonClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{
			health: ipc.Health{Alive: true, PID: 4242, Uptime: 2 * time.Minute, Ready: true, Build: &build, Notification: &doctorTestNotification},
			export: func(int) (protocol.EventsExport, error) {
				return protocol.EventsExport{
					EventsByWindow: map[int][]protocol.SessionEvent{42: {{
						At: now.Unix(), Suppressed: true, SuppressionReason: "guard_blocked",
					}}},
					WindowMetadata: map[int]protocol.WindowDebugMetadata{42: {
						Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, KittyListenOn: "unix:/tmp/kitty-42"},
					}},
				}, nil
			},
		}
	}
	newDoctorKitty = func() cli.KittyWindowLister {
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
		"kitty: ok (1 OS window)",
		"kitty sockets: unix:/tmp/kitty-42",
		"hooks: no configured profiles",
		"renderer drift: none",
		"recent suppressions (last 15m0s): 1, all guard_blocked",
		"overall: ok (1 warning)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- guard_blocked: 1") {
		t.Errorf("doctor output lists suppression reasons without --verbose:\n%s", got)
	}

	doctorVerbose = true
	out.Reset()
	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor --verbose: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "recent suppressions (last 15m0s):\n- guard_blocked: 1\n") {
		t.Errorf("doctor --verbose output missing the suppression breakdown:\n%s", got)
	}
}

func TestSuppressionSummaryNamesAMajorityReasonAndPointsAtVerbose(t *testing.T) {
	for _, tc := range []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{"guard_blocked": 3}, "3, all guard_blocked"},
		{map[string]int{"ignored_event": 159, "terminal_stop_held": 14, "unmanaged_pipe_source": 3}, "176 across 3 reasons, mostly ignored_event (--verbose lists each)"},
		{map[string]int{"a": 2, "b": 2}, "4 across 2 reasons (--verbose lists each)"},
	} {
		reasons := make([]string, 0, len(tc.counts))
		total := 0
		for reason, count := range tc.counts {
			reasons = append(reasons, reason)
			total += count
		}
		sort.Strings(reasons)
		if got := suppressionSummary(reasons, tc.counts, total); got != tc.want {
			t.Errorf("suppressionSummary(%v) = %q, want %q", tc.counts, got, tc.want)
		}
	}
}

func TestDoctorRendererTreatsSharedTabAsConvergedWhenAnyPaneMatches(t *testing.T) {
	snapshot := protocol.EventsExport{WindowMetadata: map[int]protocol.WindowDebugMetadata{
		42: {Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, Looping: false, LoopingSet: true}},
		43: {Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true, Looping: true, LoopingSet: true}},
	}}
	liveByID := map[int]protocol.WindowLiveMetadata{
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
	err := writeDoctorHooks(&out, false, time.Time{}, func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}, func(string, ...any) {}, func(string, ...any) {}, func(string, string, ...any) {})
	if err != nil {
		t.Fatalf("writeDoctorHooks: %v", err)
	}
	if !strings.Contains(out.String(), "not active") {
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

	// The same single drop reads as a warning while the running daemon was
	// up to receive it, and as a note once it predates the daemon's start:
	// the restart gap the prompt-free hook client accepts by design.
	failedAt := time.Now()
	for _, tc := range []struct {
		name      string
		startedAt time.Time
		severity  string
		line      string
	}{
		{"during uptime", failedAt.Add(-time.Hour), severityWarning, "hooks: 1 hook delivery failed in the trailing hour, the latest just now, while the daemon was running"},
		{"before restart", failedAt.Add(time.Minute), severityInfo, "hooks: 1 hook delivery dropped before the daemon started"},
		{"no daemon", time.Time{}, severityWarning, "deliveries that cannot reach a daemon are dropped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var severities []string
			err := writeDoctorHooks(&out, false, tc.startedAt, func(format string, args ...any) {
				t.Errorf("unexpected failure finding: %s", fmt.Sprintf(format, args...))
			}, func(format string, args ...any) {
				t.Errorf("delivery failures reported as hooks_unverified: %s", fmt.Sprintf(format, args...))
			}, func(string, ...any) {}, func(severity, format string, args ...any) {
				severities = append(severities, severity)
			})
			if err != nil {
				t.Fatalf("writeDoctorHooks: %v", err)
			}
			if !strings.Contains(out.String(), tc.line) {
				t.Fatalf("doctor hook output missing %q:\n%s", tc.line, out.String())
			}
			if !slices.Equal(severities, []string{tc.severity}) {
				t.Fatalf("hook_delivery_failures severities = %v, want [%s]", severities, tc.severity)
			}
		})
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

// A doctor run from a binary other than the amx on PATH says so, since its
// socket and hook checks are judged against itself; the installed binary,
// reached through a symlink or not, and a PATH without amx say nothing.
func TestDoctorNotesABinaryOtherThanTheInstalledOne(t *testing.T) {
	resetDoctorTestState(t)
	dir := t.TempDir()
	installed := filepath.Join(dir, "installed", "amx")
	dev := filepath.Join(dir, "checkout", "amx")
	link := filepath.Join(dir, "bin", "amx")
	for _, path := range []string{installed, dev} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installed, link); err != nil {
		t.Fatal(err)
	}

	notes := func(running string, lookPath func(string) (string, error)) []string {
		doctorExecutable = func() (string, error) { return running, nil }
		doctorLookPath = lookPath
		var got []string
		writeDoctorRunningBinary(func(format string, args ...any) {
			got = append(got, fmt.Sprintf(format, args...))
		})
		return got
	}
	onPath := func(string) (string, error) { return link, nil }

	if got := notes(installed, onPath); len(got) != 0 {
		t.Fatalf("installed binary reached through a symlink noted %q", got)
	}
	if got := notes(dev, func(string) (string, error) { return "", exec.ErrNotFound }); len(got) != 0 {
		t.Fatalf("PATH without amx noted %q", got)
	}
	got := notes(dev, onPath)
	if len(got) != 1 || !strings.Contains(got[0], dev) || !strings.Contains(got[0], link) || !strings.Contains(got[0], "run the installed amx") {
		t.Fatalf("dev binary notes = %q; want one naming both binaries", got)
	}
}

// Several healthy profiles are counted on one line, leaving the profiles that
// need a look on lines of their own; --verbose prints every profile, and a
// lone healthy profile is still named rather than counted.
func TestDoctorHooksSummarizesHealthyProfiles(t *testing.T) {
	healthy := func(name string) debugger.HookProfile {
		return debugger.HookProfile{Profile: name, Harness: harness.Claude, Sync: "in_sync", Activation: "healthy", Verdict: debugger.VerdictHealthy}
	}
	unverified := debugger.HookProfile{Profile: "copilot", Harness: harness.Copilot, Sync: "in_sync", Activation: "unverified", Verdict: debugger.VerdictWarning}
	write := func(profiles []debugger.HookProfile, verbose bool) string {
		t.Helper()
		var out bytes.Buffer
		ignore := func(string, ...any) {}
		if err := writeDoctorHookProfiles(&out, profiles, verbose, ignore, ignore, ignore); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	ahead := healthy("home")
	ahead.ContractAhead = true
	profiles := []debugger.HookProfile{healthy("work"), unverified, ahead}
	if got, want := write(profiles, false), "hooks: 2 profiles in sync and active (--verbose lists each)\n"+
		"hooks: copilot (copilot): in sync, activation unverified\n"; got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}
	if got, want := write(profiles, true), "hooks: work (claude): in sync and active\n"+
		"hooks: copilot (copilot): in sync, activation unverified\n"+
		"hooks: home (claude): in sync and active\n"; got != want {
		t.Errorf("verbose =\n%s\nwant\n%s", got, want)
	}
	if got, want := write([]debugger.HookProfile{healthy("work"), unverified}, false), "hooks: work (claude): in sync and active\n"+
		"hooks: copilot (copilot): in sync, activation unverified\n"; got != want {
		t.Errorf("one healthy profile =\n%s\nwant\n%s", got, want)
	}
}
