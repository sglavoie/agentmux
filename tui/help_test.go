package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRenderHelpNoHeightShowsAll(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	for _, header := range []string{"Navigate", "Act on row", "Launch / open", "Filter", "App"} {
		if !strings.Contains(out, header) {
			t.Errorf("expected section header %q in help output, got:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "footer overflow") {
		t.Errorf("expected footer overflow line in help output, got:\n%s", out)
	}
}

func TestRenderHelpClipsToVisible(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 12
	m.helpOffset = 0

	out := stripAnsi(renderHelp(m))

	if strings.Contains(out, "App") {
		t.Errorf("expected last-section header %q to be clipped at height=12, got:\n%s", "App", out)
	}
	if !strings.Contains(out, "▼") {
		t.Errorf("expected scroll-down indicator at height=12 with offset=0, got:\n%s", out)
	}
}

func TestRenderHelpIndicatorsAtTopAndBottom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 12

	m.helpOffset = 0
	topOut := stripAnsi(renderHelp(m))
	if strings.Contains(topOut, "▲") {
		t.Errorf("expected no scroll-up indicator at offset=0, got:\n%s", topOut)
	}

	total, visible := helpScrollBounds(m)
	m.helpOffset = total - visible
	if m.helpOffset < 0 {
		m.helpOffset = 0
	}
	bottomOut := stripAnsi(renderHelp(m))
	if strings.Contains(bottomOut, "▼") {
		t.Errorf("expected no scroll-down indicator at max offset, got:\n%s", bottomOut)
	}
	if !strings.Contains(bottomOut, "▲") {
		t.Errorf("expected scroll-up indicator at max offset, got:\n%s", bottomOut)
	}
}

func TestHelpScrollKeys(t *testing.T) {
	base := func() Model {
		m := loadSessions(testModel(), makeSessions())
		m.mode = ModeHelp
		m.height = 12
		return m
	}

	total, visible := helpScrollBounds(base())
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}

	t.Run("j advances by 1", func(t *testing.T) {
		m := base()
		m = pressKey(m, "j")
		if m.helpOffset != 1 {
			t.Fatalf("expected helpOffset=1 after j, got %d", m.helpOffset)
		}
	})

	t.Run("k clamps at 0", func(t *testing.T) {
		m := base()
		m = pressKey(m, "k")
		if m.helpOffset != 0 {
			t.Fatalf("expected helpOffset=0 after k from 0, got %d", m.helpOffset)
		}
	})

	t.Run("g jumps to 0", func(t *testing.T) {
		m := base()
		m.helpOffset = maxOffset
		m = pressKey(m, "g")
		if m.helpOffset != 0 {
			t.Fatalf("expected helpOffset=0 after g, got %d", m.helpOffset)
		}
	})

	t.Run("G jumps to max", func(t *testing.T) {
		m := base()
		m = pressKey(m, "G")
		if m.helpOffset != maxOffset {
			t.Fatalf("expected helpOffset=%d after G, got %d", maxOffset, m.helpOffset)
		}
	})

	t.Run("j clamps at max", func(t *testing.T) {
		m := base()
		m.helpOffset = maxOffset
		m = pressKey(m, "j")
		if m.helpOffset != maxOffset {
			t.Fatalf("expected helpOffset to stay at %d, got %d", maxOffset, m.helpOffset)
		}
	})
}

// TestHelpOffsetClampsOnResize verifies a viewport expansion drops
// m.helpOffset into the new [0, maxOffset] range. When the terminal grows
// taller, more content fits on-screen and maxOffset shrinks; without the
// clamp the persisted offset would still point past the bottom of the
// scrollable region and j/k would have to scroll up through dead rows.
func TestHelpOffsetClampsOnResize(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp

	// Small viewport: maxOffset is large; scroll to bottom.
	m.height = 12
	total, visible := helpScrollBounds(m)
	maxOffset := total - visible
	if maxOffset <= 0 {
		t.Skipf("help content too short at height=12 (maxOffset=%d)", maxOffset)
	}
	m.helpOffset = maxOffset

	// Grow the viewport — visible expands, maxOffset shrinks. The stale
	// helpOffset now sits past the bottom of the scroll region.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	m2 := updated.(Model)

	total2, visible2 := helpScrollBounds(m2)
	maxOffset2 := total2 - visible2
	if maxOffset2 < 0 {
		maxOffset2 = 0
	}
	if maxOffset2 >= maxOffset {
		t.Skipf("resize did not shrink maxOffset (was %d, now %d) — nothing to clamp", maxOffset, maxOffset2)
	}
	if m2.helpOffset > maxOffset2 {
		t.Fatalf("expected helpOffset clamped to <= %d after resize, got %d", maxOffset2, m2.helpOffset)
	}
	if m2.helpOffset < 0 {
		t.Errorf("expected helpOffset non-negative after resize, got %d", m2.helpOffset)
	}

	// After clamping, pressing k must decrement by exactly 1 — confirming
	// the offset is in sync with the renderer.
	if m2.helpOffset == 0 {
		return
	}
	before := m2.helpOffset
	m3 := pressKey(m2, "k")
	if m3.helpOffset != before-1 {
		t.Errorf("expected k to decrement to %d, got %d", before-1, m3.helpOffset)
	}
}
