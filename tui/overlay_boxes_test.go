package tui

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
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

func TestRenderActionsBoxExpandsForActionRows(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	m.width, m.height = 120, 40

	raw := renderActionsBox(m)
	lines := strings.Split(strings.TrimRight(stripAnsi(raw), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("expected actions box output")
	}
	if w := runewidth.StringWidth(lines[0]); w <= 70 {
		t.Fatalf("box width = %d, want wider than old 70-column cap:\n%s", w, stripAnsi(raw))
	}

	var editorLine string
	for _, line := range lines {
		if strings.Contains(line, "O   open in editor") {
			editorLine = line
			break
		}
	}
	if editorLine == "" {
		t.Fatalf("expected open-in-editor row in output:\n%s", stripAnsi(raw))
	}
	if !strings.Contains(editorLine, "$VISUAL/$EDITOR (or defaults.editor)") {
		t.Fatalf("open-in-editor row should keep description on one line, got %q\nfull output:\n%s", editorLine, stripAnsi(raw))
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

func TestRenderConfigPickerBoxExpandsForProfileRows(t *testing.T) {
	m := testModel()
	m.cfg.HarnessEmojis = map[string]string{
		"claude":  "⭕",
		"codex":   "📟",
		"copilot": "🚁",
	}
	m.cfg.Accounts = []config.Account{
		{Name: "personal-codex", Color: "purple", Harness: "codex", HomeDir: "/Users/sglavoie/.codex"},
		{Name: "personal-claude", Color: "yellow", Harness: "claude", HomeDir: "/Users/sglavoie/.claude"},
		{Name: "tradition-codex", Color: "purple", Harness: "codex", HomeDir: "/Users/sglavoie/.codex"},
		{Name: "tradition-claude", Color: "blue", Harness: "claude", HomeDir: "/Users/sglavoie/.claude-personal"},
		{Name: "vta-codex", Color: "purple", Harness: "codex", HomeDir: "/Users/sglavoie/.codex"},
		{Name: "vta-claude", Color: "yellow", Harness: "claude", HomeDir: "/Users/sglavoie/.claude"},
		{Name: "copilot", Color: "green", Harness: "copilot", HomeDir: "/Users/sglavoie/.copilot"},
	}
	m.targetSession = session.RunningSession{TabID: 1, Base: "🧠BrainNotes", Project: "🧠BrainNotes", Account: "vta-claude", Harness: "claude"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerCurrent = "vta-claude"
	m.configPickerCursor = 5
	m.configPickerPurpose = ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker
	m.mode = ModeConfigPicker
	m.width, m.height = 120, 40

	raw := renderConfigPickerBox(m)
	lines := strings.Split(strings.TrimRight(stripAnsi(raw), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("expected config picker box output")
	}
	if w := runewidth.StringWidth(lines[0]); w <= 60 {
		t.Fatalf("box width = %d, want wider than old 60-column cap:\n%s", w, stripAnsi(raw))
	}

	var currentLine string
	for _, line := range lines {
		if strings.Contains(line, "vta-claude") {
			currentLine = line
			break
		}
	}
	if currentLine == "" {
		t.Fatalf("expected vta-claude row in output:\n%s", stripAnsi(raw))
	}
	for _, want := range []string{"⭕ claude", "/Users/sglavoie/.claude", "(current)"} {
		if !strings.Contains(currentLine, want) {
			t.Fatalf("current row should keep %q on one line, got %q\nfull output:\n%s", want, currentLine, stripAnsi(raw))
		}
	}
}

func TestRenderConfigPickerBoxNarrowClamps(t *testing.T) {
	m := testModel()
	m.cfg.HarnessEmojis = map[string]string{"claude": "⭕"}
	m.cfg.Accounts = []config.Account{
		{Name: "tradition-claude", Color: "blue", Harness: "claude", HomeDir: "/Users/sglavoie/.claude-personal"},
	}
	m.targetSession = session.RunningSession{TabID: 1, Base: "🧠BrainNotes", Project: "🧠BrainNotes", Account: "tradition-claude", Harness: "claude"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerCurrent = "tradition-claude"
	m.configPickerCursor = 0
	m.configPickerPurpose = ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker
	m.mode = ModeConfigPicker
	m.width, m.height = 40, 20

	raw := renderConfigPickerBox(m)
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if w := runewidth.StringWidth(stripAnsi(line)); w > m.width {
			t.Errorf("box line exceeded m.width=%d (got %d): %q", m.width, w, line)
		}
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

// TestOverlayBoxesFitAtWidthViaModalDetector ties the actions and config-picker
// box sizing (the regression the "size box to fit its widest row" commits fixed)
// to the harness modal-fit detector: at a wide terminal both boxes must render
// at their natural width, so the detector reports no width overflow. If a future
// change reintroduces a fixed-width cap that clips the widest row, the detector's
// natural-vs-rendered width comparison fails here.
func TestOverlayBoxesFitAtWidthViaModalDetector(t *testing.T) {
	actions := loadSessions(testModelWithSessions(), makeSessions())
	actions = pressKey(actions, "a")
	actions.width, actions.height = 140, 50
	if hasIssueType(detectModalFitIssues(actions), UITestIssueModalWidthOverflow) {
		t.Errorf("actions box should render at natural width at 140x50; detector reported width overflow")
	}

	cp := configPickerModalAt(140, 50)
	if hasIssueType(detectModalFitIssues(cp), UITestIssueModalWidthOverflow) {
		t.Errorf("config picker box should render at natural width at 140x50; detector reported width overflow")
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
