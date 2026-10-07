package debug

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func resetAuditTestState(t *testing.T) {
	t.Helper()
	resetDoctorTestState(t)
	oldDuration := auditDuration
	oldJSON := auditJSON
	oldNow := auditNow
	oldSleep := auditSleep
	oldOut := daemonAuditCmd.OutOrStdout()
	t.Cleanup(func() {
		auditDuration = oldDuration
		auditJSON = oldJSON
		auditNow = oldNow
		auditSleep = oldSleep
		daemonAuditCmd.SetOut(oldOut)
	})
}

// installAuditFakeClock wires auditNow/auditSleep to a fake clock so a
// multi-sample audit runs instantly and deterministically.
func installAuditFakeClock(start time.Time) {
	current := start
	auditNow = func() time.Time { return current }
	auditSleep = func(d time.Duration) { current = current.Add(d) }
}

func TestDaemonAuditSamplesOnCadenceAndExitsNonzeroOnViolations(t *testing.T) {
	resetAuditTestState(t)
	start := time.Unix(1_700_000_000, 0)
	installAuditFakeClock(start)
	auditDuration = 4 * time.Second
	auditJSON = false

	build := doctorTestBuild
	doctorCLIBuild = func() buildinfo.Info { return build }
	newDoctorDaemonClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{
			health: ipc.Health{Alive: true, Ready: true, Build: &build},
			export: func(int) (protocol.EventsExport, error) {
				return violatingDoctorSnapshot(start), nil
			},
		}
	}
	newDoctorKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }

	var out bytes.Buffer
	daemonAuditCmd.SetOut(&out)
	err := runDaemonAudit(daemonAuditCmd, nil)

	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want *cli.ExitError", err)
	}
	if exitErr.Code != cli.ExitHealthFailure {
		t.Fatalf("exit code = %d; want %d", exitErr.Code, cli.ExitHealthFailure)
	}
	got := out.String()
	// 4s at a 2s cadence: samples at t+0, t+2, t+4.
	if !strings.Contains(got, "3 samples over 4s") {
		t.Errorf("output missing sample count:\n%s", got)
	}
	// The same persistent violation must be deduplicated across samples.
	if n := strings.Count(got, "[asl_active_state]"); n != 1 {
		t.Errorf("asl_active_state reported %d times; want 1 (deduplicated):\n%s", n, got)
	}
	if !strings.Contains(got, "audit: 1 invariant violation") {
		t.Errorf("output missing violation verdict:\n%s", got)
	}
}

func TestDaemonAuditCleanRunExitsZeroAndJSONReports(t *testing.T) {
	resetAuditTestState(t)
	start := time.Unix(1_700_000_000, 0)
	installAuditFakeClock(start)
	auditDuration = 2 * time.Second
	auditJSON = true

	build := doctorTestBuild
	doctorCLIBuild = func() buildinfo.Info { return build }
	newDoctorDaemonClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{
			health: ipc.Health{Alive: true, Ready: true, Build: &build},
			export: func(int) (protocol.EventsExport, error) { return protocol.EventsExport{}, nil },
		}
	}
	newDoctorKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }

	var out bytes.Buffer
	daemonAuditCmd.SetOut(&out)
	if err := runDaemonAudit(daemonAuditCmd, nil); err != nil {
		t.Fatalf("clean audit: %v", err)
	}

	var report auditReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
	}
	if !report.Healthy || report.Samples < 1 || report.SchemaVersion != auditReportSchemaVersion {
		t.Fatalf("report = %+v; want healthy with at least one sample", report)
	}
}

func TestDaemonAuditOwnerMismatchTraceUsesSharedFindingsAndFails(t *testing.T) {
	resetAuditTestState(t)
	start := time.Unix(1_700_000_000, 0)
	installAuditFakeClock(start)
	auditDuration = 4 * time.Second
	auditJSON = true

	build := doctorTestBuild
	doctorCLIBuild = func() buildinfo.Info { return build }
	newDoctorDaemonClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{
			health: ipc.Health{Alive: true, Ready: true, Build: &build},
			export: func(int) (protocol.EventsExport, error) {
				return ownerMismatchDoctorSnapshot(start), nil
			},
		}
	}
	newDoctorKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }

	var out bytes.Buffer
	daemonAuditCmd.SetOut(&out)
	err := runDaemonAudit(daemonAuditCmd, nil)
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != cli.ExitHealthFailure || !exitErr.Quiet {
		t.Fatalf("err = %+v; want quiet JSON audit health failure", err)
	}

	var report auditReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
	}
	if report.Healthy || report.Samples != 3 {
		t.Fatalf("report = %+v; want unhealthy with 3 samples", report)
	}
	prompt := findingsByCheck(report.Findings, checkOwnerMismatchPrompt)
	if len(prompt) != 1 || prompt[0].Severity != severityFailure || prompt[0].WindowID != 7 {
		t.Fatalf("deduplicated owner_mismatch_prompt findings = %+v; want one window-7 failure", prompt)
	}
	burst := findingsByCheck(report.Findings, checkOwnerMismatchBurst)
	if len(burst) != 1 || burst[0].Severity != severityWarning || burst[0].WindowID != 7 {
		t.Fatalf("deduplicated owner_mismatch_burst findings = %+v; want one window-7 warning", burst)
	}
}

func TestDaemonAuditUnreachableDaemonIsAViolation(t *testing.T) {
	resetAuditTestState(t)
	installAuditFakeClock(time.Unix(1_700_000_000, 0))
	auditDuration = 2 * time.Second
	auditJSON = false

	newDoctorDaemonClient = func() cli.DaemonExportClient {
		return &fakeDaemonExportClient{health: ipc.Health{}}
	}
	newDoctorKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }

	var out bytes.Buffer
	daemonAuditCmd.SetOut(&out)
	err := runDaemonAudit(daemonAuditCmd, nil)

	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want *cli.ExitError", err)
	}
	if !strings.Contains(out.String(), "[daemon_unreachable]") {
		t.Errorf("output missing daemon_unreachable finding:\n%s", out.String())
	}
}

func TestDaemonAuditRejectsNonPositiveDuration(t *testing.T) {
	resetAuditTestState(t)
	auditDuration = 0
	if err := runDaemonAudit(daemonAuditCmd, nil); err == nil {
		t.Fatal("expected an error for --duration 0")
	}
}
