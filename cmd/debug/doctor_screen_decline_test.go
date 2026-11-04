package debug

// `screenstate_persistent_decline` is the first health finding sourced from the
// daemon watching its own classifier refuse. These tests pin the two halves an
// operator depends on: a pane stuck behind an unchanging screen the classifier
// no longer matches is a strict failure carrying the evidence needed to fix the
// patterns, and every shape that merely resembles it — a short streak, a
// repainting pane, a closed window, a window that has since recovered — stays
// silent.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// stuckScreenDeclineSnapshot is a live ⚡ window whose probe has refused the
// same frame past the threshold: the synthetic stuck-bolt case. Its renderer
// state agrees with the reducer, so the fixture describes a live session.
func stuckScreenDeclineSnapshot(streak int) protocol.EventsExport {
	return protocol.EventsExport{
		WindowMetadata: map[int]protocol.WindowDebugMetadata{
			7: {
				Renderer: protocol.WindowDebugRenderer{DesiredIcon: "bolt", StateSet: true},
				Shadow:   protocol.WindowDebugShadow{EffectiveIcon: "bolt", EffectiveGen: 4},
				Watchdog: protocol.WindowDebugWatchdog{ScreenDeclines: []protocol.ScreenDeclineSnapshot{{
					Source:            "codex_screen_prompt",
					Streak:            streak,
					StableFingerprint: true,
					Fingerprint:       0x1111,
					Vetoes:            "working_status_line",
					StatusLine:        "Working (12s · esc to interrupt)",
				}}},
			},
		},
	}
}

func setUpStuckScreenDeclineFixture(t *testing.T, snapshot protocol.EventsExport) {
	t.Helper()
	build := doctorTestBuild
	health := ipc.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, snapshot)
}

func TestDoctorStrictFailsOnPersistentScreenDecline(t *testing.T) {
	setUpStuckScreenDeclineFixture(t, stuckScreenDeclineSnapshot(screenDeclinePersistentStreak))
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != cli.ExitHealthFailure {
		t.Fatalf("err = %+v; want strict health failure", err)
	}
	got := out.String()
	for _, want := range []string{
		"health failures:",
		"[screenstate_persistent_decline] window 7:",
		`screen probe "codex_screen_prompt" declined the same unchanging screen`,
		// The veto set and the status line are the whole point of the message:
		// they are what an operator diffs against the shipped patterns.
		"vetoes=working_status_line",
		`status line="Working (12s · esc to interrupt)"`,
		"overall: issues detected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

// TestDoctorStaysCleanOnBenignScreenDeclines is the false-positive guard. Each
// case is a decline state a perfectly healthy deployment produces routinely; a
// check that fired on any of them would make `doctor --strict` useless.
func TestDoctorStaysCleanOnBenignScreenDeclines(t *testing.T) {
	cases := []struct {
		name     string
		snapshot protocol.EventsExport
	}{
		{
			// A pane that is genuinely working repaints its elapsed-time
			// counter, so the fingerprint moves and the streak restarts. This
			// is the ordinary state of every busy window.
			name: "repainting pane never reaches a stable fingerprint",
			snapshot: func() protocol.EventsExport {
				snapshot := stuckScreenDeclineSnapshot(screenDeclinePersistentStreak * 10)
				meta := snapshot.WindowMetadata[7]
				meta.Watchdog.ScreenDeclines[0].StableFingerprint = false
				snapshot.WindowMetadata[7] = meta
				return snapshot
			}(),
		},
		{
			// A turn that simply takes a while: stable frames, but nowhere
			// near long enough to be anything but ordinary work.
			name:     "short streak below the threshold",
			snapshot: stuckScreenDeclineSnapshot(screenDeclinePersistentStreak - 1),
		},
		{
			// A tombstoned window is never classified again, so whatever its
			// last streak was it can no longer be a live problem.
			name: "closed window",
			snapshot: func() protocol.EventsExport {
				snapshot := stuckScreenDeclineSnapshot(screenDeclinePersistentStreak)
				meta := snapshot.WindowMetadata[7]
				meta.Watchdog.ClosedAt = 1_700_000_000
				snapshot.WindowMetadata[7] = meta
				return snapshot
			}(),
		},
		{
			// The clean deployment: no windows, no declines, nothing to say.
			name:     "clean deployment",
			snapshot: protocol.EventsExport{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setUpStuckScreenDeclineFixture(t, tc.snapshot)
			doctorJSON = true
			doctorStrict = true

			var out bytes.Buffer
			doctorCmd.SetOut(&out)
			if err := runDoctor(doctorCmd, nil); err != nil {
				t.Fatalf("strict doctor: %v\n%s", err, out.String())
			}
			var report doctorReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
			}
			if found := findingsByCheck(report.Findings, checkScreenPersistentDecline); len(found) != 0 {
				t.Fatalf("benign decline state produced findings: %+v", found)
			}
			if !report.Healthy {
				t.Fatalf("report is unhealthy: %+v", report.Findings)
			}
		})
	}
}

// TestDoctorIgnoresLiveClearWithStaleDeclineExport is the stale-export half of
// the recovery case, and the one the daemon's own retraction cannot cover: the
// operator cleared the pane, but the export doctor read still carries the
// threshold decline AND says ⚡ in both the renderer's desired icon and the
// reducer's effective icon. Only the live pane metadata has caught up.
//
// A found live window with no state icon is exactly that clear, so it is the
// freshest reading of the three and the finding must retract on it alone.
// TestDoctorIgnoresRecoveredPersistentScreenDecline's clear case empties the
// exported icons too, so it never puts a live clear against a stale bolt.
//
// Renderer drift is the correct reading of that disagreement and is expected
// here, so this asserts only that the decline finding is gone.

func TestScreenDeclineInvariantIgnoresLiveClearWithStaleExport(t *testing.T) {
	findings := evaluateDaemonInvariants(
		stuckScreenDeclineSnapshot(screenDeclinePersistentStreak+5), ipc.Health{}, doctorTestBuild,
		map[int]protocol.WindowLiveMetadata{7: {Found: true, IconState: ""}}, time.Now(),
	)
	if found := findingsByCheck(findings, checkScreenPersistentDecline); len(found) != 0 {
		t.Fatalf("cleared window still reported as persistently declining: %+v", found)
	}
}

// TestDoctorJSONReportsPersistentScreenDecline pins the JSON half of the
// contract: the stable identifier, the failure severity that drives strict
// exit, and the window attribution a consumer routes on.
func TestDoctorJSONReportsPersistentScreenDecline(t *testing.T) {
	setUpStuckScreenDeclineFixture(t, stuckScreenDeclineSnapshot(screenDeclinePersistentStreak+5))
	doctorJSON = true
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != cli.ExitHealthFailure || !exitErr.Quiet {
		t.Fatalf("err = %+v; want quiet JSON strict health failure", err)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not a single JSON report: %v\n%s", err, out.String())
	}
	found := findingsByCheck(report.Findings, checkScreenPersistentDecline)
	if len(found) != 1 || found[0].Severity != severityFailure || found[0].WindowID != 7 {
		t.Fatalf("screenstate_persistent_decline findings = %+v; want one window-7 failure", found)
	}
	if report.Healthy {
		t.Fatal("a persistent decline failure must make the report unhealthy")
	}
}

// TestDoctorIgnoresRecoveredPersistentScreenDecline is the other half of the
// lifetime: the streak is retained at well past the threshold and its evidence
// is still in the export, but the window has left ⚡ so the probe is no longer
// asked anything about it. The screen classifier cannot be failing a screen it
// is no longer classifying, so the finding has to retract.
//
// The daemon retracts the streak itself when the icon moves, but doctor reads
// one snapshot that may have been captured before that retraction or emitted by
// an older daemon, so these cases are exactly the ones that reach doctor with
// stale evidence intact. Every case here therefore keeps the full
// threshold-crossing decline in the snapshot and changes only the window.
func TestDoctorIgnoresRecoveredPersistentScreenDecline(t *testing.T) {
	// recovered is the stuck snapshot with the retained decline untouched and
	// the window's icon moved off ⚡ — a daemon that has not yet republished.
	recovered := func(icon string) protocol.EventsExport {
		snapshot := stuckScreenDeclineSnapshot(screenDeclinePersistentStreak + 5)
		meta := snapshot.WindowMetadata[7]
		meta.Renderer.DesiredIcon = icon
		meta.Shadow.EffectiveIcon = icon
		snapshot.WindowMetadata[7] = meta
		return snapshot
	}

	cases := []struct {
		name     string
		snapshot protocol.EventsExport
	}{
		{
			// The native Stop hook finally arrived: the turn really did end.
			name:     "native stop",
			snapshot: recovered("stop"),
		},
		{
			// The turn ended in a terminal error instead.
			name:     "terminal error",
			snapshot: recovered("error"),
		},
		{
			// A blocking prompt took the window: attention is owed, but not by
			// the screen classifier.
			name:     "blocking prompt",
			snapshot: recovered("question"),
		},
		{
			// The operator cleared the window, which resets it to no icon.
			name:     "user clear",
			snapshot: recovered(""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setUpStuckScreenDeclineFixture(t, tc.snapshot)
			doctorJSON = true
			doctorStrict = true

			var out bytes.Buffer
			doctorCmd.SetOut(&out)
			err := runDoctor(doctorCmd, nil)

			var report doctorReport
			if jsonErr := json.Unmarshal(out.Bytes(), &report); jsonErr != nil {
				t.Fatalf("output is not a single JSON report: %v\n%s", jsonErr, out.String())
			}
			if found := findingsByCheck(report.Findings, checkScreenPersistentDecline); len(found) != 0 {
				t.Fatalf("recovered window still reported as persistently declining: %+v", found)
			}
			if err != nil {
				t.Fatalf("strict doctor: %v\n%s", err, out.String())
			}
			if !report.Healthy {
				t.Fatalf("report is unhealthy: %+v", report.Findings)
			}
		})
	}
}

func TestScreenDeclineInvariantIgnoresMissingLivePane(t *testing.T) {
	findings := evaluateDaemonInvariants(
		stuckScreenDeclineSnapshot(screenDeclinePersistentStreak+5), ipc.Health{}, doctorTestBuild,
		map[int]protocol.WindowLiveMetadata{7: {Found: false}}, time.Now(),
	)
	if found := findingsByCheck(findings, checkScreenPersistentDecline); len(found) != 0 {
		t.Fatalf("closed pane still reported as persistently declining: %+v", found)
	}
}
