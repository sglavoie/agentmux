package tui

import (
	"strings"
	"testing"
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
