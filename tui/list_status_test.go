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

// TestFormatAgeNoLastAction asserts the placeholder dash and warning=false
// when the watchdog entry carries no LastAction (e.g., a row whose tab is
// not in a sweep-eligible state).
func TestFormatAgeNoLastAction(t *testing.T) {
	text, warning := formatAge(daemon.WatchdogState{}, time.Now())
	if text != "—" {
		t.Errorf("expected dash placeholder, got %q", text)
	}
	if warning {
		t.Error("warning must not fire when LastAction is empty")
	}
}

// TestFormatAgeWithinThreshold asserts coarse minute rounding and that the
// warning flag stays off while elapsed sits below the 80% band.
func TestFormatAgeWithinThreshold(t *testing.T) {
	now := time.Now()
	ws := daemon.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-5 * time.Minute).Unix(),
		ThresholdSec: 600, // 10 min — 5 min is 50% of threshold, well below warning band
	}
	text, warning := formatAge(ws, now)
	if text != "5m" {
		t.Errorf("expected \"5m\", got %q", text)
	}
	if warning {
		t.Error("warning must not fire at 50%% of threshold")
	}
}

// TestFormatAgeWarningBand asserts that warning flips to true once elapsed
// reaches 80% of the resolved threshold. 8m of 10m = exactly 80%, on the band.
func TestFormatAgeWarningBand(t *testing.T) {
	now := time.Now()
	ws := daemon.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-8 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	text, warning := formatAge(ws, now)
	if text != "8m" {
		t.Errorf("expected \"8m\", got %q", text)
	}
	if !warning {
		t.Error("warning must fire at exactly 80%% of threshold")
	}
}

// TestFormatAgeBeyondHour asserts the format switches to "<n>h" once the
// elapsed time crosses one hour. A 2h-old entry with no threshold has
// warning=false because ThresholdSec=0 disables the warning gate.
func TestFormatAgeBeyondHour(t *testing.T) {
	now := time.Now()
	ws := daemon.WatchdogState{
		LastAction:  "subagent",
		LastEventAt: now.Add(-2 * time.Hour).Unix(),
	}
	text, warning := formatAge(ws, now)
	if text != "2h" {
		t.Errorf("expected \"2h\", got %q", text)
	}
	if warning {
		t.Error("warning must not fire when ThresholdSec is zero")
	}
}

// TestRenderAgeCellWarningStyled asserts the warning glyph appears and the
// cell carries lipgloss styling (ANSI escape) when the warning band is hit.
// A non-warning cell renders plain text.
func TestRenderAgeCellWarningStyled(t *testing.T) {
	now := time.Now()
	wsWarn := daemon.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-9 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	cell := renderAgeCell(wsWarn, now)
	plain := stripAnsi(cell)
	if !strings.Contains(plain, ageWarningGlyph) {
		t.Errorf("warning cell missing ⏳ glyph; got %q", plain)
	}
	if cell == plain {
		t.Errorf("warning cell expected ANSI styling; got plain %q", cell)
	}
	if got := runewidth.StringWidth(plain); got != ageColumnWidth {
		t.Errorf("warning cell width = %d, want %d (%q)", got, ageColumnWidth, plain)
	}

	wsOk := daemon.WatchdogState{
		LastAction:   "active",
		LastEventAt:  now.Add(-1 * time.Minute).Unix(),
		ThresholdSec: 600,
	}
	cellOk := renderAgeCell(wsOk, now)
	if strings.Contains(cellOk, ageWarningGlyph) {
		t.Errorf("non-warning cell must not include ⏳; got %q", cellOk)
	}
	if got := runewidth.StringWidth(stripAnsi(cellOk)); got != ageColumnWidth {
		t.Errorf("non-warning cell width = %d, want %d (%q)", got, ageColumnWidth, cellOk)
	}
}

// TestAnyRowHasAgeFalseWhenNoLastAction asserts the column reservation
// predicate returns false when every watchdog entry has an empty LastAction
// (the daemon shouldn't emit such entries today, but the predicate must
// tolerate the case so a daemon downgrade doesn't permanently reserve the
// column).
func TestAnyRowHasAgeFalseWhenNoLastAction(t *testing.T) {
	m := Model{
		cfg: &config.Config{},
		filtered: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt"},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "", LastEventAt: time.Now().Unix()},
		},
	}
	if anyRowHasAge(m) {
		t.Error("anyRowHasAge must return false when no row has a LastAction")
	}
}

// TestAnyRowHasAgeTrueWhenAnyRowHasLastAction asserts the predicate flips
// to true as soon as one filtered row has a sweep-eligible watchdog entry.
func TestAnyRowHasAgeTrueWhenAnyRowHasLastAction(t *testing.T) {
	m := Model{
		cfg: &config.Config{},
		filtered: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt"},
			{WindowID: 2, Base: "b", State: ""},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: time.Now().Unix(), ThresholdSec: 600},
		},
	}
	if !anyRowHasAge(m) {
		t.Error("anyRowHasAge must return true when at least one row carries LastAction")
	}
}

// TestRenderListAgeColumnAppears verifies that renderList reserves the age
// column when the model carries a watchdog entry with a LastAction. The
// rendered output must contain the minute label so the column is visibly
// present beside the row.
func TestRenderListAgeColumnAppears(t *testing.T) {
	now := time.Now()
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Add(-3 * time.Minute).Unix(), ThresholdSec: 600},
		},
	}
	m.filtered = m.sessions
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "3m") {
		t.Errorf("expected \"3m\" age cell in render; got %q", out)
	}
}

// TestRenderListAgeColumnHiddenByDefault asserts that with no watchdog
// entries, the age column is omitted entirely so rows recover their full
// directory budget. The "—" placeholder must NOT appear because the column
// itself is not reserved.
func TestRenderListAgeColumnHiddenByDefault(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
		},
	}
	m.filtered = m.sessions
	if anyRowHasAge(m) {
		t.Fatal("baseline model with nil watchdog should not reserve age column")
	}
	out := stripAnsi(renderList(m))
	if strings.Contains(out, "—") {
		t.Errorf("dash placeholder must not appear when column is hidden; got %q", out)
	}
}

// TestRenderListAgeColumnRendersDashForRowsWithoutWatchdog asserts that
// once the column is reserved (because at least one row has a watchdog
// entry), the other rows render the "—" placeholder so cells stay aligned.
func TestRenderListAgeColumnRendersDashForRowsWithoutWatchdog(t *testing.T) {
	now := time.Now()
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
			{WindowID: 2, Base: "web", State: "bolt", Account: "work"},
		},
		watchdog: map[int]daemon.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Add(-2 * time.Minute).Unix(), ThresholdSec: 600},
		},
	}
	m.filtered = m.sessions
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "—") {
		t.Errorf("expected dash placeholder on the row without watchdog; got %q", out)
	}
	if !strings.Contains(out, "2m") {
		t.Errorf("expected \"2m\" on the row with watchdog; got %q", out)
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
