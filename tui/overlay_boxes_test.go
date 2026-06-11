package tui

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/session"
)

// Smoke tests for the centered-overlay box wrappers introduced when the
// inline modes were converted to floating modals. Each test verifies the
// rounded border character renders, the inner header survives the wrap, and
// the box width clamps to m.width-4 on narrow terminals.

func TestRenderActionsBox(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	if m.mode != ModeActions {
		t.Fatalf("expected ModeActions after a, got %d", m.mode)
	}
	m.width, m.height = 100, 40

	raw := renderActionsBox(m)
	if !strings.Contains(raw, "╭") {
		t.Errorf("expected rounded top-left glyph in box output; got %q", raw)
	}
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Actions for") {
		t.Errorf("expected 'Actions for' header in box output; got %q", plain)
	}
	if !strings.Contains(plain, "esc/q to back out") {
		t.Errorf("expected refreshed esc/q hint in box output; got %q", plain)
	}
}

func TestRenderActionsBoxNarrowClamps(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	m.width, m.height = 30, 20

	raw := renderActionsBox(m)
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if w := runewidth.StringWidth(stripAnsi(line)); w > m.width {
			t.Errorf("box line exceeded m.width=%d (got %d): %q", m.width, w, line)
		}
	}
}

func TestRenderConfirmBox(t *testing.T) {
	m := modalConfirmModel()
	m = loadSessions(m, makeSessions())
	m.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	m.confirmAction = "close"
	m.confirmConsequence = closeConsequence
	m.mode = ModeConfirm
	m.width, m.height = 100, 40

	raw := renderConfirmBox(m)
	if !strings.Contains(raw, "╭") {
		t.Errorf("expected rounded top-left glyph in box output; got %q", raw)
	}
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Close") {
		t.Errorf("expected Close verb in box output; got %q", plain)
	}
	if !strings.Contains(plain, "transcript persists") {
		t.Errorf("expected consequence text in box output; got %q", plain)
	}
}

func TestRenderConfigPickerBox(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerCurrent = "work"
	m.configPickerCursor = 0
	m.configPickerPurpose = ConfigPickerRestartProfile
	m.configPickerReturnMode = ModeList
	m.mode = ModeConfigPicker
	m.width, m.height = 100, 40

	raw := renderConfigPickerBox(m)
	if !strings.Contains(raw, "╭") {
		t.Errorf("expected rounded top-left glyph in box output; got %q", raw)
	}
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Restart with profile") {
		t.Errorf("expected 'Restart with profile' header in box output; got %q", plain)
	}
}

func TestRenderConfigPickerBoxLaunchHeader(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerPurpose = ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker
	m.mode = ModeConfigPicker
	m.width, m.height = 100, 40

	plain := stripAnsi(renderConfigPickerBox(m))
	if !strings.Contains(plain, "Launch profile") {
		t.Errorf("expected 'Launch profile' header when returning to ModePicker; got %q", plain)
	}
}

func TestRenderConfigPickerBoxDashboardLaunchHeader(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerPurpose = ConfigPickerDashboardLaunch
	m.configPickerReturnMode = ModeList
	m.mode = ModeConfigPicker
	m.width, m.height = 100, 40

	plain := stripAnsi(renderConfigPickerBox(m))
	if !strings.Contains(plain, "Launch new session with profile") {
		t.Errorf("expected dashboard launch header, got %q", plain)
	}
}

func TestRenderHelpBox(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.width, m.height = 100, 40

	raw := renderHelpBox(m)
	if !strings.Contains(raw, "╭") {
		t.Errorf("expected rounded top-left glyph in box output; got %q", raw)
	}
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Keyboard Shortcuts") {
		t.Errorf("expected 'Keyboard Shortcuts' header in box output; got %q", plain)
	}
}

func TestRenderHelpBoxNarrowClamps(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.width, m.height = 30, 20

	raw := renderHelpBox(m)
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if w := runewidth.StringWidth(stripAnsi(line)); w > m.width {
			t.Errorf("box line exceeded m.width=%d (got %d): %q", m.width, w, line)
		}
	}
}

func TestDashboardOverlaysReserveStatusBar(t *testing.T) {
	actions := loadSessions(testModelWithSessions(), makeSessions())
	actions = pressKey(actions, "a")

	confirm := loadSessions(modalConfirmModel(), makeSessions())
	confirm.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	confirm.confirmAction = "close"
	confirm.confirmConsequence = closeConsequence
	confirm.mode = ModeConfirm

	configPicker := loadSessions(testModelWithSessions(), makeSessions())
	configPicker.targetSession = session.RunningSession{TabID: 1, Base: "api", Account: "work"}
	configPicker.configPickerAccounts = configPicker.cfg.Accounts
	configPicker.configPickerCurrent = "work"
	configPicker.configPickerPurpose = ConfigPickerRestartProfile
	configPicker.configPickerReturnMode = ModeList
	configPicker.mode = ModeConfigPicker

	help := loadSessions(testModel(), makeSessions())
	help.mode = ModeHelp

	detail := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 42, Base: "api", Account: "work"},
	})
	detail.detailWindowID = 42
	detail.mode = ModeDetail

	cases := []struct {
		name   string
		m      Model
		popup  string
		status string
	}{
		{name: "actions", m: actions, popup: "Actions for", status: "Session actions"},
		{name: "confirm", m: confirm, popup: "Close", status: "Confirm close"},
		{name: "config picker", m: configPicker, popup: "Restart with profile", status: "Choose profile"},
		{name: "help", m: help, popup: "Keyboard Shortcuts", status: "Help"},
		{name: "detail", m: detail, popup: "api", status: "Session details: window 42"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.m
			m.width = 90
			m.height = 12
			out := stripAnsi(m.render())
			lines := strings.Split(out, "\n")
			if len(lines) != m.height {
				t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
			}
			if !strings.Contains(out, tc.popup) {
				t.Fatalf("expected popup marker %q in overlay output:\n%s", tc.popup, out)
			}
			last := strings.TrimSpace(lines[len(lines)-1])
			if !strings.Contains(last, tc.status) {
				t.Fatalf("final row = %q, want status %q", last, tc.status)
			}
		})
	}
}

func TestDashboardOverlayTinyTerminalDoesNotOverflowStatusBar(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	m.width = 50
	m.height = 3

	out := stripAnsi(m.render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); !strings.Contains(last, "Session actions") {
		t.Fatalf("final row = %q, want actions status", last)
	}
}
