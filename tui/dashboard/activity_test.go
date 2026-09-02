package dashboard

import (
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestFormatAgeNoLastAction asserts the detail pane's placeholder dash when
// the watchdog entry carries no LastAction (e.g., a row whose tab is not in a
// sweep-eligible state).
func TestFormatAgeNoLastAction(t *testing.T) {
	if text := FormatAge(protocol.WatchdogState{}, time.Now()); text != "—" {
		t.Errorf("expected dash placeholder, got %q", text)
	}
}

// TestFormatAgeCoarseRounding asserts the detail pane's age label rounds to
// whole minutes below one hour and to whole hours beyond it.
func TestFormatAgeCoarseRounding(t *testing.T) {
	now := time.Now()
	minutes := protocol.WatchdogState{LastAction: "active", LastEventAt: now.Add(-5 * time.Minute).Unix()}
	if text := FormatAge(minutes, now); text != "5m" {
		t.Errorf("expected \"5m\", got %q", text)
	}
	hours := protocol.WatchdogState{LastAction: "subagent", LastEventAt: now.Add(-2 * time.Hour).Unix()}
	if text := FormatAge(hours, now); text != "2h" {
		t.Errorf("expected \"2h\", got %q", text)
	}
}

// TestFormatRunElapsedBoundaries pins the run-timer format at every edge of
// the table: no anchor, the second/minute rollovers, the one-hour switch to
// the compact hour form, a two-digit hour count, the 99-to-100-hour switch to
// the day form and its own saturation past 99 days, and the negative clamp
// that protects against daemon/TUI clock skew. Every case also re-asserts the
// five-cell contract the activity column reserves its width against.
func TestFormatRunElapsedBoundaries(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{"zero seconds", 0, "0:00"},
		{"under one minute", 59 * time.Second, "0:59"},
		{"one minute", time.Minute, "1:00"},
		{"last second under an hour", 59*time.Minute + 59*time.Second, "59:59"},
		{"exactly one hour", time.Hour, "1h00"},
		{"two-digit hours", 10*time.Hour + 7*time.Minute, "10h07"},
		{"last minute under 100 hours", 99*time.Hour + 59*time.Minute, "99h59"},
		{"exactly 100 hours", 100 * time.Hour, "4d04"},
		{"a month of grinding", 30*24*time.Hour + 5*time.Hour + 12*time.Minute, "30d05"},
		{"widest day form", 99*24*time.Hour + 23*time.Hour, "99d23"},
		{"saturates at 100 days", 100 * 24 * time.Hour, "99d+"},
		{"saturates for an absurd stretch", 5000 * 24 * time.Hour, "99d+"},
		{"negative clamps to zero", -30 * time.Second, "0:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRunElapsed(now.Add(-tc.elapsed), now)
			if got != tc.want {
				t.Errorf("formatRunElapsed(%s) = %q, want %q", tc.elapsed, got, tc.want)
			}
			if w := layout.Width(got); w > runTimerMaxCells {
				t.Errorf("formatRunElapsed(%s) = %q occupies %d cells, want at most %d", tc.elapsed, got, w, runTimerMaxCells)
			}
		})
	}
	if got := formatRunElapsed(time.Time{}, now); got != "—" {
		t.Errorf("a row with no anchor = %q, want the dash placeholder", got)
	}

	// The named cases only sample the bands; sweep the whole range so no
	// future band boundary can reintroduce an unbounded leading count.
	for elapsed := time.Second; elapsed < 1<<20*time.Hour; elapsed = elapsed*3/2 + time.Second {
		if w := layout.Width(formatRunElapsed(now.Add(-elapsed), now)); w > runTimerMaxCells {
			t.Fatalf("formatRunElapsed(%s) = %q occupies %d cells, want at most %d", elapsed, formatRunElapsed(now.Add(-elapsed), now), w, runTimerMaxCells)
		}
	}
}

// TestActivityCellTextWarningRequiresTimer asserts the staleness warning keeps
// its 80%-of-threshold rule but never fires on a row without a timer: the
// reduced icon is authoritative, so a diverged LastAction scalar must not
// decorate an idle row.
func TestActivityCellTextWarningRequiresTimer(t *testing.T) {
	now := time.Now()
	busy := session.RunningSession{WindowID: 1, BusySince: now.Add(-90 * time.Second)}
	idle := session.RunningSession{WindowID: 2}
	onBand := protocol.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-8 * time.Minute).Unix(), // 8m of 10m is exactly the band
		ThresholdSec: 600,
	}

	text, warning := activityCellText(busy, onBand, now)
	if !warning {
		t.Error("warning must fire at 80% of threshold on a row with a timer")
	}
	if want := "1:30 " + ageWarningGlyph; text != want {
		t.Errorf("warning text = %q, want %q", text, want)
	}

	text, warning = activityCellText(idle, onBand, now)
	if warning {
		t.Error("warning must not fire on a row with no timer")
	}
	if text != "—" {
		t.Errorf("idle text = %q, want the dash placeholder", text)
	}

	belowBand := protocol.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-5 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	if _, warning = activityCellText(busy, belowBand, now); warning {
		t.Error("warning must not fire at 50% of threshold")
	}

	noThreshold := protocol.WatchdogState{LastAction: "active", LastEventAt: now.Add(-2 * time.Hour).Unix()}
	if _, warning = activityCellText(busy, noThreshold, now); warning {
		t.Error("warning must not fire when ThresholdSec is zero")
	}
}

// TestRenderActivityCellWarningStyled asserts the warning glyph appears and
// the cell carries lipgloss styling (ANSI escape) when the warning band is
// hit, that a non-warning cell renders plain text, and that both pad to the
// width the render pass chose.
func TestRenderActivityCellWarningStyled(t *testing.T) {
	now := time.Now()
	busy := session.RunningSession{WindowID: 1, BusySince: now.Add(-90 * time.Second)}
	width := 8

	wsWarn := protocol.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-9 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	cell := renderActivityCell(busy, wsWarn, now, width)
	plain := stripAnsi(cell)
	if !strings.Contains(plain, ageWarningGlyph) {
		t.Errorf("warning cell missing ⏳ glyph; got %q", plain)
	}
	if cell == plain {
		t.Errorf("warning cell expected ANSI styling; got plain %q", cell)
	}
	if got := runewidth.StringWidth(plain); got != width {
		t.Errorf("warning cell width = %d, want %d (%q)", got, width, plain)
	}

	wsOk := protocol.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-1 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	cellOk := renderActivityCell(busy, wsOk, now, width)
	if strings.Contains(cellOk, ageWarningGlyph) {
		t.Errorf("non-warning cell must not include ⏳; got %q", cellOk)
	}
	if got := runewidth.StringWidth(stripAnsi(cellOk)); got != width {
		t.Errorf("non-warning cell width = %d, want %d (%q)", got, width, cellOk)
	}
}

// TestAnyRowHasActivityFalseWhenNoRowIsBusy asserts the column reservation
// predicate ignores watchdog entries entirely: a sweep-eligible row with no
// busy anchor (a pre-upgrade daemon, or a window paused on a prompt) must not
// reserve the column.
func TestAnyRowHasActivityFalseWhenNoRowIsBusy(t *testing.T) {
	in := ListInput{
		Cfg: &config.Config{},
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt"},
		},
		Watchdog: map[int]protocol.WatchdogState{
			1: {LastAction: "active", LastEventAt: time.Now().Unix(), ThresholdSec: 600},
		},
	}
	if anyRowHasActivity(in) {
		t.Error("anyRowHasActivity must return false when no row carries a BusySince")
	}
}

// TestAnyRowHasActivityTrueWhenAnyRowIsBusy asserts the predicate flips to
// true as soon as one filtered row is running a busy stretch.
func TestAnyRowHasActivityTrueWhenAnyRowIsBusy(t *testing.T) {
	in := ListInput{
		Cfg: &config.Config{},
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt", BusySince: time.Now()},
			{WindowID: 2, Base: "b", State: ""},
		},
	}
	if !anyRowHasActivity(in) {
		t.Error("anyRowHasActivity must return true when at least one row is busy")
	}
}

// TestHarnessLabel verifies the full-name labels and the empty fallback.
func TestHarnessLabel(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude", "claude"},
		{"Claude", "claude"},
		{"CLAUDE", "claude"},
		{"codex", "codex"},
		{"Codex", "codex"},
		{"copilot", "copilot"},
		{"Copilot", "copilot"},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		got := harnessLabel(tc.input)
		if got != tc.want {
			t.Errorf("harnessLabel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
