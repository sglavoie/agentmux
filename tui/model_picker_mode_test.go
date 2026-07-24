package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestLKeyOpensPicker(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	if len(m.pickerSessions) != 3 {
		t.Fatalf("expected 3 picker sessions (from config), got %d", len(m.pickerSessions))
	}
}

func TestPickerEscReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from picker, got %d", m.mode)
	}
}

func TestPickerSpaceTogglesSelection(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	// pickerCursor=0, not selected yet
	if m.pickerSelected[0] {
		t.Fatal("expected session not selected initially")
	}
	m = pressKey(m, " ") // toggle on
	if !m.pickerSelected[0] {
		t.Fatal("expected session selected after space")
	}
	m = pressKey(m, " ") // toggle off
	if m.pickerSelected[0] {
		t.Fatal("expected session deselected after second space")
	}
}

func TestPickerEnterEmitsLaunchCmd(t *testing.T) {
	// No running sessions so this asserts the plain picker launch path without
	// also exercising duplicate-instance behavior.
	m := loadSessions(testModelWithSessions(), nil)
	m = pressKey(m, "l")
	m = pressKey(m, " ") // select first session
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after enter in picker, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter in picker (launch)")
	}
}

func TestPickerRunningStatus(t *testing.T) {
	running := makeSessions() // api, backend, dotfiles
	m := loadSessions(testModelWithSessions(), running)
	m = pressKey(m, "l")
	// All 3 config sessions match the running sessions.
	for _, sess := range m.cfg.Sessions {
		key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
		if m.pickerRunning[key] != 1 {
			t.Errorf("expected %q to have running count 1 in picker, got %d", key, m.pickerRunning[key])
		}
	}
}

func TestBuildPickerRunning(t *testing.T) {
	running := append(makeSessions(), session.RunningSession{
		TabID: 4, Base: "api", Account: "work", Directory: "/home/user/api",
	})
	got := buildPickerRunning(running)
	if got[session.SessionKey("work", "api")] != 2 {
		t.Errorf("expected work/api running count 2, got %d", got[session.SessionKey("work", "api")])
	}
	if got[session.SessionKey("work", "backend")] != 1 {
		t.Errorf("expected work/backend running count 1, got %d", got[session.SessionKey("work", "backend")])
	}
	if got[session.SessionKey("personal", "dotfiles")] != 1 {
		t.Errorf("expected personal/dotfiles running count 1, got %d", got[session.SessionKey("personal", "dotfiles")])
	}
	if got[session.SessionKey("work", "other")] != 0 {
		t.Errorf("expected work/other running count 0, got %d", got[session.SessionKey("work", "other")])
	}
}

// ---- Footer hints for new modes ----

func TestFooterConfirmHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeConfirm
	footer := renderFooter(m)
	if !strings.Contains(footer, "[y]es") {
		t.Errorf("confirm footer missing [y]es hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "[esc] cancel") {
		t.Errorf("confirm footer missing [esc] cancel hint, got:\n%s", footer)
	}
}

func TestFooterPickerHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "space: toggle") {
		t.Errorf("picker footer missing space hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "s: sessions") {
		t.Errorf("picker footer missing project sessions hint, got:\n%s", footer)
	}
}

func TestFooterListHintNoSessions(t *testing.T) {
	m := loadSessions(testModel(), nil) // empty list
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "l launch") {
		t.Errorf("expected l launch in no-session footer, got:\n%s", footer)
	}
	if !strings.Contains(footer, "R resort") {
		t.Errorf("expected R resort in no-session footer, got:\n%s", footer)
	}
}

func TestFooterIncludesDirectKeys(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	footer := stripAnsi(renderFooter(m))
	for _, frag := range []string{"r restart", "C launch", "x clear", "X close"} {
		if !strings.Contains(footer, frag) {
			t.Errorf("list footer missing direct key %q, got:\n%s", frag, footer)
		}
	}
	lines := strings.Split(footer, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected compact 2-line footer before terminal sizing, got %d lines", len(lines))
	}
	for _, frag := range []string{
		"C launch", "s project sessions", "x clear", "X close",
		"l launch", "S sessions", "P plans", "R resort",
	} {
		if !strings.Contains(lines[0], frag) {
			t.Errorf("expected action %q in continuous footer flow", frag)
		}
	}
	if !strings.Contains(lines[1], "j/k: up/down") {
		t.Error("expected navigation immediately after the action flow")
	}
	if strings.Contains(footer, "a: more actions") {
		t.Error("actions menu hint should not appear in footer")
	}
	for _, stale := range []string{"x close", "C clear"} {
		if strings.Contains(footer, stale) {
			t.Errorf("footer should not contain stale hint %q, got:\n%s", stale, footer)
		}
	}
}

func TestFooterEscHintShownWhenFiltered(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("test")
	footer := renderFooter(m)
	if !strings.Contains(footer, "esc: clear filters") {
		t.Errorf("expected esc hint when filters active, got:\n%s", footer)
	}

	m2 := loadSessions(testModel(), makeSessions())
	footer2 := renderFooter(m2)
	if strings.Contains(footer2, "esc: clear filters") {
		t.Errorf("esc hint should not appear without active filters, got:\n%s", footer2)
	}
}

// ---- Config picker mode tests ----

func TestCKeyOpensConfigPicker(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after c, got %d", m.mode)
	}
	if m.configPickerPurpose != ConfigPickerRestartProfile {
		t.Fatalf("expected restart-profile purpose after c, got %d", m.configPickerPurpose)
	}
	if len(m.configPickerAccounts) != 2 {
		t.Fatalf("expected 2 accounts in picker, got %d", len(m.configPickerAccounts))
	}
}

func TestConfigPickerEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from config picker, got %d", m.mode)
	}
}

func TestConfigPickerNavigation(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c") // → ModeConfigPicker, cursor=0
	if m.configPickerCursor != 0 {
		t.Fatalf("expected configPickerCursor=0, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "j") // cursor=1
	if m.configPickerCursor != 1 {
		t.Fatalf("expected configPickerCursor=1 after j, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "j") // clamp at 1 (only 2 accounts)
	if m.configPickerCursor != 1 {
		t.Fatalf("expected configPickerCursor clamped at 1, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "k") // cursor=0
	if m.configPickerCursor != 0 {
		t.Fatalf("expected configPickerCursor=0 after k, got %d", m.configPickerCursor)
	}
}

func TestConfigPickerSameAccountNoOp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c") // → ModeConfigPicker
	// After state-group sort cursor=0 is backend (bolt), account="work".
	// configPickerCurrent should be "work", cursor at index 0 (work).
	if m.configPickerPurpose != ConfigPickerRestartProfile {
		t.Fatalf("expected restart-profile purpose after c, got %d", m.configPickerPurpose)
	}
	if m.configPickerCurrent != "work" {
		t.Fatalf("expected configPickerCurrent=work, got %q", m.configPickerCurrent)
	}
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after selecting same account, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd when selecting same account (no-op)")
	}
}

func TestConfigPickerCurrentProfileCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "personal", Color: "yellow"},
			{Name: "work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 10, Base: "api", Profile: "Work", Account: "Work"},
	})

	m = pressKey(m, "c")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after c, got %d", m.mode)
	}
	if m.configPickerCursor != 1 {
		t.Fatalf("expected cursor on work profile despite case mismatch, got %d", m.configPickerCursor)
	}
	if out := stripAnsi(renderConfigPicker(m)); !strings.Contains(out, "work") || !strings.Contains(out, "(current)") {
		t.Fatalf("expected work row to be marked current despite case mismatch, got:\n%s", out)
	}

	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after selecting same profile, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd when selected profile only differs by case")
	}
}

func TestConfigPickerDifferentAccountEmitsCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c") // → ModeConfigPicker (current=work, cursor=0)
	m = pressKey(m, "j") // cursor=1 (personal)
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after selecting different account, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd when selecting different account")
	}
}

func TestFooterConfigPickerHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeConfigPicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "enter: select") {
		t.Errorf("config picker footer missing select hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "esc/q: cancel") {
		t.Errorf("config picker footer missing cancel hint, got:\n%s", footer)
	}
}
