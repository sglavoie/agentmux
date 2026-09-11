package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
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
	// Baseline: header(1) + spacer(1) + padding row(1) + the measured footer
	// + the feedback row(1). Nothing sits below the list any more: the
	// selected-item strip that used to be charged here is gone, and what it
	// carried is in the inspector beside the list instead — which costs columns
	// rather than rows and so appears in no height budget. The legend and the
	// deprecation banner that used to be charged above the list are gone too.
	got := listChromeLines(m)
	want := 1 + 1 + 1 + footerLines + statusBarLines
	if got != want {
		t.Errorf("baseline chrome: got %d want %d", got, want)
	}
}

// TestListFooterBudgetMatchesRenderedRowsAcrossStates pins the fixed point the
// layout math depends on: the position fragment depends on the list height,
// which depends on the footer height, which depends on the position fragment.
// The budget must therefore measure the same capped output the renderer emits —
// at every width, because the cap only binds once the palette overflows.
func TestListFooterBudgetMatchesRenderedRowsAcrossStates(t *testing.T) {
	for _, width := range []int{0, 79, 80, 119, 120, 160} {
		base := scrollModel()
		base.width = width
		base.height = 18
		base.filtered = makeRowsForScroll(20)
		base.rebuildItems()

		empty := base
		empty.filtered = nil
		empty.rebuildItems()
		filtered := base
		filtered.activeFilter = dashboard.Filter{Substrings: []string{"api"}}
		// The inspector's three display states are three different footers: the
		// split and the hidden queue draw the same one with a different `i` label
		// and a different tab gloss, and full screen draws none at all. Each has
		// to measure what it renders.
		hidden := base
		hidden.inspectorDisplay = inspectorHidden
		fullScreen := base
		fullScreen.inspectorDisplay = inspectorFullScreen

		for _, tc := range []struct {
			name string
			m    Model
		}{
			{name: "overflowing list", m: base},
			{name: "empty list", m: empty},
			{name: "active filter", m: filtered},
			{name: "inspector hidden", m: hidden},
			{name: "inspector full screen", m: fullScreen},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				got := footerBudgetLines(tc.m)
				want := len(rootFooterLines(tc.m))
				if got != want {
					t.Fatalf("footer budget = %d, rendered footer rows = %d", got, want)
				}
				if width > 0 && dashboard.BandFor(width) != dashboard.BandCompact && got > footerHintMaxLines {
					t.Fatalf("budget measured %d rows, above the %d-row cap", got, footerHintMaxLines)
				}
			})
		}
	}
}

// The quiet header is one line whatever the health reading says. That is the
// point of consolidating the pips, their hint strip, and the deprecation banner
// into a single indicator: a condition arriving or clearing used to move every
// row of the list, so a degrading daemon reflowed the thing the operator was
// reading.
func TestListChromeLines_HeaderIsOneLineInEveryHealthState(t *testing.T) {
	base := scrollModel()
	base.width = 120
	baseline := listChromeLines(base)
	now := time.Now()

	for _, tc := range []struct {
		name  string
		apply func(Model) Model
	}{
		{"deprecated config keys", func(m Model) Model {
			m.deprecations = []config.Deprecation{{Key: "foo", Replacement: "bar"}}
			return m
		}},
		{"daemon down and hooks drifted", func(m Model) Model {
			m.daemonAlive = false
			m.daemonHealthCheckedAt = now
			m.hookHealth = commands.HookHealth{Drifted: []string{"work"}}
			m.hookHealthCheckedAt = now
			return m
		}},
		{"everything clean and observed", func(m Model) Model {
			m.daemonAlive = true
			m.daemonHealthCheckedAt = now
			m.hookHealth = commands.HookHealth{InSync: []string{"work"}}
			m.hookHealthCheckedAt = now
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.apply(base)
			if rows := len(layout.SectionLines(renderHeader(m))); rows != 1 {
				t.Errorf("header rows = %d, want 1", rows)
			}
			if got := listChromeLines(m); got != baseline {
				t.Errorf("chrome lines = %d, want the baseline %d", got, baseline)
			}
		})
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

// TestListVisibleHeight_SplitInspectorKeepsTheWholePaneHeight is the height half
// of the side-by-side split. The inspector takes columns from the list, never
// rows: both panes are drawn to the same height, so the queue's visible budget
// beside an inspector is exactly the budget it has without one. A split that
// shortened the list would be the vertical preview this replaced.
func TestListVisibleHeight_SplitInspectorKeepsTheWholePaneHeight(t *testing.T) {
	m := scrollModel()
	m.width = 160
	m.cfg.Defaults.Preview = config.PreviewConfig{Direction: "horizontal", Ratio: 0.5}
	requireInspector(t, m)

	footerLines := footerBudgetLines(m)
	want := m.height - chromeAboveListLines(m) - 1 - footerLines - statusBarLines
	if got := listVisibleHeight(m); got != want {
		t.Errorf("split inspector: got %d want %d", got, want)
	}

	// And the same model with the inspector hidden answers the same number, which
	// is the claim above said from the other side.
	hidden := m
	hidden.inspectorDisplay = inspectorHidden
	if got := listVisibleHeight(hidden); got != want {
		t.Errorf("hiding the inspector changed the list height: got %d want %d", got, want)
	}
}

// TestListVisibleHeight_FullScreenInspectorLeavesTheListNothing is the other
// end. Full screen is not a wider split; the queue is not drawn at all, so its
// height question has no pane behind it and the geometry keeps the list's own
// dimensions untouched rather than clamping them to zero — which is what lets
// the cursor and the scroll offset survive the round trip.
func TestListVisibleHeight_FullScreenInspectorLeavesTheListNothing(t *testing.T) {
	m := scrollModel()
	m.width = 160
	m.filtered = makeRowsForScroll(20)
	m.rebuildItems()
	m.cursor = 12
	scrollListToCursor(&m)
	before := m.listOffset

	split := dashboardGeometry(m)
	m.inspectorDisplay = inspectorFullScreen
	full := dashboardGeometry(m)

	// Rows, and only rows. The scroll offset is an index into a row budget, so
	// the height is what has to survive; the width the undrawn queue is handed
	// is arbitrary and the assertion deliberately says nothing about it.
	if full.listHeight != split.listHeight {
		t.Errorf("full screen resized the row budget of the queue it stopped drawing: %d, want %d",
			full.listHeight, split.listHeight)
	}
	if !full.fullScreen {
		t.Error("geometry did not report a full-screen inspector")
	}
	if m.cursor != 12 || m.listOffset != before {
		t.Errorf("entering full screen moved the selection: cursor=%d offset=%d, want 12/%d",
			m.cursor, m.listOffset, before)
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
	m.rebuildItems()

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
	m.rebuildItems()
	m.listOffset = 4
	clampListOffset(&m)
	if m.listOffset != 0 {
		t.Errorf("total <= visible: got offset %d want 0", m.listOffset)
	}
}

func TestClampListOffset_ShrinkPullsOffsetBack(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(40)
	m.rebuildItems()
	visible := listVisibleHeight(m)
	m.listOffset = len(m.filtered) - visible // valid offset at full size

	// Simulate a refresh that shrinks the list (e.g. a session was closed).
	m.filtered = m.filtered[:6]
	m.rebuildItems()
	clampListOffset(&m)
	if m.listOffset != 0 {
		t.Errorf("after shrink to %d rows: got offset %d want 0", len(m.filtered), m.listOffset)
	}
}

func TestScrollListToCursor_CursorBeforeWindowMovesOffsetDown(t *testing.T) {
	m := scrollModel()
	m.height = 15 // small enough that the visible budget is bounded
	m.filtered = makeRowsForScroll(30)
	m.rebuildItems()
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
	m.rebuildItems()
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
	m.rebuildItems()
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

// TestScrollListToCursor_InspectorShownKeepsCursorVisible reconstructs the
// screenshot scenario reported against commit 1802fa2: side pane on,
// multi-group filtered list, cursor on a row past the visible window. Before
// the fix, listVisibleHeight (and renderSplit) over-counted the available
// height by the size of the chrome above the list (legend + spacer +
// banner + chips), so scrollListToCursor thought the cursor fit when in
// practice the terminal clipped it. This test fails on the buggy
// `m.height - 1 - 1 - footerLines` formula and passes after the chrome is
// subtracted properly.
func TestScrollListToCursor_InspectorShownKeepsCursorVisible(t *testing.T) {
	m := scrollModel()
	m.width = 160
	m.cfg.Defaults.Preview = config.PreviewConfig{Direction: "horizontal", Ratio: 0.5}
	// Mirror the screenshot: a tight terminal where chrome consumes enough
	// lines that ignoring it inflates the budget by several rows. It is shorter
	// than the reported one because the retired detail strip gave the list two
	// rows back, and the fixture only tests anything while the list overflows.
	m.height = 16
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
	m.rebuildItems()
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
		t.Errorf("cursor not in window after scroll with the inspector shown: "+
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
	m.height = 15 // short-terminal chrome leaves a 7-line list pane
	m.filtered = makeRowsForScroll(20)
	m.rebuildItems()
	m.cursor = 10
	m.listOffset = 0

	// Ten: capping the footer at ONE row handed the list another row back on
	// top of the three the two-row cap had already recovered from the old
	// exhaustive footer and the two the retired selected-item strip gave up.
	visible := listVisibleHeight(m)
	if visible != 10 {
		t.Fatalf("test fixture expected visible=10, got %d", visible)
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
