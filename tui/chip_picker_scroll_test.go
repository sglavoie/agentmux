package tui

import (
	"strings"
	"testing"
)

// chipPickerScrollFixture builds a Model with `rows` synthetic options
// (cursor at the top, [""] leading so the (any) row is always present) and
// a viewport sized so chipPickerVisibleHeight is strictly smaller than the
// row count. It fatals if that invariant breaks so future chrome-math drift
// surfaces immediately — same contract as sessionSearchScrollFixture.
func chipPickerScrollFixture(t *testing.T, rows, height int) Model {
	t.Helper()
	if rows < 2 {
		t.Fatalf("chipPickerScrollFixture: rows must be >= 2; got %d", rows)
	}
	opts := make([]string, rows)
	opts[0] = ""
	for i := 1; i < rows; i++ {
		opts[i] = indexedRowName(i - 1)
	}
	m := Model{
		mode:              ModeChipPicker,
		width:             100,
		height:            height,
		chipPickerKind:    chipPickerProject,
		chipPickerOptions: opts,
	}
	if visible := chipPickerVisibleHeight(m); visible >= rows {
		t.Fatalf("chipPickerScrollFixture: visible budget %d not smaller than rows %d; tighten the height", visible, rows)
	}
	return m
}

func TestChipPickerVisibleHeightBudget(t *testing.T) {
	cases := []struct {
		height int
		want   int
	}{
		{0, 0},
		{1, 1},
		{5, 1},
		{6, 1},
		{10, 5},
		{16, 11},
		{17, 12},
		{100, 12},
	}
	for _, tc := range cases {
		m := Model{height: tc.height}
		if got := chipPickerVisibleHeight(m); got != tc.want {
			t.Errorf("height=%d: chipPickerVisibleHeight=%d, want %d", tc.height, got, tc.want)
		}
	}
}

func TestChipPickerScroll_CursorAtBottomShowsTopArrowOnly(t *testing.T) {
	m := chipPickerScrollFixture(t, 40, 15)
	m.chipPickerCursor = len(m.chipPickerOptions) - 1
	scrollChipPickerToCursor(&m)

	out := stripAnsi(renderChipPicker(m))
	if !strings.Contains(out, "▲") {
		t.Errorf("expected ▲ when cursor is at bottom with overflow; got %q", out)
	}
	if strings.Contains(out, "▼") {
		t.Errorf("did not expect ▼ when cursor is on the final row; got %q", out)
	}
}

func TestChipPickerScroll_CursorAtTopShowsBottomArrowOnly(t *testing.T) {
	m := chipPickerScrollFixture(t, 40, 15)
	m.chipPickerCursor = 0
	scrollChipPickerToCursor(&m)

	out := stripAnsi(renderChipPicker(m))
	if !strings.Contains(out, "▼") {
		t.Errorf("expected ▼ when cursor is at top with overflow; got %q", out)
	}
	if strings.Contains(out, "▲") {
		t.Errorf("did not expect ▲ when cursor is on the first row; got %q", out)
	}
}

func TestChipPickerScroll_CursorMidListShowsBothArrows(t *testing.T) {
	m := chipPickerScrollFixture(t, 40, 15)
	m.chipPickerCursor = 20
	scrollChipPickerToCursor(&m)

	out := stripAnsi(renderChipPicker(m))
	if !strings.Contains(out, "▲") {
		t.Errorf("expected ▲ mid-list; got %q", out)
	}
	if !strings.Contains(out, "▼") {
		t.Errorf("expected ▼ mid-list; got %q", out)
	}
}

// TestChipPickerScroll_CursorRemainsRenderedAcrossWindow walks the cursor
// across the full list, calling scrollChipPickerToCursor at each step, and
// confirms the cursor row is still inside the rendered window. Mirrors the
// session-search scroll regression test.
func TestChipPickerScroll_CursorRemainsRenderedAcrossWindow(t *testing.T) {
	m := chipPickerScrollFixture(t, 40, 15)
	total := len(m.chipPickerOptions)

	for step := 0; step < total; step++ {
		m.chipPickerCursor = step
		scrollChipPickerToCursor(&m)

		out := stripAnsi(renderChipPicker(m))
		cursorLabel := m.chipPickerOptions[step]
		if cursorLabel == "" {
			cursorLabel = "(any)"
		}
		if !strings.Contains(out, cursorLabel) {
			t.Fatalf("cursor row %q dropped from render at step %d (cursor=%d offset=%d)\nrender:\n%s",
				cursorLabel, step, m.chipPickerCursor, m.chipPickerOffset, out)
		}
	}
}

// TestChipPickerScroll_ResizeKeepsCursorVisible shrinks the terminal height
// after parking the cursor deep in the list. scrollChipPickerToCursor must
// re-clamp the offset so the cursor row still renders.
func TestChipPickerScroll_ResizeKeepsCursorVisible(t *testing.T) {
	m := chipPickerScrollFixture(t, 40, 30)
	m.chipPickerCursor = 35
	scrollChipPickerToCursor(&m)

	// Shrink the viewport — the equivalent of a WindowSizeMsg arriving with
	// a smaller height. The clamp must keep the cursor inside the window.
	m.height = 10
	scrollChipPickerToCursor(&m)

	out := stripAnsi(renderChipPicker(m))
	cursorLabel := m.chipPickerOptions[m.chipPickerCursor]
	if !strings.Contains(out, cursorLabel) {
		t.Fatalf("cursor row %q dropped after resize (cursor=%d offset=%d)\n%s",
			cursorLabel, m.chipPickerCursor, m.chipPickerOffset, out)
	}
}
