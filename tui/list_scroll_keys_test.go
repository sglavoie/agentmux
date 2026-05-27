package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

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
