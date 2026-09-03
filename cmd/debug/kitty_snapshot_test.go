package debug

// The diagnostic surfaces over the daemon's cached Kitty topology: what doctor
// and `amx debug status` say about it, and which of the four states raises a
// finding. The daemon's own arithmetic is pinned in internal/daemon; what these
// tests own is that a CLI never re-decides staleness and never turns a version
// gap into a health fault.

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func freshKittySnapshot() *protocol.KittySnapshot {
	return &protocol.KittySnapshot{
		Ready: true, Generation: 12, CapturedAt: 1_700_000_000, AgeMS: 1_500,
		Coordinator: &protocol.KittyListCoordinatorStatus{
			Socket: "unix:/tmp/kitty-756", Attempts: 40, LastLatencyMS: 35, MaxLatencyMS: 120,
		},
	}
}

// staleKittySnapshot carries the daemon's own Stale verdict beside an age past
// the threshold. Both are set because the CLI must read the boolean: an age
// alone would let a test pass against a CLI that recomputed the verdict.
func staleKittySnapshot() *protocol.KittySnapshot {
	return &protocol.KittySnapshot{
		Ready: true, Stale: true, Generation: 12, CapturedAt: 1_700_000_000,
		AgeMS: protocol.DurationMillis(9 * time.Second),
		Coordinator: &protocol.KittyListCoordinatorStatus{
			Socket: "unix:/tmp/kitty-756", Attempts: 40, Failures: 3, Timeouts: 3,
			ConsecutiveFailures: 3, CoalescedWaiters: 5, InFlight: true,
			LastLatencyMS: 10_000, MaxLatencyMS: 10_000,
			LastFailureAt: 1_700_000_000, LastErrorClass: string(kitty.ListErrorTimeout),
		},
	}
}

func runDoctorForKittySnapshot(t *testing.T, snapshot *protocol.KittySnapshot) (string, error) {
	t.Helper()
	build := doctorTestBuild
	health := ipc.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build,
		KittySnapshot: snapshot,
	}
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)
	return out.String(), err
}

func TestDoctorReportsAFreshKittySnapshotWithoutAFinding(t *testing.T) {
	got, err := runDoctorForKittySnapshot(t, freshKittySnapshot())
	if err != nil {
		t.Fatalf("strict doctor with a fresh snapshot: %v", err)
	}
	for _, want := range []string{
		"kitty snapshot: fresh (generation 12, age 1.5s)",
		"kitty list coordinator: socket=unix:/tmp/kitty-756 attempts=40 failures=0 timeouts=0 coalesced=0 in_flight=false consecutive_failures=0 last_latency=35ms max_latency=120ms last_error=none",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, checkKittySnapshotStale) {
		t.Errorf("a fresh snapshot must raise no finding:\n%s", got)
	}
}

// The whole point of the finding is that it is diagnostic, not fatal: an
// operator running `amx doctor --strict` in a release gate must see the stale
// snapshot without the gate failing on it.
func TestDoctorStaleKittySnapshotWarnsWithoutFailingStrict(t *testing.T) {
	got, err := runDoctorForKittySnapshot(t, staleKittySnapshot())
	if err != nil {
		t.Fatalf("a warning-severity stale snapshot must not fail --strict: %v", err)
	}
	for _, want := range []string{
		"kitty snapshot: stale (generation 12, age 9s, threshold 6s)",
		"consecutive_failures=3",
		"last_error=timeout",
		"warnings:",
		"[kitty_snapshot_stale] daemon Kitty topology is stale: the last successful kitten @ ls completed 9s ago (threshold 6s, generation 12)",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

// A cold daemon has no age to be past. protocol.KittySnapshotStale already
// refuses to call it stale, so this pins that doctor did not add an opinion of
// its own on top of that rule.
func TestDoctorColdKittySnapshotIsReportedAndRaisesNoFinding(t *testing.T) {
	cold := &protocol.KittySnapshot{Ready: false, AgeMS: 0}
	got, err := runDoctorForKittySnapshot(t, cold)
	if err != nil {
		t.Fatalf("strict doctor with a cold snapshot: %v", err)
	}
	for _, want := range []string{
		"kitty snapshot: cold (no successful kitten @ ls yet)",
		"kitty list coordinator: no information (never read, evicted, or unreported)",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, checkKittySnapshotStale) {
		t.Errorf("a cold snapshot must raise no finding:\n%s", got)
	}
}

// A daemon old enough to omit the block is a version gap, not a fault: it must
// not be reported as cold (which would claim the daemon has never listed) and
// must not raise a finding an operator would try to fix in Kitty.
func TestDoctorLegacyDaemonKittySnapshotIsUnknownNotAFailure(t *testing.T) {
	got, err := runDoctorForKittySnapshot(t, nil)
	if err != nil {
		t.Fatalf("strict doctor against a daemon without the block: %v", err)
	}
	if !strings.Contains(got, "kitty snapshot: unreported (older daemon; restart required)") {
		t.Errorf("doctor output missing the unreported line:\n%s", got)
	}
	for _, unwanted := range []string{checkKittySnapshotStale, "kitty list coordinator:", "cold ("} {
		if strings.Contains(got, unwanted) {
			t.Errorf("doctor must not emit %q for a daemon without the block:\n%s", unwanted, got)
		}
	}
}

// An unreachable daemon has its own failure finding and its own line. Adding a
// snapshot verdict under it would describe a daemon that was never asked.
func TestDoctorOmitsTheKittySnapshotWhenTheDaemonIsUnreachable(t *testing.T) {
	setUpDoctorFixture(t, ipc.Health{}, protocol.EventsExport{})
	doctorStrict = false

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "daemon: unreachable") {
		t.Fatalf("fixture did not produce an unreachable daemon:\n%s", got)
	}
	if strings.Contains(got, "kitty snapshot:") {
		t.Errorf("doctor must not report a snapshot for a daemon it never reached:\n%s", got)
	}
}

func TestDoctorJSONCarriesTheKittySnapshotBlock(t *testing.T) {
	build := doctorTestBuild
	health := ipc.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build,
		KittySnapshot: staleKittySnapshot(),
	}
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorJSON = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v\n%s", err, out.String())
	}
	block := report.Daemon.KittySnapshot
	if block == nil {
		t.Fatal("doctor JSON dropped the kitty_snapshot block")
	}
	if !block.Ready || !block.Stale || block.Generation != 12 {
		t.Fatalf("kitty_snapshot = %+v; want the daemon's own ready/stale verdict", block)
	}
	if block.Coordinator == nil || block.Coordinator.ConsecutiveFailures != 3 {
		t.Fatalf("kitty_snapshot.coordinator = %+v; want the retained failure accounting", block.Coordinator)
	}
	// Health carries status only. A window tree leaking into a doctor report
	// would put an unbounded payload behind a diagnostic command.
	if len(block.Windows) != 0 {
		t.Errorf("kitty_snapshot.windows = %d entries; health must carry status only", len(block.Windows))
	}
	// The report stays healthy: warnings never change Healthy, and this is the
	// same contract --strict relies on above.
	if !report.Healthy {
		t.Errorf("a warning-severity finding must not make the report unhealthy: %+v", report.Findings)
	}
}

// runDebugStatusForKittySnapshot drives the real `amx debug status` leaf, in
// both output modes, against a daemon publishing snapshot. Everything else is
// offline on purpose: the snapshot must be reported by a status run that could
// reach neither Kitty nor a config.
func runDebugStatusForKittySnapshot(t *testing.T, snapshot *protocol.KittySnapshot, asJSON bool) string {
	t.Helper()
	resetDebugTestState(t)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	enabled := true
	debugLoadConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
			JournalEnabled: &enabled, JournalMaxMiB: 1, NativeLogging: &enabled, NativeMaxMiB: 1, NativeSessionMaxMiB: 1,
		}}}, nil
	}
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
	debugExecutable = func() (string, error) { return "/tmp/amx", nil }
	debugNow = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	debugDaemon = func() debugger.DaemonHealthClient {
		return debugTestDaemon{health: ipc.Health{
			Alive: true, Ready: true, PID: 3, Uptime: time.Minute, KittySnapshot: snapshot,
		}}
	}
	debugKitty = func() debugger.KittyWindowLister { return debugTestKitty{err: errors.New("offline")} }
	debugStatusJSON = asJSON

	var out bytes.Buffer
	debugStatusCmd.SetOut(&out)
	if err := debugStatusCmd.RunE(debugStatusCmd, nil); err != nil {
		t.Fatalf("debug status: %v", err)
	}
	return out.String()
}

func TestDebugStatusCarriesTheKittySnapshotWithoutChangingItsVerdict(t *testing.T) {
	raw := runDebugStatusForKittySnapshot(t, staleKittySnapshot(), true)
	var report debugger.StatusReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, raw)
	}
	if report.Daemon.KittySnapshot == nil || !report.Daemon.KittySnapshot.Stale {
		t.Fatalf("status daemon block = %+v; want the stale snapshot", report.Daemon.KittySnapshot)
	}
	// debug status describes; doctor judges. A stale Kitty read must not move
	// the daemon verdict a consumer has always branched on.
	if report.Daemon.Verdict != debugger.VerdictHealthy {
		t.Errorf("daemon verdict = %s; a stale snapshot must not change it", report.Daemon.Verdict)
	}
	for _, finding := range report.Findings {
		if finding.Check == checkKittySnapshotStale {
			t.Errorf("debug status must not own the doctor finding: %+v", finding)
		}
	}
	// The block is additive; the report version is not a consumer's problem.
	if report.SchemaVersion != 2 || debugger.StatusSchemaVersion != 2 {
		t.Errorf("status schema = %d; the snapshot block is additive and must not bump it", report.SchemaVersion)
	}

	got := runDebugStatusForKittySnapshot(t, staleKittySnapshot(), false)
	for _, want := range []string{
		"kitty snapshot: stale (generation 12, age 9s, threshold 6s)",
		"kitty list coordinator: socket=unix:/tmp/kitty-756",
		"consecutive_failures=3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("debug status text missing %q:\n%s", want, got)
		}
	}
}

// Doctor and debug status share one renderer precisely so an operator reading
// either one sees the same words for the same state.
func TestDebugStatusAndDoctorAgreeOnEveryKittySnapshotState(t *testing.T) {
	for _, snapshot := range []*protocol.KittySnapshot{
		nil,
		{Ready: false},
		freshKittySnapshot(),
		staleKittySnapshot(),
	} {
		want := "kitty snapshot: " + kittySnapshotSummary(snapshot)
		if status := runDebugStatusForKittySnapshot(t, snapshot, false); !strings.Contains(status, want) {
			t.Errorf("debug status missing %q:\n%s", want, status)
		}
		doctorOut, err := runDoctorForKittySnapshot(t, snapshot)
		if err != nil {
			t.Fatalf("strict doctor for %s: %v", kittySnapshotState(snapshot), err)
		}
		if !strings.Contains(doctorOut, want) {
			t.Errorf("doctor missing %q:\n%s", want, doctorOut)
		}
	}
}
