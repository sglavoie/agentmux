package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestFormatRowLockIconPresentWhenLocked verifies that a locked session
// renders the 🔒 glyph in the new lock column and an unlocked session does
// not.
func TestFormatRowLockIconPresentWhenLocked(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Title:     "api",
		Base:      "api",
		Directory: "/tmp",
	}
	acctColor := map[string]string{"work": "blue"}

	unlocked := stripAnsi(formatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if strings.Contains(unlocked, palette.LockIcon()) {
		t.Fatalf("unlocked row should not contain %q; got %q", palette.LockIcon(), unlocked)
	}

	s.Locked = true
	locked := stripAnsi(formatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if !strings.Contains(locked, palette.LockIcon()) {
		t.Fatalf("locked row should contain %q; got %q", palette.LockIcon(), locked)
	}
}

// TestFormatRowMuteIconPresentWhenMuted verifies that a muted session renders
// the 🔇 glyph in the mute column and an unmuted session does not.
func TestFormatRowMuteIconPresentWhenMuted(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Title:     "api",
		Base:      "api",
		Directory: "/tmp",
	}
	acctColor := map[string]string{"work": "blue"}

	unmuted := stripAnsi(formatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if strings.Contains(unmuted, palette.MuteIcon()) {
		t.Fatalf("unmuted row should not contain %q; got %q", palette.MuteIcon(), unmuted)
	}

	s.Muted = true
	muted := stripAnsi(formatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if !strings.Contains(muted, palette.MuteIcon()) {
		t.Fatalf("muted row should contain %q; got %q", palette.MuteIcon(), muted)
	}
}

func TestTabNumberColumnAlignsSingleAndMultiDigitNumbers(t *testing.T) {
	sessions := []session.RunningSession{
		{TabNumber: 2},
		{TabNumber: 12},
		{},
	}
	width := tabNumberColumnWidth(sessions)
	if width != 4 {
		t.Fatalf("tab number column width = %d, want 4", width)
	}
	if got := renderTabNumberCell(2, width); got != " 2: " {
		t.Errorf("single-digit cell = %q, want %q", got, " 2: ")
	}
	if got := renderTabNumberCell(12, width); got != "12: " {
		t.Errorf("multi-digit cell = %q, want %q", got, "12: ")
	}
	if got := renderTabNumberCell(0, width); got != "    " {
		t.Errorf("unknown cell = %q, want four spaces", got)
	}
}

func TestRenderListPrefixesRowsWithTabNumbers(t *testing.T) {
	m := testModel()
	m.width = 120
	m = loadSessions(m, []session.RunningSession{
		{TabID: 20, TabNumber: 2, WindowID: 1, Account: "work", Base: "api", Directory: "/work/api"},
		{TabID: 120, TabNumber: 12, WindowID: 2, Account: "work", Base: "backend", Directory: "/work/backend"},
	})

	lines := strings.Split(strings.TrimRight(stripAnsi(renderListAt(m, m.width, 0)), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered lines = %d, want 2: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], " 2: ") || !strings.Contains(lines[0], "api") {
		t.Errorf("first row = %q, want tab 2 api row", lines[0])
	}
	if !strings.HasPrefix(lines[1], "12: ") || !strings.Contains(lines[1], "backend") {
		t.Errorf("second row = %q, want tab 12 backend row", lines[1])
	}
}

// TestRenderListLockedRowIsDimmed verifies that the per-row styling switch
// in renderListAt wraps a locked row in styleDim. Because styleDim emits the
// Faint (\x1b[2m) ANSI escape and the locked-row case sits above the
// attention-amber branch, an unlocked row with no other dim spans renders
// without that escape sequence around the whole row.
func TestRenderListLockedRowIsDimmed(t *testing.T) {
	build := func(locked bool) Model {
		m := testModel()
		m.width = 120
		s := session.RunningSession{
			TabID:     1,
			WindowID:  42,
			Account:   "work",
			Project:   "api",
			Title:     "api",
			Base:      "api",
			Directory: "/tmp",
			State:     "bolt",
			Locked:    locked,
		}
		m = loadSessions(m, []session.RunningSession{s})
		// Move cursor off the row so the selection style does not mask the
		// locked-row dim wrap.
		m.cursor = -1
		return m
	}

	unlocked := renderListAt(build(false), 120, 0)
	locked := renderListAt(build(true), 120, 0)

	// Plain text must differ because the locked row carries the 🔒 glyph in
	// the lock column.
	if stripAnsi(unlocked) == stripAnsi(locked) {
		t.Fatalf("expected plain text to differ between locked and unlocked rows\nunlocked=%q\nlocked=%q", stripAnsi(unlocked), stripAnsi(locked))
	}

	// styleDim renders as \x1b[2m...\x1b[0m. The locked row must contain it.
	if !strings.Contains(locked, "\x1b[2m") {
		t.Fatalf("locked row should carry the dim ANSI escape; got %q", locked)
	}
}

func TestRenderListActiveLoopColumnAlignsRows(t *testing.T) {
	m := testModel()
	m.width = 120
	m = loadSessions(m, []session.RunningSession{
		{WindowID: 1, Account: "work", Base: "frontend", Directory: "/work/frontend"},
		{WindowID: 2, Account: "work", Base: "backend", Directory: "/work/backend", Looping: true},
	})

	out := stripAnsi(renderListAt(m, m.width, 0))
	if count := strings.Count(out, "🔁"); count != 1 {
		t.Fatalf("active loop indicator count = %d, want 1 in:\n%s", count, out)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered lines = %d, want 2:\n%s", len(lines), out)
	}
	frontTitle := idxTitle(lines[0], "frontend")
	backTitle := idxTitle(lines[1], "backend")
	if frontTitle < 0 || backTitle < 0 {
		t.Fatalf("title offsets not found: frontend=%d backend=%d lines=%q", frontTitle, backTitle, lines)
	}
	if frontTitle != backTitle {
		t.Fatalf("title columns differ with active-loop column: frontend=%d backend=%d lines=%q", frontTitle, backTitle, lines)
	}
}

func TestRenderListOmitsActiveLoopColumnWhenNoVisibleRowMatches(t *testing.T) {
	m := testModel()
	m.width = 120
	m = loadSessions(m, []session.RunningSession{
		{WindowID: 1, Account: "work", Base: "visible", Directory: "/work/visible"},
		{WindowID: 2, Account: "work", Base: "middle", Directory: "/work/middle"},
		{WindowID: 3, Account: "work", Base: "hidden", Directory: "/work/hidden"},
	})
	m.sessions[2].Looping = true
	m.filtered[2].Looping = true
	m.height = 12
	m.listOffset = 0

	out := stripAnsi(renderListAt(m, m.width, 2))
	if strings.Contains(out, "🔁") {
		t.Fatalf("active loop indicator should be omitted when only off-window rows match:\n%s", out)
	}
	if !strings.Contains(out, "visible") {
		t.Fatalf("test fixture should render the visible row:\n%s", out)
	}
}

func TestRenderListActiveLoopColumnFitsNarrowWidth(t *testing.T) {
	const width = 56
	m := testModel()
	m.width = width
	m = loadSessions(m, []session.RunningSession{
		{
			TabNumber: 12,
			WindowID:  1,
			Account:   "work",
			Base:      "frontend-service",
			Directory: "/a/very/long/project/path/that/must/truncate/frontend",
			Looping:   true,
		},
	})

	out := renderListAt(m, width, 0)
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := terminalWidth(stripAnsi(line)); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, stripAnsi(line))
		}
	}
}
