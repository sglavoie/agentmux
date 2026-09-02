package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// detailModel returns a model parked in ModeDetail with the cursor's
// WindowID stored on the detail fields. Tests then mutate detailEvents /
// detailLoading / detailErr / detailScroll as needed.
func detailModel(t *testing.T) Model {
	t.Helper()
	m := loadSessions(testModel(), detailSessions())
	mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID, false)
	m = mm.(Model)
	m.height = 30 // generous default; specific tests override
	m.width = 100
	m.detailLoading = false
	return m
}

func TestRenderDetailLoading(t *testing.T) {
	m := detailModel(t)
	m.detailLoading = true
	m.detailEvents = nil

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "Loading…") {
		t.Errorf("expected loading indicator in detail output, got:\n%s", out)
	}
}

func TestRenderDetailEmpty(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = nil

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "No events recorded") {
		t.Errorf("expected empty-state message in detail output, got:\n%s", out)
	}
}

func TestRenderDetailPopulatedMostRecentFirst(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "SessionStart", Action: "active"},
		{At: 200, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
		{At: 300, Event: "Stop", Action: "stop"},
	}

	out := stripAnsi(renderDetail(m))

	for _, want := range []string{"SessionStart", "PreToolUse", "Stop", "Bash"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in detail output, got:\n%s", want, out)
		}
	}
	// Most-recent-first: Stop (At=300) should appear before SessionStart (At=100).
	stopIdx := strings.Index(out, "Stop")
	startIdx := strings.Index(out, "SessionStart")
	if stopIdx < 0 || startIdx < 0 {
		t.Fatalf("expected both Stop and SessionStart in output, got stopIdx=%d startIdx=%d:\n%s", stopIdx, startIdx, out)
	}
	if stopIdx >= startIdx {
		t.Errorf("expected Stop (newest) before SessionStart (oldest); stopIdx=%d startIdx=%d output:\n%s", stopIdx, startIdx, out)
	}
	// A terminal event in the buffer must emit a "session ended" separator
	// between the terminal row and the older (post-mortem) rows below it.
	// The separator's trailing horizontal-rule glyph distinguishes it from
	// the header's "session ended" meta tag.
	sepIdx := strings.Index(out, "session ended ─")
	if sepIdx < 0 {
		t.Errorf("expected `session ended` separator after Stop row, got:\n%s", out)
	} else if stopIdx >= sepIdx || sepIdx >= startIdx {
		t.Errorf("expected order Stop < separator < SessionStart; got stopIdx=%d sepIdx=%d startIdx=%d output:\n%s", stopIdx, sepIdx, startIdx, out)
	}
}

func TestRenderDetailSuppressedRow(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "Stop", Action: "stop", Suppressed: true},
	}

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "⊘") {
		t.Errorf("expected suppression marker ⊘ in output, got:\n%s", out)
	}
}

func TestRenderDetailToolNameVisible(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
	}

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "Bash") {
		t.Errorf("expected tool name Bash in output, got:\n%s", out)
	}
}

func TestRenderDetailError(t *testing.T) {
	m := detailModel(t)
	m.detailErr = errors.New("daemon offline")

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "daemon offline") {
		t.Errorf("expected error message in output, got:\n%s", out)
	}
	if !strings.Contains(out, "amx daemon status") {
		t.Errorf("expected diagnostic hint in output, got:\n%s", out)
	}
}

// makeEvents returns n synthetic events with increasing timestamps so the
// most-recent-first display has a stable order across the scroll tests.
func makeEvents(n int) []protocol.SessionEvent {
	out := make([]protocol.SessionEvent, n)
	for i := 0; i < n; i++ {
		out[i] = protocol.SessionEvent{
			At:     int64(i),
			Event:  "PreToolUse",
			Action: "active",
		}
	}
	return out
}

func TestRenderDetailScrollIndicatorAtTop(t *testing.T) {
	m := detailModel(t)
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.detailEvents = makeEvents(60)
	m.detailScroll = 0

	out := stripAnsi(renderDetail(m))

	if strings.Contains(out, "▲") {
		t.Errorf("expected no scroll-up indicator at offset=0, got:\n%s", out)
	}
	if !strings.Contains(out, "▼") {
		t.Errorf("expected scroll-down indicator with 60 events and offset=0, got:\n%s", out)
	}
}

func TestRenderDetailScrollIndicatorAtBottom(t *testing.T) {
	m := detailModel(t)
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.detailEvents = makeEvents(60)
	total, visible := detailScrollBounds(m, detailVisibleHeight(m))
	m.detailScroll = total - visible

	out := stripAnsi(renderDetail(m))

	if strings.Contains(out, "▼") {
		t.Errorf("expected no scroll-down indicator at max offset, got:\n%s", out)
	}
	if !strings.Contains(out, "▲") {
		t.Errorf("expected scroll-up indicator at max offset, got:\n%s", out)
	}
}

func TestRenderDetailFooterCount(t *testing.T) {
	m := detailModel(t)
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.detailEvents = makeEvents(60)
	m.detailScroll = 0

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "of 60") {
		t.Errorf("expected `of 60` in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "d/esc close") {
		t.Errorf("expected key hint `d/esc close` in footer, got:\n%s", out)
	}
}

func TestDetailPageScrollKeys(t *testing.T) {
	m := detailModel(t)
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.detailEvents = makeEvents(20)

	_, visible := detailScrollBounds(m, detailVisibleHeight(m))
	maxOffset := len(m.detailEvents) - visible
	if maxOffset <= visible {
		t.Fatalf("test pre-condition: expected maxOffset > visible (%d) for meaningful page scroll, got %d", visible, maxOffset)
	}

	t.Run("pgdown advances by one page", func(t *testing.T) {
		mm := m
		mm = pressKey(mm, "pgdown")
		if mm.detailScroll != visible {
			t.Errorf("expected detailScroll=%d after pgdown, got %d", visible, mm.detailScroll)
		}
	})

	t.Run("pgup retreats by one page", func(t *testing.T) {
		mm := m
		mm.detailScroll = visible
		mm = pressKey(mm, "pgup")
		if mm.detailScroll != 0 {
			t.Errorf("expected detailScroll=0 after pgup, got %d", mm.detailScroll)
		}
	})

	t.Run("g jumps to top", func(t *testing.T) {
		mm := m
		mm.detailScroll = maxOffset
		mm = pressKey(mm, "g")
		if mm.detailScroll != 0 {
			t.Errorf("expected detailScroll=0 after g, got %d", mm.detailScroll)
		}
	})

	t.Run("G jumps to bottom", func(t *testing.T) {
		mm := m
		mm = pressKey(mm, "G")
		if mm.detailScroll != maxOffset {
			t.Errorf("expected detailScroll=%d after G, got %d", maxOffset, mm.detailScroll)
		}
	})

	t.Run("pgdown clamps at max", func(t *testing.T) {
		mm := m
		mm.detailScroll = maxOffset
		mm = pressKey(mm, "pgdown")
		if mm.detailScroll != maxOffset {
			t.Errorf("expected detailScroll to stay at maxOffset=%d, got %d", maxOffset, mm.detailScroll)
		}
	})
}

func TestRenderDetail_HeaderShowsSessionEnded(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "SessionStart", Action: "active"},
		{At: 200, Event: "Stop", Action: "stop"},
	}

	header := stripAnsi(detailHeader(m))

	if !strings.Contains(header, "session ended") {
		t.Errorf("expected `session ended` in header meta line, got:\n%s", header)
	}
}

func TestRenderDetail_SessionEndedSeparatorRendered(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "SessionStart", Action: "active"},
		{At: 200, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
		{At: 300, Event: "Stop", Action: "stop"},
	}

	out := stripAnsi(renderDetail(m))

	// "session ended ─" only appears in the separator (the header suffix
	// has no trailing horizontal rule).
	sepIdx := strings.Index(out, "session ended ─")
	stopIdx := strings.LastIndex(out, "Stop")
	startIdx := strings.Index(out, "SessionStart")
	if sepIdx < 0 {
		t.Fatalf("expected separator with horizontal rule in output, got:\n%s", out)
	}
	if stopIdx < 0 || startIdx < 0 {
		t.Fatalf("expected Stop and SessionStart in output, got stopIdx=%d startIdx=%d:\n%s", stopIdx, startIdx, out)
	}
	if stopIdx >= sepIdx || sepIdx >= startIdx {
		t.Errorf("expected separator between Stop and SessionStart; stopIdx=%d sepIdx=%d startIdx=%d output:\n%s", stopIdx, sepIdx, startIdx, out)
	}
}

func TestRenderDetail_NoSeparatorForActiveSession(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "SessionStart", Action: "active"},
		{At: 200, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
	}

	out := stripAnsi(renderDetail(m))

	if strings.Contains(out, "session ended") {
		t.Errorf("expected no `session ended` marker for an active session, got:\n%s", out)
	}
}

func TestRenderDetail_FailureDoesNotMarkSessionEnded(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []protocol.SessionEvent{
		{At: 100, Event: "SessionStart", Action: "active"},
		{At: 200, Event: "PostToolUseFailure", Action: "failure", ToolName: "Bash"},
	}

	out := stripAnsi(renderDetail(m))

	if strings.Contains(out, "session ended") {
		t.Errorf("expected no `session ended` marker for a recoverable failure, got:\n%s", out)
	}
}

// TestDetailScrollClampsOnResize verifies that a viewport expansion via a
// WindowSizeMsg drops m.detailScroll into the new [0, maxOffset] range.
// When the terminal grows taller, visible grows and maxOffset shrinks; the
// pre-clamp bug left m.detailScroll above the new maxOffset so the next
// j/k press had to scroll up through dead rows before the viewport reacted.
func TestDetailScrollClampsOnResize(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = makeEvents(20)

	// Small viewport: scroll to bottom (maxOffset is large).
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	_, visible := detailScrollBounds(m, detailVisibleHeight(m))
	maxOffset := len(m.detailEvents) - visible
	if maxOffset <= 0 {
		t.Fatalf("setup: expected non-zero maxOffset at small height, got %d", maxOffset)
	}
	m.detailScroll = maxOffset

	// Grow the viewport — visible expands, maxOffset shrinks. The stale
	// offset must be clamped to the new maxOffset.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m2 := updated.(Model)

	_, visible2 := detailScrollBounds(m2, detailVisibleHeight(m2))
	maxOffset2 := len(m2.detailEvents) - visible2
	if maxOffset2 < 0 {
		maxOffset2 = 0
	}
	if maxOffset2 >= maxOffset {
		t.Fatalf("setup: expected resize to shrink maxOffset, was %d still %d", maxOffset, maxOffset2)
	}
	if m2.detailScroll > maxOffset2 {
		t.Fatalf("expected detailScroll clamped to <= %d after resize, got %d", maxOffset2, m2.detailScroll)
	}
	if m2.detailScroll < 0 {
		t.Errorf("expected detailScroll non-negative, got %d", m2.detailScroll)
	}

	// Sanity: pressing k now should immediately move the viewport (the
	// pre-fix bug would have required N stale-state keypresses before k
	// produced any visible change).
	if m2.detailScroll == 0 {
		return
	}
	before := m2.detailScroll
	m3 := pressKey(m2, "k")
	if m3.detailScroll != before-1 {
		t.Errorf("expected k to decrement to %d, got %d", before-1, m3.detailScroll)
	}
}
