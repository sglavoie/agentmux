package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// setUpDoctorFixture installs a healthy socket/binary/config environment plus
// the given daemon snapshot, returning the shared "now" used by the fakes.
func setUpDoctorFixture(t *testing.T, health daemon.Health, snapshot daemon.EventsExport) time.Time {
	t.Helper()
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
	if health.Alive && health.Notification == nil {
		capability := doctorTestNotification
		health.Notification = &capability
	}
	doctorCLIBuild = func() buildinfo.Info { return build }
	newDoctorDaemonClient = func() daemonExportClient {
		return &fakeDaemonExportClient{
			health: health,
			export: func(int) (daemon.EventsExport, error) { return snapshot, nil },
		}
	}
	newDoctorKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
	doctorLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	doctorExecutable = func() (string, error) { return binary, nil }
	doctorSocketPath = func() string { return socket }
	doctorNow = func() time.Time { return now }
	return now
}

func violatingDoctorSnapshot(now time.Time) daemon.EventsExport {
	return daemon.EventsExport{
		WindowMetadata: map[int]daemon.WindowDebugMetadata{
			// An active ASL turn rendered as empty: a health failure. StateSet
			// stays false so the not-live window does not also trip the
			// renderer-drift check — the test asserts exactly the invariant
			// findings it sets up.
			7: {
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "", StateSet: false},
				Shadow: daemon.WindowDebugShadow{EffectiveGen: 1, Facts: []daemon.SourceFactSnapshot{{
					Kind: daemon.SourceASL, Key: "run-1", RunID: "run-1",
					Phase: daemon.ASLPhaseTurn, LeaseLive: true, At: now.Unix(),
				}}},
			},
		},
	}
}

func ownerMismatchDoctorSnapshot(now time.Time) daemon.EventsExport {
	events := make([]daemon.SessionEvent, 0, 19)
	events = append(events, daemon.SessionEvent{
		At: now.Unix(), Event: "UserPromptSubmit", Action: "active",
		Suppressed: true, SuppressionReason: "owner_mismatch",
	})
	for i := 0; i < 18; i++ {
		events = append(events, daemon.SessionEvent{
			At: now.Unix(), Event: "PreToolUse", Action: "active",
			Suppressed: true, SuppressionReason: "owner_mismatch",
		})
	}
	return daemon.EventsExport{
		EventsByWindow: map[int][]daemon.SessionEvent{7: events},
		WindowMetadata: map[int]daemon.WindowDebugMetadata{7: {
			Durable: daemon.WindowDebugDurable{SessionTitle: "OrderBook"},
		}},
	}
}

func TestDoctorStrictExitsNonzeroOnHealthFailures(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, violatingDoctorSnapshot(doctorNow()))
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want *ExitError", err)
	}
	if exitErr.Code != exitHealthFailure || exitErr.Quiet {
		t.Fatalf("ExitError = %+v; want code %d, not quiet", exitErr, exitHealthFailure)
	}
	got := out.String()
	for _, want := range []string{
		"health failures:",
		"[asl_active_state] window 7:",
		"overall: issues detected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorStrictPassesOnCleanDeployment(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, daemon.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("strict doctor on a clean deployment: %v", err)
	}
	if !strings.Contains(out.String(), "overall: clean") {
		t.Errorf("doctor output missing overall: clean:\n%s", out.String())
	}
}

func TestDoctorStrictFailsWhenInteractiveNotificationsUnavailable(t *testing.T) {
	build := doctorTestBuild
	capability := daemon.NotificationCapability{
		Enabled: true, Backend: "alerter", Reason: "not_found",
	}
	health := daemon.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build,
		Notification: &capability,
	}
	setUpDoctorFixture(t, health, daemon.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != exitHealthFailure {
		t.Fatalf("err = %+v; want strict health failure", err)
	}
	got := out.String()
	for _, want := range []string{
		"notifications: unavailable (alerter; not_found)",
		"[notifications_unavailable] interactive notifications are enabled but unavailable: not_found",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorStrictStaysGreenOnDeliveryFailureWarning(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	now := setUpDoctorFixture(t, health, daemon.EventsExport{})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("hook.failure")
	record.Outcome = "error"
	record.Reason = "daemon_unavailable"
	record.ObservedAt = now.UTC().Add(-10 * time.Minute)
	if err := journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("strict doctor with warning-only delivery failures: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"hooks: 1 delivery failure(s) recorded in the trailing hour",
		"warnings:",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorOwnerMismatchTraceRendersSharedFindingsAndFailsStrict(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	now := time.Unix(1_700_000_000, 0)
	setUpDoctorFixture(t, health, ownerMismatchDoctorSnapshot(now))
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != exitHealthFailure || exitErr.Quiet {
		t.Fatalf("err = %+v; want visible strict health failure", err)
	}
	got := out.String()
	for _, want := range []string{
		"health failures:",
		"[owner_mismatch_prompt] window 7: 1 prompt event(s) suppressed as owner_mismatch within 15m0s",
		"warnings:",
		"[owner_mismatch_burst] window 7: 18 non-prompt event(s) suppressed as owner_mismatch within 15m0s",
		"overall: issues detected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONEmitsStructuredReport(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{
		Alive: true, Ready: true, PID: 77, Uptime: 3 * time.Minute, Build: &build,
		Metrics: &daemon.MetricsSnapshot{Accepted: 9},
	}
	setUpDoctorFixture(t, health, violatingDoctorSnapshot(doctorNow()))
	doctorJSON = true
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want *ExitError", err)
	}
	if !exitErr.Quiet {
		t.Fatalf("JSON strict exit must be quiet (complete JSON already on stdout): %+v", exitErr)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
	}
	// Pinned to a literal, not the constant: the wire version is a consumer
	// contract, so a bump has to be a deliberate edit here. v3 arrived with the
	// screenstate_persistent_decline failure identifier.
	if report.SchemaVersion != 3 || report.Healthy {
		t.Fatalf("report = schema %d healthy %t; want schema 3 healthy=false", report.SchemaVersion, report.Healthy)
	}
	if doctorReportSchemaVersion != report.SchemaVersion {
		t.Fatalf("doctorReportSchemaVersion = %d; the emitted report must carry the constant", doctorReportSchemaVersion)
	}
	if !report.Daemon.Alive || !report.Daemon.Ready || report.Daemon.PID != 77 {
		t.Fatalf("report.Daemon = %+v", report.Daemon)
	}
	if report.Daemon.Notification == nil || !report.Daemon.Notification.Interactive || report.Daemon.Notification.Executable != "/opt/homebrew/bin/alerter" {
		t.Fatalf("report.Daemon.Notification = %+v; want ready Alerter capability", report.Daemon.Notification)
	}
	if report.CLIBuild.BinaryHash != doctorTestBuild.BinaryHash {
		t.Fatalf("report.CLIBuild = %+v; want the injected CLI identity", report.CLIBuild)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Check == checkASLActiveState && finding.Severity == severityFailure && finding.WindowID == 7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings missing asl_active_state failure for window 7: %+v", report.Findings)
	}
	// Text sections must be suppressed in JSON mode.
	if strings.Contains(out.String(), "Agent Mux doctor") {
		t.Fatalf("JSON output contains text sections:\n%s", out.String())
	}
}

func TestDoctorJSONOwnerMismatchTraceMatchesTextSeverityAndWindow(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 77, Uptime: time.Minute, Build: &build}
	now := time.Unix(1_700_000_000, 0)
	setUpDoctorFixture(t, health, ownerMismatchDoctorSnapshot(now))
	doctorJSON = true
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != exitHealthFailure || !exitErr.Quiet {
		t.Fatalf("err = %+v; want quiet JSON strict health failure", err)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
	}
	prompt := findingsByCheck(report.Findings, checkOwnerMismatchPrompt)
	if len(prompt) != 1 || prompt[0].Severity != severityFailure || prompt[0].WindowID != 7 {
		t.Fatalf("owner_mismatch_prompt findings = %+v; want one window-7 failure", prompt)
	}
	burst := findingsByCheck(report.Findings, checkOwnerMismatchBurst)
	if len(burst) != 1 || burst[0].Severity != severityWarning || burst[0].WindowID != 7 {
		t.Fatalf("owner_mismatch_burst findings = %+v; want one window-7 warning", burst)
	}
	if report.Healthy {
		t.Fatal("owner_mismatch prompt failure must make the JSON report unhealthy")
	}
}
