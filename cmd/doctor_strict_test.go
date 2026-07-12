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

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
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
	if report.SchemaVersion != doctorReportSchemaVersion || report.Healthy {
		t.Fatalf("report = schema %d healthy %t; want schema %d healthy=false", report.SchemaVersion, report.Healthy, doctorReportSchemaVersion)
	}
	if !report.Daemon.Alive || !report.Daemon.Ready || report.Daemon.PID != 77 {
		t.Fatalf("report.Daemon = %+v", report.Daemon)
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

func TestDoctorStrictIncludesDatastoreFTSAndActivityWriterFailures(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build,
		ActivityWriter: &activity.Metrics{Queued: 3, Persisted: 1, Dropped: 1, WriteFailures: 2, Degraded: true, LastError: "disk unavailable"},
	}
	setUpDoctorFixture(t, health, daemon.EventsExport{})
	verified := false
	doctorInspectDatastore = func(_ context.Context, verifySearch bool) (datastore.Status, error) {
		verified = verifySearch
		return datastore.Status{
			SchemaVersion: 2, CurrentSchemaVersion: 3, Integrity: "ok", SecurePermissions: false,
			PermissionIssues: []string{"database mode is 0644; want 0600"},
			Search:           datastore.SearchStatus{DocumentCount: 4, FTSRowCount: 3, Integrity: "ok", Fresh: false},
		}, nil
	}
	doctorStrict = true
	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)
	if !verified {
		t.Fatal("strict doctor did not request explicit FTS verification")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != exitHealthFailure {
		t.Fatalf("error = %v, want strict health failure", err)
	}
	for _, want := range []string{
		"[datastore_migration]", "[fts_freshness]", "[datastore_permissions]", "[activity_writer]",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor output missing %q:\n%s", want, out.String())
		}
	}
}
