package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// scrollModel returns a baseline Model parked in ModeList with a generous
// height so the legend renders and footerBudgetLines reports the non-preview
// default. Tests override individual fields to flip a single chrome element
// or shrink the viewport.
func scrollModel() Model {
	m := testModel()
	m.height = 40
	m.width = 100
	return m
}

func TestListChromeLines_BaselineModeList(t *testing.T) {
	m := scrollModel()
	footerLines := footerBudgetLines(m)
	if renderedLines := len(rootFooterLines(m)); footerLines != renderedLines {
		t.Fatalf("footer budget = %d, rendered footer rows = %d", footerLines, renderedLines)
	}
	// Baseline: header(1) + wrapped legend + spacer(1) + padding row(1)
	// + the measured reflowed footer + status bar(1).
	got := listChromeLines(m)
	want := 1 + legendLineCount(m) + 1 + 1 + footerLines + statusBarLines
	if got != want {
		t.Errorf("baseline chrome: got %d want %d", got, want)
	}
}

func TestListFooterBudgetMatchesRenderedRowsAcrossStates(t *testing.T) {
	base := scrollModel()
	base.width = 72
	base.height = 18
	base.filtered = makeRowsForScroll(20)

	filtered := base
	filtered.activeFilter = dashboard.Filter{Substrings: []string{"api"}}
	jump := base
	jump.pendingJumpDir = 1

	for _, tc := range []struct {
		name string
		m    Model
	}{
		{name: "overflowing list", m: base},
		{name: "active filter", m: filtered},
		{name: "jump chord", m: jump},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := footerBudgetLines(tc.m)
			want := len(rootFooterLines(tc.m))
			if got != want {
				t.Fatalf("footer budget = %d, rendered footer rows = %d", got, want)
			}
		})
	}
}

func TestListChromeLines_DeprecationBannerAddsTwoLines(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	m.deprecations = []config.Deprecation{{Key: "foo", Replacement: "bar"}}
	got := listChromeLines(m)
	if got != base+2 {
		t.Errorf("with deprecation banner: got %d want %d", got, base+2)
	}
	// Dismissed banner contributes 0 lines.
	m.deprecationDismissed = true
	if got := listChromeLines(m); got != base {
		t.Errorf("dismissed deprecation banner: got %d want %d", got, base)
	}
}

func TestListChromeLines_HealthHintsAddHeaderLines(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	now := time.Now()
	m.daemonAlive = false
	m.daemonHealthCheckedAt = now
	m.hookHealth = HookHealth{Drifted: []string{"work"}}
	m.hookHealthCheckedAt = now

	got := listChromeLines(m)
	if got != base+2 {
		t.Errorf("with daemon and hook health hints: got %d want %d", got, base+2)
	}
	if hints := healthHintLines(m); hints != 2 {
		t.Errorf("health hint count: got %d want 2", hints)
	}

	m.daemonAlive = true
	m.hookHealth = HookHealth{InSync: []string{"work"}}
	if got := listChromeLines(m); got != base {
		t.Errorf("healthy pips should not reserve hint lines: got %d want %d", got, base)
	}
}

func TestListChromeLines_LegendDroppedBelowLegendMinHeight(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	legendLines := legendLineCount(m)
	m.height = legendMinHeight - 1 // legend omitted
	got := listChromeLines(m)
	if got != base-legendLines {
		t.Errorf("legend hidden on short term: got %d want %d", got, base-legendLines)
	}
}

func TestListChromeLines_ChipsAddOneLine(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	m.activeFilter = dashboard.Filter{Substrings: []string{"api"}}
	got := listChromeLines(m)
	if got != base+1 {
		t.Errorf("with chips: got %d want %d", got, base+1)
	}
}

func TestListChromeLines_SpacerDropsOutsideModeList(t *testing.T) {
	m := scrollModel()
	listFooterLines := footerBudgetLines(m)
	base := listChromeLines(m)
	// Outside ModeList the spacer line is not emitted; the footer
	// also flips back to its default but for this test we only assert the
	// spacer delta by holding everything else constant.
	m.mode = ModeHelp
	got := listChromeLines(m)
	want := base - 1 - listFooterLines + footerBudgetLines(m)
	if got != want {
		t.Errorf("spacer outside ModeList: got %d want %d", got, want)
	}
}

func TestListVisibleHeight_PreviewOffSubtractsChrome(t *testing.T) {
	m := scrollModel()
	chrome := listChromeLines(m)
	want := m.height - chrome
	if got := listVisibleHeight(m); got != want {
		t.Errorf("preview off: got %d want %d", got, want)
	}
}

func TestListVisibleHeight_PreviewVerticalMatchesRenderSplit(t *testing.T) {
	m := scrollModel()
	m.cfg.Defaults.Preview = config.PreviewConfig{Direction: "vertical", Ratio: 0.5}
	m.previewEnabled = true

	footerLines := footerBudgetLines(m)
	paneHeight := m.height - chromeAboveListLines(m) - 1 - footerLines - statusBarLines
	previewHeight := int(float64(paneHeight) * 0.5)
	if previewHeight < 3 {
		previewHeight = 3
	}
	if previewHeight > paneHeight-2 {
		previewHeight = paneHeight - 2
	}
	want := paneHeight - previewHeight - 1

	if got := listVisibleHeight(m); got != want {
		t.Errorf("preview vertical: got %d want %d (pane=%d preview=%d)", got, want, paneHeight, previewHeight)
	}
}

func TestListVisibleHeight_PreviewHorizontalReturnsPaneHeight(t *testing.T) {
	m := scrollModel()
	m.cfg.Defaults.Preview = config.PreviewConfig{Direction: "horizontal", Ratio: 0.5}
	m.previewEnabled = true

	footerLines := footerBudgetLines(m)
	want := m.height - chromeAboveListLines(m) - 1 - footerLines - statusBarLines

	if got := listVisibleHeight(m); got != want {
		t.Errorf("preview horizontal: got %d want %d", got, want)
	}
}

func TestListVisibleHeight_FloorAtListMinVisible(t *testing.T) {
	m := scrollModel()
	m.height = 2 // chrome wildly exceeds the height
	if got := listVisibleHeight(m); got != listMinVisible {
		t.Errorf("floor: got %d want %d", got, listMinVisible)
	}
}

func TestClampListOffset_PinsToValidRange(t *testing.T) {
	m := scrollModel()
	m.height = 15 // small enough that total > visible budget
	m.filtered = makeRowsForScroll(40)

	visible := listVisibleHeight(m)
	maxOffset := len(m.filtered) - visible
	if maxOffset <= 0 {
		t.Fatalf("test fixture must keep total > visible: total=%d visible=%d",
			len(m.filtered), visible)
	}

	// Offset above max collapses to max.
	m.listOffset = maxOffset + 7
	clampListOffset(&m)
	if m.listOffset != maxOffset {
		t.Errorf("over-max: got %d want %d", m.listOffset, maxOffset)
	}

	// Negative offset clamps to 0.
	m.listOffset = -5
	clampListOffset(&m)
	if m.listOffset != 0 {
		t.Errorf("negative offset: got %d want 0", m.listOffset)
	}
}

func TestClampListOffset_ZeroWhenTotalFits(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(2) // fewer rows than the visible budget
	m.listOffset = 4
	clampListOffset(&m)
	if m.listOffset != 0 {
		t.Errorf("total <= visible: got offset %d want 0", m.listOffset)
	}
}

func TestClampListOffset_ShrinkPullsOffsetBack(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(40)
	visible := listVisibleHeight(m)
	m.listOffset = len(m.filtered) - visible // valid offset at full size

	// Simulate a refresh that shrinks the list (e.g. a session was closed).
	m.filtered = m.filtered[:6]
	clampListOffset(&m)
	if m.listOffset != 0 {
		t.Errorf("after shrink to %d rows: got offset %d want 0", len(m.filtered), m.listOffset)
	}
}

func TestScrollListToCursor_CursorBeforeWindowMovesOffsetDown(t *testing.T) {
	m := scrollModel()
	m.height = 15 // small enough that the visible budget is bounded
	m.filtered = makeRowsForScroll(30)
	m.listOffset = 10
	m.cursor = 4 // cursor sits above the window

	scrollListToCursor(&m)
	if m.listOffset != 4 {
		t.Errorf("cursor before window: got offset %d want 4", m.listOffset)
	}
}

func TestScrollListToCursor_CursorAfterWindowAdvancesOffset(t *testing.T) {
	m := scrollModel()
	m.height = 15 // tight visible budget so the cursor lands past the window
	m.filtered = makeRowsForScroll(50)
	visible := listVisibleHeight(m)
	m.listOffset = 0
	m.cursor = visible + 5 // cursor sits past the window's bottom row

	scrollListToCursor(&m)

	// With no group labels every row costs 1 line; cursor must end up within
	// the budget [offset, offset+visible).
	if m.cursor < m.listOffset || m.cursor >= m.listOffset+visible {
		t.Errorf("cursor not in window after scroll: cursor=%d offset=%d visible=%d",
			m.cursor, m.listOffset, visible)
	}
	wantMinOffset := m.cursor - visible + 1
	if m.listOffset < wantMinOffset {
		t.Errorf("offset advanced too little: got %d want >= %d", m.listOffset, wantMinOffset)
	}
}

func TestScrollListToCursor_GroupLabelChargedCorrectly(t *testing.T) {
	m := scrollModel()
	// Two groups: group 0 (state="stop", needs attention) then group 1
	// (state="bolt", active). With multiGroup=true the renderer emits a
	// label before the first row of each group and a blank separator before
	// subsequent visible groups, so a slice that contains both groups costs
	// (rows + labels + separators) lines while a slice from a single group
	// costs (rows + 1) lines.
	m.filtered = []session.RunningSession{
		{WindowID: 1, Base: "a1", Account: "work", State: "stop"},
		{WindowID: 2, Base: "a2", Account: "work", State: "stop"},
		{WindowID: 3, Base: "b1", Account: "work", State: "bolt"},
		{WindowID: 4, Base: "b2", Account: "work", State: "bolt"},
		{WindowID: 5, Base: "b3", Account: "work", State: "bolt"},
	}
	m.lastActivity = map[int]time.Time{}

	if g := dashboard.CountGroups(m.filtered, m.lastActivity, m.idleThreshold); g <= 1 {
		t.Fatalf("test fixture must span multiple groups: countGroups=%d", g)
	}

	// listLineCost mirrors the renderer: prevGroup=-1 at offset so the first
	// row always picks up a label. Slice [0..0] crosses group boundary 0→0
	// (from -1) once → cost = 1 (label) + 1 (row) = 2.
	if got := listLineCost(&m, 0, 0, true); got != 2 {
		t.Errorf("single-row, single-group cost: got %d want 2", got)
	}

	// Slice [0..2] spans groups 0 and 1: rows=3, labels=2, separators=1 → cost=6.
	if got := listLineCost(&m, 0, 2, true); got != 6 {
		t.Errorf("two-group slice cost: got %d want 6", got)
	}

	// Slice [2..4] stays within group 1: rows=3, labels=1 → cost=4.
	if got := listLineCost(&m, 2, 4, true); got != 4 {
		t.Errorf("single-group slice cost: got %d want 4", got)
	}

	// scrollListToCursor must still place the cursor within the window even
	// when group labels eat into the line budget. Use a tight visible budget
	// so the label cost actually forces an extra offset advance.
	m.height = listMinVisible + 8 // tight terminal
	m.cursor = 4                  // last row
	m.listOffset = 0
	scrollListToCursor(&m)
	if m.listOffset > m.cursor {
		t.Errorf("offset overshot cursor: offset=%d cursor=%d", m.listOffset, m.cursor)
	}
	// The total line cost from the resulting offset through cursor must fit
	// within the visible budget.
	visible := listVisibleHeight(m)
	if cost := listLineCost(&m, m.listOffset, m.cursor, true); cost > visible {
		t.Errorf("cursor cost exceeds budget after scroll: cost=%d visible=%d offset=%d",
			cost, visible, m.listOffset)
	}
}

// TestScrollListToCursor_PreviewEnabledKeepsCursorVisible reconstructs the
// screenshot scenario reported against commit 1802fa2: preview pane on,
// multi-group filtered list, cursor on a row past the visible window. Before
// the fix, listVisibleHeight (and renderSplit) over-counted the available
// height by the size of the chrome above the list (legend + spacer +
// banner + chips), so scrollListToCursor thought the cursor fit when in
// practice the terminal clipped it. This test fails on the buggy
// `m.height - 1 - 1 - footerLines` formula and passes after the chrome is
// subtracted properly.
func TestScrollListToCursor_PreviewEnabledKeepsCursorVisible(t *testing.T) {
	m := scrollModel()
	m.cfg.Defaults.Preview = config.PreviewConfig{Direction: "vertical", Ratio: 0.5}
	m.previewEnabled = true
	// Mirror the screenshot: a tight terminal where chrome consumes enough
	// lines that ignoring it inflates the budget by several rows.
	m.height = 26
	m.filtered = []session.RunningSession{
		{WindowID: 1, Base: "amx", Account: "work", State: "stop"},
		{WindowID: 2, Base: "AiBroker", Account: "work", State: "stop"},
		{WindowID: 3, Base: "diffusion", Account: "work", State: "stop"},
		{WindowID: 4, Base: "Katana", Account: "work", State: "stop"},
		{WindowID: 5, Base: "sis", Account: "work", State: "key"},
		{WindowID: 6, Base: "Excel", Account: "work", State: "bolt"},
		{WindowID: 7, Base: "act2", Account: "work", State: "bolt"},
		{WindowID: 8, Base: "act3", Account: "work", State: "bolt"},
		{WindowID: 9, Base: "act4", Account: "work", State: "bolt"},
		{WindowID: 10, Base: "act5", Account: "work", State: "bolt"},
		{WindowID: 11, Base: "act6", Account: "work", State: "bolt"},
		{WindowID: 12, Base: "act7", Account: "work", State: "bolt"},
	}
	m.lastActivity = map[int]time.Time{}

	if g := dashboard.CountGroups(m.filtered, m.lastActivity, m.idleThreshold); g <= 1 {
		t.Fatalf("test fixture must span multiple groups: countGroups=%d", g)
	}

	visible := listVisibleHeight(m)
	if visible >= len(m.filtered) {
		t.Fatalf("test fixture must overflow the budget: total=%d visible=%d",
			len(m.filtered), visible)
	}

	m.cursor = len(m.filtered) - 1 // last row, past the visible window
	m.listOffset = 0
	scrollListToCursor(&m)

	// After the scroll, the line cost from offset through cursor — including
	// any group-label charges — must fit within the visible budget so the
	// renderer can actually emit the cursor's row.
	if cost := listLineCost(&m, m.listOffset, m.cursor, true); cost > visible {
		t.Errorf("cursor not in window after scroll with preview enabled: "+
			"cost=%d visible=%d offset=%d cursor=%d",
			cost, visible, m.listOffset, m.cursor)
	}

	out := stripAnsi(renderListAt(m, m.width, visible))
	if !strings.Contains(out, "act7") {
		t.Errorf("selected cursor row was not rendered after scroll; offset=%d cursor=%d visible=%d output=%q",
			m.listOffset, m.cursor, visible, out)
	}
}

func TestScrollListToCursor_ReservesBottomIndicatorWhenRowsRemain(t *testing.T) {
	m := scrollModel()
	m.height = 15 // short-terminal chrome leaves a 6-line list pane
	m.filtered = makeRowsForScroll(20)
	m.cursor = 10
	m.listOffset = 0

	visible := listVisibleHeight(m)
	if visible != 6 {
		t.Fatalf("test fixture expected visible=6, got %d", visible)
	}

	scrollListToCursor(&m)

	out := stripAnsi(renderListAt(m, m.width, visible))
	selected := indexedRowName(m.cursor)
	if !strings.Contains(out, selected) {
		t.Errorf("selected row %s was not rendered with bottom indicator; offset=%d cursor=%d output=%q",
			selected, m.listOffset, m.cursor, out)
	}
	if !strings.Contains(out, "▲") || !strings.Contains(out, "▼") {
		t.Errorf("fixture should render both scroll indicators; got %q", out)
	}
}

// makeRowsForScroll builds n synthetic running-sessions in a single group
// (all idle) so listLineCost/scrollListToCursor tests do not need to reason
// about group-label charges. Group-label behavior is exercised separately.
// Each row carries a unique Base ("row001", "row002", ...) so renderer tests
// can assert on which rows landed in the windowed output.
func makeRowsForScroll(n int) []session.RunningSession {
	out := make([]session.RunningSession, n)
	for i := 0; i < n; i++ {
		out[i] = session.RunningSession{
			WindowID:  i + 1,
			Base:      indexedRowName(i),
			Account:   "work",
			Directory: "/tmp",
			State:     "bolt", // group 1 (active)
		}
	}
	return out
}

// indexedRowName returns a zero-padded row name like "row007" so renderer
// tests can substring-match against individual rows without false hits from
// numeric prefixes (e.g. "row1" matching inside "row10").
func indexedRowName(i int) string {
	const padded = "000"
	s := itoaForTest(i + 1)
	if len(s) < len(padded) {
		s = padded[:len(padded)-len(s)] + s
	}
	return "row" + s
}

// itoaForTest is a tiny strconv.Itoa to keep the test file's import set
// unchanged and avoid pulling strconv just for fixture names.
func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
