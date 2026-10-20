package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestRenderListRowsOpenWithNamesRatherThanTabNumbers covers the tab-number
// prefix at the render level, where it used to be drawn. It was the widest of
// the operational prefixes — a right-aligned number and a colon in front of
// every row, sized for the widest tab on screen — and the queue opens with the
// session's name instead. The two-digit fixture is deliberate: a renderer that
// merely stopped printing the number but still reserved its column would leave
// the same leading blanks, and only checking the prefix exactly catches that.
func TestRenderListRowsOpenWithNamesRatherThanTabNumbers(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width = 120
	m = loadSessions(m, []session.RunningSession{
		{TabID: 20, TabNumber: 2, WindowID: 1, Account: "work", Base: "api", Directory: "/work/api"},
		{TabID: 120, TabNumber: 12, WindowID: 2, Account: "work", Base: "backend", Directory: "/work/backend"},
	})

	lines := strings.Split(strings.TrimRight(stripAnsi(renderListAt(m, m.width, 0)), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered lines = %d, want a heading and 2 rows: %q", len(lines), lines)
	}
	for i, want := range []string{"api", "backend"} {
		row := lines[i+1]
		if !strings.HasPrefix(row, want) {
			t.Errorf("row %d = %q, want it to open with %q", i, row, want)
		}
		if strings.Contains(row, ": ") {
			t.Errorf("row %d still carries a tab-number prefix: %q", i, row)
		}
	}
}

// TestRenderListLockedRowIsDimmed verifies that the per-row styling switch in
// renderListAt wraps a locked row in the explicit muted foreground while also
// marking its name with the lock glyph.
func TestRenderListLockedRowIsDimmed(t *testing.T) {
	t.Parallel()

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

	if strings.Contains(stripAnsi(unlocked), "🔒") {
		t.Fatalf("unlocked row carries a lock glyph: %q", stripAnsi(unlocked))
	}
	if !strings.Contains(stripAnsi(locked), "🔒 api") {
		t.Fatalf("locked row should mark the session name: %q", stripAnsi(locked))
	}

	mutedPrefix := strings.Split(styleDim.Render("api"), "api")[0]
	if !strings.Contains(locked, mutedPrefix) {
		t.Fatalf("locked row should carry the muted foreground; got %q", locked)
	}
}

// TestSupervisionMarkerMovedOffTheRowIntoTheSelectedContext covers the last of
// the retired row columns. The 🔁 marker used to be a column the whole list paid
// for whenever any visible row was supervised — which is why the old tests here
// were about its ALIGNMENT and about omitting it when no visible row matched.
// Neither question exists now: who is driving a session is selected-session
// context, so the list never reserves anything for it and the strip says it for
// the one row the operator is on.
func TestSupervisionMarkerMovedOffTheRowIntoTheSelectedContext(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height = 120, 40
	m = loadSessions(m, []session.RunningSession{
		{WindowID: 1, Account: "work", Base: "frontend", Directory: "/work/frontend"},
		{WindowID: 2, Account: "work", Base: "backend", Directory: "/work/backend", Looping: true},
	})

	if out := stripAnsi(renderListAt(m, m.width, 0)); strings.Contains(out, "🔁") {
		t.Fatalf("the supervision marker is back in the list:\n%s", out)
	}

	// Put the cursor on the supervised session; the inspector is what answers.
	for i := range m.items.Items {
		if s, ok := m.items.Session(m.items.Items[i]); ok && s.Base == "backend" {
			m.cursor = i
		}
	}
	requireInspector(t, m)
	pane := stripAnsi(renderPreviewPane(m, 60, 16))
	if !strings.Contains(pane, "🔁") {
		t.Fatalf("the selected supervised session is not marked in its context:\n%s", pane)
	}
	if !strings.Contains(pane, "backend") {
		t.Fatalf("the inspector is describing some other session:\n%s", pane)
	}
}

// TestRenderListRowsFitNarrowWidth is the surviving half of the retired
// loop-column test: whatever the list composes, no line may draw past the pane.
// The fixture keeps the long directory and the two-digit tab number, which are
// now facts the row does not print — so a renderer that resurrected either
// column would blow the budget here first.
func TestRenderListRowsFitNarrowWidth(t *testing.T) {
	t.Parallel()

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
