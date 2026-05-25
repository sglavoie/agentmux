package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// pickerScrollFixtureModel builds a picker-mode model with rows config
// sessions and a tight viewport so j/k/page-key advances are guaranteed to
// push the cursor past the visible window. Fatals when the visible budget
// is not strictly smaller than the row count, so a future chrome-math drift
// surfaces immediately instead of silently no-opping the assertion.
func pickerScrollFixtureModel(t *testing.T, rows, height int) Model {
	t.Helper()
	sessions := make([]config.Session, rows)
	for i := 0; i < rows; i++ {
		sessions[i] = config.Session{
			Title:     indexedRowName(i),
			Directory: "/tmp",
			Account:   "work",
		}
	}
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions:      sessions,
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	m.width = 100
	m.height = height
	if visible := pickerVisibleHeight(m); visible >= rows {
		t.Fatalf("pickerScrollFixtureModel: visible budget %d not smaller than rows %d; tighten the height", visible, rows)
	}
	return m
}

func TestPickerVisibleHeight_FloorAtListMinVisible(t *testing.T) {
	m := pickerModel(t)
	m.height = 2
	if got := pickerVisibleHeight(m); got != listMinVisible {
		t.Errorf("floor: got %d want %d", got, listMinVisible)
	}
}

func TestClampPickerOffset_PinsToValidRange(t *testing.T) {
	m := pickerScrollFixtureModel(t, 30, 15)
	visible := pickerVisibleHeight(m)
	maxOffset := len(m.pickerFilteredIdx) - visible
	if maxOffset <= 0 {
		t.Fatalf("test fixture must keep total > visible: total=%d visible=%d",
			len(m.pickerFilteredIdx), visible)
	}

	m.pickerOffset = maxOffset + 7
	clampPickerOffset(&m)
	if m.pickerOffset != maxOffset {
		t.Errorf("over-max: got %d want %d", m.pickerOffset, maxOffset)
	}

	m.pickerOffset = -5
	clampPickerOffset(&m)
	if m.pickerOffset != 0 {
		t.Errorf("negative offset: got %d want 0", m.pickerOffset)
	}
}

func TestScrollPickerToCursor_CursorAfterWindowAdvancesOffset(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	visible := pickerVisibleHeight(m)
	m.pickerOffset = 0
	m.pickerCursor = visible + 5

	scrollPickerToCursor(&m)

	if m.pickerCursor < m.pickerOffset || m.pickerCursor >= m.pickerOffset+visible {
		t.Errorf("cursor not in window after scroll: cursor=%d offset=%d visible=%d",
			m.pickerCursor, m.pickerOffset, visible)
	}
}

func TestHandlePickerKey_JAdvancesOffsetPastVisibleWindow(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	visible := pickerVisibleHeight(m)

	steps := visible + 5
	for i := 0; i < steps; i++ {
		m = pressKey(m, "j")
	}

	if m.pickerCursor != steps {
		t.Fatalf("cursor after %d j presses: got %d want %d", steps, m.pickerCursor, steps)
	}
	if m.pickerOffset == 0 {
		t.Errorf("pickerOffset did not advance after pressing j past visible window")
	}
	if m.pickerCursor < m.pickerOffset || m.pickerCursor >= m.pickerOffset+visible {
		t.Errorf("cursor outside window: cursor=%d offset=%d visible=%d",
			m.pickerCursor, m.pickerOffset, visible)
	}
}

func TestHandlePickerKey_KFollowsCursorBackUp(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	visible := pickerVisibleHeight(m)

	for i := 0; i < visible+10; i++ {
		m = pressKey(m, "j")
	}
	advancedOffset := m.pickerOffset
	if advancedOffset == 0 {
		t.Fatalf("setup: pickerOffset did not advance during initial walk-down")
	}

	for i := 0; i < advancedOffset+2; i++ {
		m = pressKey(m, "k")
	}

	if m.pickerOffset > m.pickerCursor {
		t.Errorf("pickerOffset above cursor after walk-up: offset=%d cursor=%d", m.pickerOffset, m.pickerCursor)
	}
}

func TestHandlePickerKey_CtrlDAdvancesByHalfPage(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	visible := pickerVisibleHeight(m)
	step := visible / 2
	if step < 1 {
		step = 1
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = updated.(Model)

	if m.pickerCursor != step {
		t.Errorf("ctrl+d picker cursor: got %d want %d", m.pickerCursor, step)
	}
}

func TestHandlePickerKey_PgDownAdvancesByPage(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	visible := pickerVisibleHeight(m)
	step := visible - 1
	if step < 1 {
		step = 1
	}

	m = pressKey(m, "pgdown")

	if m.pickerCursor != step {
		t.Errorf("pgdown picker cursor: got %d want %d", m.pickerCursor, step)
	}
	if m.pickerCursor >= m.pickerOffset+visible {
		t.Errorf("pgdown left picker cursor outside window: cursor=%d offset=%d visible=%d",
			m.pickerCursor, m.pickerOffset, visible)
	}
}

func TestRenderPickerList_OffsetEmitsTopIndicator(t *testing.T) {
	m := pickerScrollFixtureModel(t, 30, 15)
	m.pickerOffset = 5

	out := stripAnsi(renderPickerList(m, pickerVisibleHeight(m)))

	firstContentLine := ""
	for _, line := range strings.Split(out, "\n") {
		// Skip header + filter-prompt lines.
		if strings.Contains(line, "▲") || strings.Contains(line, "▼") {
			firstContentLine = line
			break
		}
	}
	if !strings.Contains(firstContentLine, "▲") {
		t.Errorf("expected ▲ indicator with non-zero offset; got %q", out)
	}
	// Rows before the offset must not appear.
	for i := 0; i < m.pickerOffset; i++ {
		if strings.Contains(out, indexedRowName(i)) {
			t.Errorf("skipped row %s should not appear in windowed picker output", indexedRowName(i))
		}
	}
}

func TestRenderPickerList_OverflowEmitsBottomIndicator(t *testing.T) {
	m := pickerScrollFixtureModel(t, 40, 15)
	m.pickerOffset = 0

	out := stripAnsi(renderPickerList(m, pickerVisibleHeight(m)))
	if !strings.Contains(out, "▼") {
		t.Errorf("expected ▼ indicator when overflowing; got %q", out)
	}
}

func TestRenderPickerList_UnboundedRendersAllRows(t *testing.T) {
	// Build a small picker directly (the scroll fixture fatals when rows fit
	// in the budget; we want the opposite here).
	sessions := make([]config.Session, 12)
	for i := 0; i < 12; i++ {
		sessions[i] = config.Session{Title: indexedRowName(i), Directory: "/tmp", Account: "work"}
	}
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions:      sessions,
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")

	out := stripAnsi(renderPickerList(m, 0))
	for i := 0; i < 12; i++ {
		if !strings.Contains(out, indexedRowName(i)) {
			t.Errorf("unbounded render missing %s; got %q", indexedRowName(i), out)
		}
	}
	if strings.Contains(out, "▲") || strings.Contains(out, "▼") {
		t.Errorf("unbounded render should not emit indicators; got %q", out)
	}
}

func TestPickerWindowSizeMsg_ResizeKeepsCursorVisible(t *testing.T) {
	m := pickerScrollFixtureModel(t, 60, 40)
	for i := 0; i < 50; i++ {
		m = pressKey(m, "j")
	}
	if m.pickerCursor < 40 {
		t.Fatalf("setup: expected picker cursor past 40 after 50 j presses; got %d", m.pickerCursor)
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updated.(Model)

	visible := pickerVisibleHeight(m)
	if m.pickerCursor < m.pickerOffset || m.pickerCursor >= m.pickerOffset+visible {
		t.Errorf("picker cursor outside window after resize: cursor=%d offset=%d visible=%d",
			m.pickerCursor, m.pickerOffset, visible)
	}
	maxOffset := len(m.pickerFilteredIdx) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.pickerOffset > maxOffset {
		t.Errorf("pickerOffset exceeds new maxOffset: got %d max %d", m.pickerOffset, maxOffset)
	}
}

func TestListFooterPositionFragment_PresentWhenOverflow(t *testing.T) {
	m := scrollFixtureModel(t, 40, 15)
	out := stripAnsi(renderFooter(m))
	// Format: `start–end of total`. With cursor=0 and visible<total, start=1.
	if !strings.Contains(out, "of 40") {
		t.Errorf("expected position fragment 'of 40' in footer when list overflows; got %q", out)
	}
}

func TestListFooterPositionFragment_AbsentWhenAllFits(t *testing.T) {
	m := testModel()
	m.width = 100
	m.height = 40
	m = loadSessions(m, makeSessions())
	out := stripAnsi(renderFooter(m))
	if strings.Contains(out, " of ") {
		t.Errorf("expected no position fragment when everything fits; got %q", out)
	}
}
