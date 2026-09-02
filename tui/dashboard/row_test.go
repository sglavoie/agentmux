package dashboard

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

	unlocked := stripAnsi(FormatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if strings.Contains(unlocked, palette.LockIcon()) {
		t.Fatalf("unlocked row should not contain %q; got %q", palette.LockIcon(), unlocked)
	}

	s.Locked = true
	locked := stripAnsi(FormatRow(s, acctColor, nil, nil, 120, "", "", ""))
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

	unmuted := stripAnsi(FormatRow(s, acctColor, nil, nil, 120, "", "", ""))
	if strings.Contains(unmuted, palette.MuteIcon()) {
		t.Fatalf("unmuted row should not contain %q; got %q", palette.MuteIcon(), unmuted)
	}

	s.Muted = true
	muted := stripAnsi(FormatRow(s, acctColor, nil, nil, 120, "", "", ""))
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
