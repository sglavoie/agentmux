package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// scrollModel returns a baseline Model parked in ModeList with a generous
// height so the legend renders and previewFooterLines reports the non-preview
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
	// Baseline: header(1) + legend(1) + inline filter(1) + trailing nl(1)
	// + previewFooterLines (preview off, no notice → 2) = 6.
	got := listChromeLines(m)
	want := 1 + 1 + 1 + 1 + 2
	if got != want {
		t.Errorf("baseline chrome: got %d want %d", got, want)
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

func TestListChromeLines_LegendDroppedBelowLegendMinHeight(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	m.height = legendMinHeight - 1 // legend omitted
	got := listChromeLines(m)
	if got != base-1 {
		t.Errorf("legend hidden on short term: got %d want %d", got, base-1)
	}
}

func TestListChromeLines_ChipsAddOneLine(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	m.activeFilter = Filter{Substrings: []string{"api"}}
	got := listChromeLines(m)
	if got != base+1 {
		t.Errorf("with chips: got %d want %d", got, base+1)
	}
}

func TestListChromeLines_InlineFilterDropsOutsideModeList(t *testing.T) {
	m := scrollModel()
	base := listChromeLines(m)
	// Outside ModeList the inline filter line is not emitted; the footer
	// also flips back to its default but for this test we only assert the
	// inline-filter delta by holding everything else constant. Switching to
	// a mode whose footer is still the default-2 lines preserves the
	// invariant.
	m.mode = ModeHelp
	got := listChromeLines(m)
	if got != base-1 {
		t.Errorf("inline filter outside ModeList: got %d want %d", got, base-1)
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

	footerLines := previewFooterLines(m)
	paneHeight := m.height - chromeAboveListLines(m) - 1 - footerLines
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

	footerLines := previewFooterLines(m)
	want := m.height - chromeAboveListLines(m) - 1 - footerLines

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
	// label before the first row of each group, so a slice that contains
	// both groups costs (rows + 2) lines while a slice from a single group
	// costs (rows + 1) lines.
	m.filtered = []session.RunningSession{
		{WindowID: 1, Base: "a1", Account: "work", State: "stop"},
		{WindowID: 2, Base: "a2", Account: "work", State: "stop"},
		{WindowID: 3, Base: "b1", Account: "work", State: "bolt"},
		{WindowID: 4, Base: "b2", Account: "work", State: "bolt"},
		{WindowID: 5, Base: "b3", Account: "work", State: "bolt"},
	}
	m.lastActivity = map[int]time.Time{}

	if g := countGroups(m.filtered, m.lastActivity, m.idleThreshold); g <= 1 {
		t.Fatalf("test fixture must span multiple groups: countGroups=%d", g)
	}

	// listLineCost mirrors the renderer: prevGroup=-1 at offset so the first
	// row always picks up a label. Slice [0..0] crosses group boundary 0→0
	// (from -1) once → cost = 1 (label) + 1 (row) = 2.
	if got := listLineCost(&m, 0, 0, true); got != 2 {
		t.Errorf("single-row, single-group cost: got %d want 2", got)
	}

	// Slice [0..2] spans groups 0 and 1: rows=3, labels=2 → cost=5.
	if got := listLineCost(&m, 0, 2, true); got != 5 {
		t.Errorf("two-group slice cost: got %d want 5", got)
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
// height by the size of the chrome above the list (legend + inline filter +
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

	if g := countGroups(m.filtered, m.lastActivity, m.idleThreshold); g <= 1 {
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
	m.height = 13 // short-terminal chrome leaves an 8-line list pane
	m.filtered = makeRowsForScroll(20)
	m.cursor = 10
	m.listOffset = 0

	visible := listVisibleHeight(m)
	if visible != 8 {
		t.Fatalf("test fixture expected visible=8, got %d", visible)
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

// TestRenderListAt_UnboundedHeightRendersAllRowsNoIndicators verifies the
// height<=0 path: every row is rendered (matching the pre-windowing behavior)
// and neither ▲ nor ▼ indicators appear.
func TestRenderListAt_UnboundedHeightRendersAllRowsNoIndicators(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(30)
	m.listOffset = 5 // offset is ignored when height <= 0

	out := stripAnsi(renderListAt(m, m.width, 0))

	for i := 0; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("unbounded render missing %s; got %q", name, out)
		}
	}
	if strings.Contains(out, "▲") {
		t.Errorf("unbounded render should not emit ▲ indicator")
	}
	if strings.Contains(out, "▼") {
		t.Errorf("unbounded render should not emit ▼ indicator")
	}
}

// TestRenderListAt_OffsetZeroFullBudgetRendersAllNoIndicators covers the
// "everything fits" case: when offset=0 and the row count is well within the
// height budget, no indicators are emitted.
func TestRenderListAt_OffsetZeroFullBudgetRendersAllNoIndicators(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(5)
	m.listOffset = 0

	out := stripAnsi(renderListAt(m, m.width, 20))

	for i := 0; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("expected %s in full-budget render; got %q", name, out)
		}
	}
	if strings.Contains(out, "▲") || strings.Contains(out, "▼") {
		t.Errorf("no indicators expected when everything fits; got %q", out)
	}
}

// TestRenderListAt_OffsetGreaterThanZeroEmitsTopIndicator verifies that a
// non-zero listOffset skips the leading rows and prepends the ▲ indicator,
// and that the skipped rows are absent from the output.
func TestRenderListAt_OffsetGreaterThanZeroEmitsTopIndicator(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(20)
	m.listOffset = 5

	// Budget big enough that the remaining rows (15) all fit -- only the
	// top indicator should appear, not the bottom one.
	out := stripAnsi(renderListAt(m, m.width, 30))

	// The very first content line must be the ▲ indicator.
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(firstLine, "▲") {
		t.Errorf("expected ▲ as first line; got first line %q (full %q)", firstLine, out)
	}

	// Skipped rows must not appear.
	for i := 0; i < m.listOffset; i++ {
		name := indexedRowName(i)
		if strings.Contains(out, name) {
			t.Errorf("skipped row %s should not appear in windowed output", name)
		}
	}
	// Rows from the window onward should all appear (budget is generous).
	for i := m.listOffset; i < len(m.filtered); i++ {
		name := indexedRowName(i)
		if !strings.Contains(out, name) {
			t.Errorf("expected windowed row %s; got %q", name, out)
		}
	}
}

// TestRenderListAt_OverflowEmitsBottomIndicator verifies that when more rows
// remain past the visible window the renderer reserves a line and emits ▼.
func TestRenderListAt_OverflowEmitsBottomIndicator(t *testing.T) {
	m := scrollModel()
	m.filtered = makeRowsForScroll(40)
	m.listOffset = 0

	out := stripAnsi(renderListAt(m, m.width, 8))
	trimmed := strings.TrimRight(out, "\n")
	lines := strings.Split(trimmed, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "▼") {
		t.Errorf("expected ▼ as last line; got last line %q (full %q)", last, out)
	}
	if strings.Contains(out, "▲") {
		t.Errorf("offset=0 should not emit ▲ indicator; got %q", out)
	}
	// The total emitted lines (including any indicator) must not exceed the
	// passed-in height budget.
	if len(lines) > 8 {
		t.Errorf("renderer exceeded budget: emitted %d lines for budget 8", len(lines))
	}
}

// TestRenderListAt_GroupLabelEmittedAtWindowStart verifies that a window that
// begins inside group 1 still renders the group-1 label as its first row
// (renderListAt's walk initializes prevGroup=-1 at startIdx).
func TestRenderListAt_GroupLabelEmittedAtWindowStart(t *testing.T) {
	m := scrollModel()
	m.filtered = []session.RunningSession{
		{WindowID: 1, Base: "attn1", Account: "w", State: "stop"}, // group 0
		{WindowID: 2, Base: "attn2", Account: "w", State: "stop"}, // group 0
		{WindowID: 3, Base: "act1", Account: "w", State: "bolt"},  // group 1
		{WindowID: 4, Base: "act2", Account: "w", State: "bolt"},  // group 1
		{WindowID: 5, Base: "act3", Account: "w", State: "bolt"},  // group 1
	}
	m.lastActivity = map[int]time.Time{}
	// Offset places the window squarely inside group 1.
	m.listOffset = 3

	out := stripAnsi(renderListAt(m, m.width, 30))

	// Group label should still be present even though the window begins
	// mid-group: this locks in the prevGroup=-1 initializer.
	if !strings.Contains(out, "Active (") {
		t.Errorf("expected Active group label at window start; got %q", out)
	}
	// The "Needs Attention" label belongs to the skipped group 0 rows and
	// should not appear.
	if strings.Contains(out, "Needs Attention") {
		t.Errorf("group-0 label must not appear when window starts inside group 1; got %q", out)
	}
}

// scrollFixtureModel builds a model with a tight viewport and a long
// single-group filtered list so j/k/page-key cursor advances are guaranteed
// to push the cursor past the visible window. Skips the test when the
// fixture's visible budget is not strictly smaller than the row count, so a
// future bump to listMinVisible or chrome math doesn't silently nullify the
// assertion.
func scrollFixtureModel(t *testing.T, rows, height int) Model {
	t.Helper()
	m := testModel()
	m.width = 100
	m.height = height
	m.filtered = makeRowsForScroll(rows)
	if visible := listVisibleHeight(m); visible >= rows {
		t.Fatalf("scrollFixtureModel: visible budget %d not smaller than rows %d; tighten the height", visible, rows)
	}
	return m
}

func TestHandleListKey_JAdvancesListOffsetPastVisibleWindow(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)

	// Press j enough times to push the cursor past the visible budget.
	steps := visible + 5
	for i := 0; i < steps; i++ {
		m = pressKey(m, "j")
	}

	if m.cursor != steps {
		t.Fatalf("cursor after %d j presses: got %d want %d", steps, m.cursor, steps)
	}
	if m.listOffset == 0 {
		t.Errorf("listOffset did not advance after pressing j past visible window")
	}
	// Cursor must remain inside the windowed range [offset, offset+visible).
	if m.cursor < m.listOffset || m.cursor >= m.listOffset+visible {
		t.Errorf("cursor outside window: cursor=%d offset=%d visible=%d",
			m.cursor, m.listOffset, visible)
	}
}

func TestHandleListKey_KFollowsCursorBackUp(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)

	// Walk down past the window, then walk back up to a row that sits above
	// the current offset and assert the offset rewinds to it.
	for i := 0; i < visible+10; i++ {
		m = pressKey(m, "j")
	}
	advancedOffset := m.listOffset
	if advancedOffset == 0 {
		t.Fatalf("setup: listOffset did not advance during initial walk-down")
	}

	for i := 0; i < advancedOffset+2; i++ {
		m = pressKey(m, "k")
	}

	if m.cursor != visible+10-(advancedOffset+2) {
		t.Fatalf("unexpected cursor after walk-up: got %d", m.cursor)
	}
	if m.listOffset > m.cursor {
		t.Errorf("listOffset above cursor after walk-up: offset=%d cursor=%d", m.listOffset, m.cursor)
	}
}

func TestHandleListKey_CtrlDAdvancesByHalfPage(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)
	step := visible / 2
	if step < 1 {
		step = 1
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = updated.(Model)

	if m.cursor != step {
		t.Errorf("ctrl+d cursor: got %d want %d", m.cursor, step)
	}
}

func TestHandleListKey_CtrlUMovesUpByHalfPage(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)
	step := visible / 2
	if step < 1 {
		step = 1
	}

	// Park the cursor below the half-page step so ctrl+u has somewhere to go.
	m.cursor = step + 3
	scrollListToCursor(&m)

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl}))
	m = updated.(Model)

	if m.cursor != 3 {
		t.Errorf("ctrl+u cursor: got %d want 3", m.cursor)
	}
}

func TestHandleListKey_PgDownAdvancesByPage(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)
	step := visible - 1
	if step < 1 {
		step = 1
	}

	m = pressKey(m, "pgdown")

	if m.cursor != step {
		t.Errorf("pgdown cursor: got %d want %d", m.cursor, step)
	}
	if m.cursor >= m.listOffset+visible {
		t.Errorf("pgdown left cursor outside window: cursor=%d offset=%d visible=%d",
			m.cursor, m.listOffset, visible)
	}
}

func TestHandleListKey_PgUpMovesUpByPage(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	visible := listVisibleHeight(m)
	step := visible - 1
	if step < 1 {
		step = 1
	}

	m.cursor = step + 4
	scrollListToCursor(&m)

	m = pressKey(m, "pgup")

	if m.cursor != 4 {
		t.Errorf("pgup cursor: got %d want 4", m.cursor)
	}
}

// TestWindowSizeMsg_ResizeKeepsCursorVisible drives the resize regression
// path: a model whose cursor sits well below the new visible window receives
// a WindowSizeMsg that shrinks the viewport. Both clampListOffset and
// scrollListToCursor must run so the persisted offset fits the new height
// and the cursor stays inside the window.
func TestWindowSizeMsg_ResizeKeepsCursorVisible(t *testing.T) {
	m := scrollFixtureModel(t, 60, 40)
	// Walk past the original window so listOffset is non-zero.
	for i := 0; i < 50; i++ {
		m = pressKey(m, "j")
	}
	if m.cursor < 40 {
		t.Fatalf("setup: expected cursor past 40 after 50 j presses; got %d", m.cursor)
	}

	// Shrink the viewport. The previous offset is almost certainly above the
	// new maxOffset; both clamps must run so the next render is sane.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updated.(Model)

	visible := listVisibleHeight(m)
	if m.cursor < m.listOffset || m.cursor >= m.listOffset+visible {
		t.Errorf("cursor outside window after resize: cursor=%d offset=%d visible=%d",
			m.cursor, m.listOffset, visible)
	}
	maxOffset := len(m.filtered) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.listOffset > maxOffset {
		t.Errorf("listOffset exceeds new maxOffset: got %d max %d", m.listOffset, maxOffset)
	}
}
