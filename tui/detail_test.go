package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// detailModel returns a model parked in ModeDetail with the cursor's
// WindowID stored on the detail fields. Tests then mutate detailEvents /
// detailLoading / detailErr / detailScroll as needed.
func detailModel(t *testing.T) Model {
	t.Helper()
	m := loadSessions(testModel(), detailSessions())
	mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
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
	m.detailEvents = []daemon.SessionEvent{
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
}

func TestRenderDetailSuppressedRow(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []daemon.SessionEvent{
		{At: 100, Event: "Stop", Action: "stop", Suppressed: true},
	}

	out := stripAnsi(renderDetail(m))

	if !strings.Contains(out, "⊘") {
		t.Errorf("expected suppression marker ⊘ in output, got:\n%s", out)
	}
}

func TestRenderDetailToolNameVisible(t *testing.T) {
	m := detailModel(t)
	m.detailEvents = []daemon.SessionEvent{
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
func makeEvents(n int) []daemon.SessionEvent {
	out := make([]daemon.SessionEvent, n)
	for i := 0; i < n; i++ {
		out[i] = daemon.SessionEvent{
			At:     int64(i),
			Event:  "PreToolUse",
			Action: "active",
		}
	}
	return out
}

func TestRenderDetailScrollIndicatorAtTop(t *testing.T) {
	m := detailModel(t)
	m.height = detailChrome + detailMinVisible // visible == 3
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
	m.height = detailChrome + detailMinVisible // visible == 3
	m.detailEvents = makeEvents(60)
	total, visible := detailScrollBounds(m)
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
	m.height = detailChrome + detailMinVisible
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

func TestFormatRelativeAge(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"just now", now, "0s ago"},
		{"12 seconds", now.Add(-12 * time.Second), "12s ago"},
		{"3 minutes", now.Add(-3 * time.Minute), "3m ago"},
		{"59 minutes", now.Add(-59 * time.Minute), "59m ago"},
		{"1 hour", now.Add(-time.Hour), "1h ago"},
		{"23 hours", now.Add(-23 * time.Hour), "23h ago"},
		{"1 day", now.Add(-25 * time.Hour), "1d ago"},
		{"future clamps to zero", now.Add(5 * time.Second), "0s ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRelativeAge(tc.at, now)
			if got != tc.want {
				t.Errorf("formatRelativeAge: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDetailPageScrollKeys(t *testing.T) {
	m := detailModel(t)
	m.height = detailChrome + detailMinVisible // visible == 3
	m.detailEvents = makeEvents(20)

	_, visible := detailScrollBounds(m)
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
