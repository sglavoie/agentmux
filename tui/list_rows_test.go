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
	harnessColor := map[string]string{}

	unlocked := stripAnsi(formatRow(s, acctColor, harnessColor, 120, ""))
	if strings.Contains(unlocked, palette.LockIcon()) {
		t.Fatalf("unlocked row should not contain %q; got %q", palette.LockIcon(), unlocked)
	}

	s.Locked = true
	locked := stripAnsi(formatRow(s, acctColor, harnessColor, 120, ""))
	if !strings.Contains(locked, palette.LockIcon()) {
		t.Fatalf("locked row should contain %q; got %q", palette.LockIcon(), locked)
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
