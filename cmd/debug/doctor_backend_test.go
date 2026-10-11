package debug

// What doctor and `amx debug status` say about the tmux backend and about the
// daemon's work for runtime sessions: which states raise a finding, at which
// severity, under which stable identifier, and that none of it carries what a
// pane printed or a launch was given.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/tmux"
)

func seconds(value float64) *float64 { return &value }

// healthyRuntime is a daemon that reconciled a moment ago with three agents
// on one server, two of them watched.
func healthyRuntime() *protocol.RuntimeHealth {
	return &protocol.RuntimeHealth{
		Reconcile: protocol.RuntimeReconcileHealth{
			Enabled: true, IntervalSeconds: 2, Passes: 412, AgeSeconds: seconds(0.4), LastDurationMs: 12, MaxDurationMs: 40,
		},
		Sessions: protocol.RuntimeSessionHealth{Known: true, Live: 3, Tmux: 3, Servers: 1, Attached: 2, Unattached: 1},
		Capture:  protocol.ScreenCaptureHealth{Reads: 320, Failures: 2, AgeSeconds: seconds(1.2), MaxDurationMs: 31},
		TmuxClient: protocol.TmuxClientHealth{
			Limit: tmux.MaxClientCommands, Commands: 1450, MaxInFlight: 2,
		},
	}
}

func runDoctorForBackend(t *testing.T, backend cli.BackendStatus, runtime *protocol.RuntimeHealth, alive bool) (string, doctorReport, error) {
	t.Helper()
	build := doctorTestBuild
	health := ipc.Health{}
	if alive {
		health = ipc.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build, Runtime: runtime}
	}
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	previous := doctorBackendStatus
	doctorBackendStatus = func() cli.BackendStatus { return backend }
	t.Cleanup(func() { doctorBackendStatus = previous })
	doctorStrict = true

	var text bytes.Buffer
	doctorCmd.SetOut(&text)
	err := runDoctor(doctorCmd, nil)

	doctorJSON = true
	var encoded bytes.Buffer
	doctorCmd.SetOut(&encoded)
	_ = runDoctor(doctorCmd, nil)
	var report doctorReport
	if decodeErr := json.Unmarshal(encoded.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode doctor report: %v\n%s", decodeErr, encoded.String())
	}
	return text.String(), report, err
}

func TestDoctorReportsAHealthyBackendWithoutAFinding(t *testing.T) {
	backend := quietBackendStatus()
	backend.Tmux.Source, backend.Tmux.Verified = "bundled", true
	backend.Server = cli.TmuxServerStatus{Running: true, Binary: backend.Tmux.Path, Sessions: 3, Agents: 3}
	got, report, err := runDoctorForBackend(t, backend, healthyRuntime(), true)
	if err != nil {
		t.Fatalf("a healthy backend must not fail --strict: %v\n%s", err, got)
	}
	for _, want := range []string{
		"backend: launches go to kitty; tmux 3.7c (bundled, verified); server running with 3 agents in 3 sessions",
		"runtime sessions: 3 live (3 on tmux, 1 server), 2 with a terminal attached, 1 unattached; reconciled 400ms ago in 12ms (412 passes, 0 failed)",
		"screen captures: 320 read, 2 failed, last good one 1.2s ago (slowest 31ms); tmux clients: 1450 run, at most 2 at once (limit 4), 0 timed out",
		"versions: amx " + doctorTestBuild.Version,
		"tmux 3.7c (needs " + tmux.MinVersion + " or newer), kitty 0.43.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.Check, checkBackendPrefix) || strings.HasPrefix(finding.Check, "runtime_") ||
			finding.Check == checkScreenCaptureFailing || finding.Check == checkTmuxClientTimeouts {
			t.Errorf("unexpected finding on a healthy backend: %+v", finding)
		}
	}
	if report.Daemon.Runtime == nil || report.Daemon.Runtime.Sessions.Attached != 2 {
		t.Errorf("daemon.runtime = %+v; want the daemon's block carried through", report.Daemon.Runtime)
	}
	if !report.Backend.Server.Running || report.Versions.Tmux != "3.7c" || report.Versions.TmuxMin != tmux.MinVersion || report.Versions.Kitty != "0.43.1" {
		t.Errorf("backend = %+v versions = %+v", report.Backend, report.Versions)
	}
}

// The backend is read by doctor itself, so a daemon that is down does not
// hide a tmux that cannot be used, and a tmux that cannot be used fails
// --strict exactly where the operator launches on tmux.
func TestDoctorDiagnosesAnUnusableTmuxWithTheDaemonDown(t *testing.T) {
	unusable := func(launch, severity string) cli.BackendStatus {
		return cli.BackendStatus{
			LaunchBackend: launch,
			Tmux:          cli.TmuxStatus{MinVersion: tmux.MinVersion, Candidates: []tmux.Candidate{{Source: "path", Problem: "tmux not found"}}},
			Problems: []cli.BackendProblem{{
				Code: "tmux_unavailable", Severity: severity,
				Message: "The tmux backend cannot start agents: no usable tmux was found.", Hint: tmux.RuntimeHint,
			}},
		}
	}

	got, report, err := runDoctorForBackend(t, unusable(config.LaunchBackendTmux, "error"), nil, false)
	if err == nil {
		t.Fatalf("launching on a tmux that cannot be used must fail --strict:\n%s", got)
	}
	if !strings.Contains(got, "backend: launches go to tmux; no usable tmux; no server running") {
		t.Errorf("doctor output missing the backend line:\n%s", got)
	}
	found := findingsFor(report, "backend_tmux_unavailable")
	if len(found) != 1 || found[0].Severity != severityFailure || !strings.Contains(found[0].Message, tmux.RuntimeHint) {
		t.Fatalf("backend_tmux_unavailable = %+v; want one failure carrying the hint", found)
	}
	if strings.Contains(got, "runtime sessions:") {
		t.Errorf("doctor must not describe the work of a daemon it never reached:\n%s", got)
	}

	// The same machine, for someone who launches in Kitty: how it is set up.
	_, report, _ = runDoctorForBackend(t, unusable(config.LaunchBackendKitty, "warning"), nil, false)
	found = findingsFor(report, "backend_tmux_unavailable")
	if len(found) != 1 || found[0].Severity != severityInfo {
		t.Fatalf("backend_tmux_unavailable = %+v; want one note where launches go to Kitty and no server runs", found)
	}
}

func TestDoctorNamesEveryBackendProblemByItsStatusCode(t *testing.T) {
	backend := quietBackendStatus()
	backend.LaunchBackend = config.LaunchBackendTmux
	backend.Server = cli.TmuxServerStatus{Running: true}
	backend.Problems = []cli.BackendProblem{
		{Code: "config_invalid", Severity: "error", Message: "The configuration could not be loaded."},
		{Code: "server_binary_unknown", Severity: "warning", Message: "A private tmux server is running, but its tmux is not recorded."},
		{Code: "server_keeps_previous_tmux", Severity: "info", Message: "The running server keeps the tmux it was started with."},
		{Code: "server_unreadable", Severity: "error", Message: "The private tmux server is running but did not answer."},
	}
	_, report, err := runDoctorForBackend(t, backend, healthyRuntime(), true)
	if err == nil {
		t.Fatal("a server that does not answer where launches go to tmux must fail --strict")
	}
	want := map[string]string{
		"backend_server_binary_unknown":      severityWarning,
		"backend_server_keeps_previous_tmux": severityInfo,
		"backend_server_unreadable":          severityFailure,
	}
	for check, severity := range want {
		found := findingsFor(report, check)
		if len(found) != 1 || found[0].Severity != severity {
			t.Errorf("%s = %+v; want one %s", check, found, severity)
		}
	}
	// The configuration is reported where doctor reads it, once.
	if found := findingsFor(report, "backend_config_invalid"); len(found) != 0 {
		t.Errorf("backend_config_invalid = %+v; want none", found)
	}
}

func TestDoctorJudgesTheDaemonsRuntimeWork(t *testing.T) {
	cases := []struct {
		name     string
		change   func(*protocol.RuntimeHealth)
		check    string
		severity string
		wantText string
	}{
		{
			name: "passes that keep failing",
			change: func(r *protocol.RuntimeHealth) {
				r.Reconcile.Failures, r.Reconcile.ConsecutiveFailures, r.Reconcile.FailureClass = 9, 3, "timeout"
			},
			check: checkRuntimeReconcileFailing, severity: severityWarning, wantText: "the last 3 reconciliation passes failed (timeout)",
		},
		{
			name:   "a maintenance loop that stopped",
			change: func(r *protocol.RuntimeHealth) { r.Reconcile.AgeSeconds = seconds(95) },
			check:  checkRuntimeReconcileStale, severity: severityWarning, wantText: "last reconciled runtime sessions 1m35s ago",
		},
		{
			name: "screen reads that all fail",
			change: func(r *protocol.RuntimeHealth) {
				r.Capture.ConsecutiveFailures, r.Capture.AgeSeconds = 10, seconds(40)
			},
			check: checkScreenCaptureFailing, severity: severityWarning, wantText: "the last 10 screen reads failed",
		},
		{
			name:   "tmux clients that timed out",
			change: func(r *protocol.RuntimeHealth) { r.TmuxClient.Timeouts, r.TmuxClient.MaxQueueMs = 2, 5000 },
			check:  checkTmuxClientTimeouts, severity: severityInfo, wantText: "2 of the daemon's 1450 tmux client commands timed out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := healthyRuntime()
			tc.change(runtime)
			got, report, err := runDoctorForBackend(t, quietBackendStatus(), runtime, true)
			if err != nil {
				t.Fatalf("a %s finding must not fail --strict: %v", tc.severity, err)
			}
			found := findingsFor(report, tc.check)
			if len(found) != 1 || found[0].Severity != tc.severity || !strings.Contains(found[0].Message, tc.wantText) {
				t.Fatalf("%s = %+v; want one %s containing %q\n%s", tc.check, found, tc.severity, tc.wantText, got)
			}
		})
	}

	// One failed pass is a busy server and one failed read a pane that just
	// closed: neither is a finding.
	runtime := healthyRuntime()
	runtime.Reconcile.Failures, runtime.Reconcile.ConsecutiveFailures, runtime.Reconcile.FailureClass = 1, 1, "timeout"
	runtime.Capture.ConsecutiveFailures = 1
	got, report, _ := runDoctorForBackend(t, quietBackendStatus(), runtime, true)
	if len(findingsFor(report, checkRuntimeReconcileFailing))+len(findingsFor(report, checkScreenCaptureFailing)) != 0 {
		t.Errorf("a single failure raised a finding:\n%s", got)
	}
	if !strings.Contains(got, "last pass failed: timeout") {
		t.Errorf("doctor output does not name the failed pass:\n%s", got)
	}
}

// A daemon that predates the block says nothing about its runtime work, which
// is unknown rather than broken.
func TestDoctorLegacyDaemonRuntimeIsUnknownNotAFailure(t *testing.T) {
	got, report, err := runDoctorForBackend(t, quietBackendStatus(), nil, true)
	if err != nil {
		t.Fatalf("an older daemon must not fail --strict: %v", err)
	}
	if !strings.Contains(got, "runtime sessions: unreported (older daemon; restart required)") {
		t.Errorf("doctor output missing the unreported line:\n%s", got)
	}
	if strings.Contains(got, "screen captures:") {
		t.Errorf("doctor described captures a daemon never reported:\n%s", got)
	}
	if report.Daemon.Runtime != nil {
		t.Errorf("daemon.runtime = %+v; want it omitted", report.Daemon.Runtime)
	}
}

// The identifiers this file raises are contracts like every other finding's.
func TestBackendFindingIdentifiersAreStable(t *testing.T) {
	got := []string{
		checkBackendPrefix + "tmux_unavailable", checkBackendPrefix + "server_unreadable",
		checkRuntimeReconcileStale, checkRuntimeReconcileFailing, checkScreenCaptureFailing, checkTmuxClientTimeouts,
	}
	want := []string{
		"backend_tmux_unavailable", "backend_server_unreadable",
		"runtime_reconcile_stale", "runtime_reconcile_failing", "screen_capture_failing", "tmux_client_timeouts",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("identifier %d = %q; want %q", i, got[i], want[i])
		}
	}
}

type failingKittyLister struct{}

func (failingKittyLister) ListWindows() ([]kitty.OSWindow, error) {
	return nil, errors.New("KITTY_LISTEN_ON is not set")
}

// A Kitty that does not answer is a failure wherever one was named, by this
// terminal or by the daemon. With no Kitty to ask there is nothing to judge:
// a note where launches go to tmux, a warning where they go to Kitty.
func TestDoctorKittyIsNotAFailureWhereItIsNotInUse(t *testing.T) {
	run := func(launch, listenOn, daemonSocket string) (string, doctorReport, error) {
		backend := quietBackendStatus()
		backend.LaunchBackend = launch
		health := ipc.Health{}
		if daemonSocket != "" {
			health.KittySnapshot = &protocol.KittySnapshot{Ready: true, Socket: daemonSocket}
		}
		setUpDoctorFixture(t, health, protocol.EventsExport{})
		previousStatus, previousGetenv := doctorBackendStatus, doctorGetenv
		doctorBackendStatus = func() cli.BackendStatus { return backend }
		doctorGetenv = func(key string) string {
			if key == "KITTY_LISTEN_ON" {
				return listenOn
			}
			return ""
		}
		t.Cleanup(func() { doctorBackendStatus, doctorGetenv = previousStatus, previousGetenv })
		newDoctorKitty = func() cli.KittyWindowLister { return failingKittyLister{} }
		doctorJSON = true
		var out bytes.Buffer
		doctorCmd.SetOut(&out)
		err := runDoctor(doctorCmd, nil)
		var report doctorReport
		if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
			t.Fatalf("decode doctor report: %v\n%s", decodeErr, out.String())
		}
		return out.String(), report, err
	}
	severityOf := func(report doctorReport) string {
		found := findingsFor(report, "kitty")
		if len(found) != 1 {
			t.Fatalf("kitty findings = %+v; want exactly one", found)
		}
		return found[0].Severity
	}

	if _, report, _ := run(config.LaunchBackendTmux, "", ""); severityOf(report) != severityInfo {
		t.Errorf("launches on tmux, no Kitty listening: kitty is %s; want a note", severityOf(report))
	}
	if _, report, _ := run(config.LaunchBackendTmux, "unix:/tmp/kitty-1", ""); severityOf(report) != severityFailure {
		t.Errorf("launches on tmux inside a Kitty window that does not answer: kitty is %s; want a failure", severityOf(report))
	}
	if _, report, _ := run(config.LaunchBackendKitty, "unix:/tmp/kitty-1", ""); severityOf(report) != severityFailure {
		t.Errorf("launches in Kitty, inside a Kitty window that does not answer: kitty is %s; want a failure", severityOf(report))
	}
	if _, report, _ := run(config.LaunchBackendKitty, "", "unix:/tmp/kitty-1"); severityOf(report) != severityFailure {
		t.Errorf("outside Kitty, the daemon's Kitty does not answer: kitty is %s; want a failure", severityOf(report))
	}
	if _, report, _ := run(config.LaunchBackendKitty, "", ""); severityOf(report) != severityWarning {
		t.Errorf("launches in Kitty, no Kitty named by this terminal or the daemon: kitty is %s; want a warning", severityOf(report))
	}
}
