package cmd

// `screenstate_persistent_decline` is the first health finding sourced from the
// daemon watching its own classifier refuse. These tests pin the two halves an
// operator depends on: a pane stuck behind an unchanging screen the classifier
// no longer matches is a strict failure carrying the evidence needed to fix the
// patterns, and every shape that merely resembles it — a short streak, a
// repainting pane, a closed window — stays silent.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// stuckScreenDeclineSnapshot is a live ⚡ window whose probe has refused the
// same frame past the threshold: the synthetic stuck-bolt case.
func stuckScreenDeclineSnapshot(streak int) daemon.EventsExport {
	return daemon.EventsExport{
		WindowMetadata: map[int]daemon.WindowDebugMetadata{
			7: {
				// StateSet stays false so the not-live fixture window does not
				// also trip the renderer-drift check, matching
				// violatingDoctorSnapshot: these tests assert exactly the
				// finding they set up.
				Renderer: daemon.WindowDebugRenderer{DesiredIcon: "bolt"},
				Watchdog: daemon.WindowDebugWatchdog{ScreenDeclines: []daemon.ScreenDeclineSnapshot{{
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

func TestDoctorStrictFailsOnPersistentScreenDecline(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, stuckScreenDeclineSnapshot(screenDeclinePersistentStreak))
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
		snapshot daemon.EventsExport
	}{
		{
			// A pane that is genuinely working repaints its elapsed-time
			// counter, so the fingerprint moves and the streak restarts. This
			// is the ordinary state of every busy window.
			name: "repainting pane never reaches a stable fingerprint",
			snapshot: func() daemon.EventsExport {
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
			snapshot: func() daemon.EventsExport {
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
			snapshot: daemon.EventsExport{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := doctorTestBuild
			health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
			setUpDoctorFixture(t, health, tc.snapshot)
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

// TestDoctorJSONReportsPersistentScreenDecline pins the JSON half of the
// contract: the stable identifier, the failure severity that drives strict
// exit, and the window attribution a consumer routes on.
func TestDoctorJSONReportsPersistentScreenDecline(t *testing.T) {
	build := doctorTestBuild
	health := daemon.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, stuckScreenDeclineSnapshot(screenDeclinePersistentStreak+5))
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
	found := findingsByCheck(report.Findings, checkScreenPersistentDecline)
	if len(found) != 1 || found[0].Severity != severityFailure || found[0].WindowID != 7 {
		t.Fatalf("screenstate_persistent_decline findings = %+v; want one window-7 failure", found)
	}
	if report.Healthy {
		t.Fatal("a persistent decline failure must make the report unhealthy")
	}
}
