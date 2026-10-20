package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
)

// This file is the inspector's geometry: the exact widths and heights a split is
// granted or refused at, and what a refusal turns into.
//
// It replaces the vertical preview's toggle tests wholesale, because the thing
// they asserted no longer exists. That pane was opt-in and REFUSED a terminal
// below its floors, with a toast saying so; the inspector is on by default and
// refuses nothing — a terminal too narrow for two columns gets the queue alone,
// and an operator who asks for the pane there gets it over the whole body. There
// is no size at which `i` produces a notice instead of a pane.

func inspectorGeometryCfg() config.PreviewConfig {
	return config.PreviewConfig{
		Direction: "horizontal",
		Ratio:     0.4,
		RefreshMs: 500,
	}
}

// splitFloor is the narrowest terminal a side-by-side split is drawn at with no
// configured minimum raising it: both panes at their floors with the divider
// between them.
const splitFloor = inspectorMinQueueCols + inspectorDividerCols + inspectorMinCols

// TestInspectorSplitWidthBoundaryIsExact pins the width half of the fit one cell
// on either side. A floor that is off by one is invisible in a screenshot and is
// exactly what an operator who lives at a fixed terminal width would hit every
// time.
func TestInspectorSplitWidthBoundaryIsExact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		width int
		want  bool
	}{
		{splitFloor - 1, false},
		{splitFloor, true},
		{splitFloor + 1, true},
		{0, false},
	} {
		m := testModel()
		m.width, m.height = tc.width, 50
		m.cfg.Defaults.Preview = inspectorGeometryCfg()
		if got := m.inspectorActive(); got != tc.want {
			t.Errorf("width %d: inspector active = %v, want %v (floor %d)", tc.width, got, tc.want, splitFloor)
		}
	}
}

// TestConfiguredMinColsRaisesTheSplitFloorAndNeverLowersIt is the operator's
// half of the width rule. A configured minimum wider than the built-in floor is
// honoured; one narrower than it is ignored, because the built-in floor is what
// keeps the metadata block readable rather than a preference.
func TestConfiguredMinColsRaisesTheSplitFloorAndNeverLowersIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		minCols   int
		width     int
		wantSplit bool
	}{
		{"configured floor binds above the built-in one", 140, 139, false},
		{"configured floor binds above the built-in one, met", 140, 140, true},
		{"configured floor below the built-in one is ignored", 20, splitFloor - 1, false},
		{"configured floor below the built-in one, built-in met", 20, splitFloor, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel()
			m.width, m.height = tc.width, 50
			m.cfg.Defaults.Preview = inspectorGeometryCfg()
			m.cfg.Defaults.Preview.MinCols = tc.minCols
			if got := m.inspectorActive(); got != tc.wantSplit {
				t.Errorf("min_cols=%d width=%d: split = %v, want %v", tc.minCols, tc.width, got, tc.wantSplit)
			}
		})
	}
}

// TestSplitWidthsNeverStarveEitherPane checks the allocation rather than the
// gate: whatever the ratio asks for, neither column may end up below its floor,
// because a ratio is a preference and a floor is a legibility requirement.
func TestSplitWidthsNeverStarveEitherPane(t *testing.T) {
	t.Parallel()

	for _, ratio := range []float64{0, 0.05, 0.4, 0.5, 0.95, 1} {
		for _, width := range []int{splitFloor, 100, 160, 240} {
			m := testModel()
			m.width, m.height = width, 50
			m.cfg.Defaults.Preview = inspectorGeometryCfg()
			m.cfg.Defaults.Preview.Ratio = ratio
			listWidth, inspectorWidth, ok := dashboardSplitWidths(m)
			if !ok {
				t.Fatalf("ratio %v width %d: no split at or above the floor", ratio, width)
			}
			if listWidth < inspectorMinQueueCols {
				t.Errorf("ratio %v width %d: queue column %d is below its floor %d",
					ratio, width, listWidth, inspectorMinQueueCols)
			}
			if inspectorWidth < inspectorMinCols {
				t.Errorf("ratio %v width %d: inspector column %d is below its floor %d",
					ratio, width, inspectorWidth, inspectorMinCols)
			}
			if total := listWidth + inspectorDividerCols + inspectorWidth; total != width {
				t.Errorf("ratio %v width %d: columns sum to %d", ratio, width, total)
			}
		}
	}
}

// TestInspectorSplitHeightBoundaryIsExact pins the row half, which the width
// test cannot reach: a terminal can be wide enough for two columns and still too
// short to put anything in them. The floor is on the BODY rows the panes share,
// so the assertion walks the terminal height down until the body crosses it and
// checks the split turns off on exactly that step.
func TestInspectorSplitHeightBoundaryIsExact(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width = 160
	m.cfg.Defaults.Preview = inspectorGeometryCfg()

	var lastFit, firstMiss int
	for height := 60; height >= 1; height-- {
		m.height = height
		rows := dashboardInspectorBodyRows(m)
		active := m.inspectorActive()
		if active != (rows >= inspectorMinBodyRows) {
			t.Fatalf("height %d: active=%v but body rows=%d against floor %d",
				height, active, rows, inspectorMinBodyRows)
		}
		if active {
			lastFit = height
		} else if lastFit != 0 && firstMiss == 0 {
			firstMiss = height
		}
	}
	if lastFit == 0 || firstMiss != lastFit-1 {
		t.Fatalf("the split did not turn off on one step: last fit %d, first miss %d", lastFit, firstMiss)
	}
	if dashboardInspectorBodyRows(withHeight(m, lastFit)) != inspectorMinBodyRows {
		t.Errorf("the tallest refused terminal should sit exactly one row under the floor, not further")
	}
}

func withHeight(m Model, height int) Model {
	m.height = height
	return m
}

// TestAShortTerminalDropsToTheQueueRatherThanPromotingTheInspector is the
// direction rule. Full screen is a deliberate answer to `i` on a terminal that
// cannot split; it is never what a shrinking terminal turns into, because losing
// sight of the session list is not a reasonable response to a resize nobody
// asked the dashboard about.
func TestAShortTerminalDropsToTheQueueRatherThanPromotingTheInspector(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height = 160, 50
	m.cfg.Defaults.Preview = inspectorGeometryCfg()
	requireInspector(t, m)

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 60, Height: 50})
	m = updated.(Model)
	if m.inspectorFullScreenActive() {
		t.Fatal("a narrowing promoted the inspector over the queue")
	}
	if g := dashboardGeometry(m); g.split || g.fullScreen {
		t.Fatalf("a narrowed dashboard is not queue-only: %+v", g)
	}
	if m.inspectorDisplay != inspectorAutomatic {
		t.Fatalf("a narrowing rewrote intent to %v", m.inspectorDisplay)
	}
}

// TestPressingTheKeyOnATerminalTooNarrowToSplitGoesFullScreen is the refusal
// that is not one. The old pane answered this terminal with a toast; the
// inspector answers it with the output the operator asked to read.
func TestPressingTheKeyOnATerminalTooNarrowToSplitGoesFullScreen(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height = splitFloor-1, 50
	m.cfg.Defaults.Preview = inspectorGeometryCfg()
	m = loadSessions(m, makeSessions())
	if m.inspectorActive() {
		t.Fatal("setup: a terminal below the floor already holds a split")
	}

	updated, cmd := m.Update(keyPress(keyInspector))
	got := updated.(Model)
	if !got.inspectorFullScreenActive() {
		t.Fatalf("a too-narrow terminal did not go full screen; display=%v toasts=%+v",
			got.inspectorDisplay, got.toasts)
	}
	if len(got.toasts) != 0 {
		t.Errorf("the inspector refused out loud: %+v", got.toasts)
	}
	if cmd == nil {
		t.Error("going full screen dispatched neither a read nor a tick")
	}
	if !got.inspectorFocused() {
		t.Error("the full-screen pane did not take the keyboard")
	}
}

// TestFullScreenReturnsToTheSplitItCameFrom is the round trip the review item
// names. Escape leaves full screen for the AUTOMATIC intent rather than for
// hidden: the operator asked to read output and then asked to stop, which says
// nothing about whether a split should be drawn on a terminal that affords one.
func TestFullScreenReturnsToTheSplitItCameFrom(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height = 160, 50
	m.cfg.Defaults.Preview = inspectorGeometryCfg()
	m = loadSessions(m, makeSessions())
	m.cursor = 1
	scrollListToCursor(&m)
	cursor, offset := m.cursor, m.listOffset

	m.inspectorDisplay = inspectorFullScreen
	m.paneFocus = focusInspector
	if !dashboardGeometry(m).fullScreen {
		t.Fatal("setup: the pane is not full screen")
	}

	updated, _ := m.Update(keyPress(keyEsc))
	m = updated.(Model)
	if m.inspectorDisplay != inspectorAutomatic {
		t.Fatalf("escape left full screen for %v, want automatic", m.inspectorDisplay)
	}
	if !m.inspectorActive() || m.inspectorFullScreenActive() {
		t.Fatalf("escape did not land on the split: active=%v full=%v",
			m.inspectorActive(), m.inspectorFullScreenActive())
	}
	if m.inspectorFocused() {
		t.Error("escape left the keyboard in the pane it just shrank")
	}
	if m.cursor != cursor || m.listOffset != offset {
		t.Errorf("the round trip moved the selection: cursor %d→%d offset %d→%d",
			cursor, m.cursor, offset, m.listOffset)
	}
}
