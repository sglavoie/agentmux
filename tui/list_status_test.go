package tui

import (
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestRenderDeprecationBannerShownWhenDeprecationsPresent verifies the
// banner renders headline + hint when the config has at least one
// deprecated key and the user has not dismissed it.
func TestRenderDeprecationBannerShownWhenDeprecationsPresent(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles", Location: "[[accounts]]"},
		},
	}
	out := stripAnsi(renderDeprecationBanner(m))
	if !strings.Contains(out, "deprecated keys") {
		t.Errorf("expected headline; got %q", out)
	}
	if !strings.Contains(out, "accounts → profiles") {
		t.Errorf("expected migration arrow; got %q", out)
	}
	if !strings.Contains(out, "press d to dismiss") {
		t.Errorf("expected dismiss hint; got %q", out)
	}
}

// TestRenderDeprecationBannerHiddenWhenDismissed verifies that setting
// deprecationDismissed suppresses the banner even when deprecations are
// recorded — the dismissal is session-scoped, not config-scoped.
func TestRenderDeprecationBannerHiddenWhenDismissed(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles"},
		},
		deprecationDismissed: true,
	}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner after dismiss; got %q", got)
	}
}

// TestRenderDeprecationBannerHiddenWhenNoDeprecations covers the common
// case where the user is on a clean canonical config — no banner ever
// appears.
func TestRenderDeprecationBannerHiddenWhenNoDeprecations(t *testing.T) {
	m := Model{width: 80}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner with no deprecations; got %q", got)
	}
}

// TestRenderHealthPipsHiddenBeforeFirstCheck verifies that the pip strip
// is suppressed until at least one health refresh has completed so the
// header doesn't briefly mis-render as "all red" at startup.
func TestRenderHealthPipsHiddenBeforeFirstCheck(t *testing.T) {
	m := Model{width: 80}
	if got := renderHealthPips(m); got != "" {
		t.Errorf("expected no pips before first check; got %q", got)
	}
}

// TestRenderHealthPipsShowsBothPips verifies that after a health refresh
// both daemon and hooks pips render with their labels.
func TestRenderHealthPipsShowsBothPips(t *testing.T) {
	m := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: time.Now(),
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   time.Now(),
	}
	out := stripAnsi(renderHealthPips(m))
	if !strings.Contains(out, "daemon") {
		t.Errorf("expected daemon label; got %q", out)
	}
	if !strings.Contains(out, "hooks") {
		t.Errorf("expected hooks label; got %q", out)
	}
	if !strings.Contains(out, healthPipGlyph) {
		t.Errorf("expected pip glyph; got %q", out)
	}
}

// TestRenderHealthHintsShownWhenDegraded verifies that the under-pip hint
// surfaces both a daemon-down and a hooks-drifted message when applicable,
// while staying silent when everything is healthy.
func TestRenderHealthHintsShownWhenDegraded(t *testing.T) {
	now := time.Now()
	m := Model{
		width:                 80,
		daemonAlive:           false,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{Drifted: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	out := stripAnsi(renderHealthHints(m))
	if !strings.Contains(out, "daemon down") {
		t.Errorf("expected daemon-down hint; got %q", out)
	}
	if !strings.Contains(out, "hooks drifted") {
		t.Errorf("expected hooks-drifted hint; got %q", out)
	}

	healthy := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	if got := renderHealthHints(healthy); got != "" {
		t.Errorf("expected empty hints when healthy; got %q", got)
	}
}

// TestFormatAgeNoLastAction asserts the detail pane's placeholder dash when
// the watchdog entry carries no LastAction (e.g., a row whose tab is not in a
// sweep-eligible state).
func TestFormatAgeNoLastAction(t *testing.T) {
	if text := formatAge(daemon.WatchdogState{}, time.Now()); text != "—" {
		t.Errorf("expected dash placeholder, got %q", text)
	}
}

// TestFormatAgeCoarseRounding asserts the detail pane's age label rounds to
// whole minutes below one hour and to whole hours beyond it.
func TestFormatAgeCoarseRounding(t *testing.T) {
	now := time.Now()
	minutes := daemon.WatchdogState{LastAction: "active", LastEventAt: now.Add(-5 * time.Minute).Unix()}
	if text := formatAge(minutes, now); text != "5m" {
		t.Errorf("expected \"5m\", got %q", text)
	}
	hours := daemon.WatchdogState{LastAction: "subagent", LastEventAt: now.Add(-2 * time.Hour).Unix()}
	if text := formatAge(hours, now); text != "2h" {
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
			if w := displayWidth(got); w > runTimerMaxCells {
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
		if w := displayWidth(formatRunElapsed(now.Add(-elapsed), now)); w > runTimerMaxCells {
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
	onBand := daemon.WatchdogState{
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

	belowBand := daemon.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-5 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	if _, warning = activityCellText(busy, belowBand, now); warning {
		t.Error("warning must not fire at 50% of threshold")
	}

	noThreshold := daemon.WatchdogState{LastAction: "active", LastEventAt: now.Add(-2 * time.Hour).Unix()}
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

	wsWarn := daemon.WatchdogState{
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

	wsOk := daemon.WatchdogState{
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
	m := Model{
		cfg: &config.Config{},
		filtered: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt"},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: time.Now().Unix(), ThresholdSec: 600},
		},
	}
	if anyRowHasActivity(m) {
		t.Error("anyRowHasActivity must return false when no row carries a BusySince")
	}
}

// TestAnyRowHasActivityTrueWhenAnyRowIsBusy asserts the predicate flips to
// true as soon as one filtered row is running a busy stretch.
func TestAnyRowHasActivityTrueWhenAnyRowIsBusy(t *testing.T) {
	m := Model{
		cfg: &config.Config{},
		filtered: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt", BusySince: time.Now()},
			{WindowID: 2, Base: "b", State: ""},
		},
	}
	if !anyRowHasActivity(m) {
		t.Error("anyRowHasActivity must return true when at least one row is busy")
	}
}

// TestRenderListActivityColumnAppears verifies that renderList reserves the
// activity column when a row carries a busy anchor, and renders the elapsed
// stretch rather than the time since the last hook event.
func TestRenderListActivityColumnAppears(t *testing.T) {
	now := time.Now()
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work", BusySince: now.Add(-(3*time.Minute + 7500*time.Millisecond))},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Unix(), ThresholdSec: 600},
		},
	}
	m.filtered = m.sessions
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "3:07") {
		t.Errorf("expected \"3:07\" run timer in render; got %q", out)
	}
}

// TestRenderListActivityColumnHiddenByDefault asserts that with no busy rows
// the column is omitted entirely so rows recover their full directory budget.
// The "—" placeholder must NOT appear because the column itself is not
// reserved.
func TestRenderListActivityColumnHiddenByDefault(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
		},
	}
	m.filtered = m.sessions
	if anyRowHasActivity(m) {
		t.Fatal("baseline model with no busy rows should not reserve the activity column")
	}
	out := stripAnsi(renderList(m))
	if strings.Contains(out, "—") {
		t.Errorf("dash placeholder must not appear when column is hidden; got %q", out)
	}
}

// TestRenderListActivityColumnSharedWidthAcrossRows asserts that once the
// column is reserved, an idle row renders the "—" placeholder and every row
// pays the same column width, so the directory column starts at one offset
// regardless of how wide the busiest row's timer is. The third row carries the
// widest representation formatRunElapsed can produce, pinning that even a
// multi-month stretch cannot widen the column past a warning-decorated timer.
func TestRenderListActivityColumnSharedWidthAcrossRows(t *testing.T) {
	now := time.Now()
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work", Directory: "~/p", BusySince: now.Add(-(12*time.Hour + 7*time.Minute + 30*time.Second))},
			{WindowID: 2, Base: "web", State: "bolt", Account: "work", Directory: "~/p"},
			{WindowID: 3, Base: "etl", State: "bolt", Account: "work", Directory: "~/p", BusySince: now.Add(-(99*24*time.Hour + 23*time.Hour))},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Add(-9 * time.Minute).Unix(), ThresholdSec: 600},
		},
	}
	m.filtered = m.sessions

	if got, want := activityColumnWidth(m, now), displayWidth("12h07 "+ageWarningGlyph)+1; got != want {
		t.Fatalf("activityColumnWidth = %d, want %d (the widest cell of the pass plus its trailing space)", got, want)
	}

	lines := strings.Split(strings.TrimRight(stripAnsi(renderList(m)), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 rendered rows, got %d: %q", len(lines), lines)
	}
	busy, idle, long := lines[0], lines[1], lines[2]
	if !strings.Contains(busy, "12h07") {
		t.Errorf("expected \"12h07\" on the busy row; got %q", busy)
	}
	if !strings.Contains(idle, "—") {
		t.Errorf("expected dash placeholder on the idle row; got %q", idle)
	}
	if !strings.Contains(long, "99d23") {
		t.Errorf("expected \"99d23\" on the long-running row; got %q", long)
	}
	busyDir := strings.Index(busy, "~/p")
	for _, row := range []struct {
		name string
		line string
	}{{"idle", idle}, {"long-running", long}} {
		dir := strings.Index(row.line, "~/p")
		if busyDir < 0 || dir < 0 {
			t.Fatalf("directory column missing: busy=%q %s=%q", busy, row.name, row.line)
		}
		if got, want := displayWidth(row.line[:dir]), displayWidth(busy[:busyDir]); got != want {
			t.Errorf("directory starts at cell %d on the %s row and %d on the busy row; the activity column must share one width", got, row.name, want)
		}
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
