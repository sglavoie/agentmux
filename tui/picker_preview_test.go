package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// pickerPreviewCfg returns a Preview config with thresholds friendly for tests.
func pickerPreviewCfg() config.PreviewConfig {
	return config.PreviewConfig{
		Direction: "horizontal",
		Ratio:     0.4,
		RefreshMs: 1000,
		MinCols:   40,
		MinRows:   10,
	}
}

func TestPickerPreviewToggleP(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	if m.pickerPreviewEnabled {
		t.Fatal("picker preview should start disabled")
	}
	dashboardBefore := m.inspectorDisplay
	m = pressKey(m, "p")
	if !m.pickerPreviewEnabled {
		t.Fatal("expected pickerPreviewEnabled=true after p in picker")
	}
	if m.inspectorDisplay != dashboardBefore {
		t.Fatalf("p in the picker moved the dashboard inspector to %v", m.inspectorDisplay)
	}
	// `p` should not have landed in the filter buffer.
	if strings.Contains(m.pickerFilter, "p") {
		t.Errorf("expected p to act as toggle, not buffer append; pickerFilter=%q", m.pickerFilter)
	}
	m = pressKey(m, "p")
	if m.pickerPreviewEnabled {
		t.Fatal("expected pickerPreviewEnabled=false after second p in picker")
	}
	if m.inspectorDisplay != dashboardBefore {
		t.Fatalf("a second p in the picker moved the dashboard inspector to %v", m.inspectorDisplay)
	}
}

func TestPickerPreviewRenderShowsConfigBlock(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	// pickerCursor=0 → first sorted row: "dotfiles" (personal).
	if !strings.Contains(out, "dotfiles") {
		t.Errorf("expected highlighted config title in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "dir:") {
		t.Errorf("expected dir label in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "profile:") {
		t.Errorf("expected profile label in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "personal") {
		t.Errorf("expected profile value in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "harness:") {
		t.Errorf("expected harness label in preview pane, got:\n%s", out)
	}
}

func TestPickerPreviewEmojiTitleReservesIconColumn(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		Accounts: []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "🗣commscoach", Directory: "/home/user/commscoach", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")

	header := strings.SplitN(stripAnsi(renderPickerPreview(m, 0, 40, 6)), "\n", 2)[0]
	if !strings.Contains(header, "🗣commscoach") {
		t.Fatalf("picker preview header should keep title icon tight, got %q", header)
	}
}

func TestPickerPreviewStyledFieldTruncationPreservesAnsi(t *testing.T) {
	m := pickerModel(t)
	raw := renderPickerPreview(m, 0, 4, 6)
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected picker preview field line, got %q", raw)
	}
	assertStyledTruncatedLine(t, "picker preview field", lines[1], 4)
}

func TestPickerPreviewRootKeepsFooterAboveStatusBar(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 100, 14
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true

	out := stripAnsi(m.render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(last, "Launch Sessions") {
		t.Fatalf("final row = %q, want picker status", last)
	}
	footerLines := rootFooterLines(m)
	footerStart := len(lines) - statusBarLines - len(footerLines)
	if footerStart < 0 {
		t.Fatalf("footer start underflow: lines=%d footer=%d", len(lines), len(footerLines))
	}
	footer := strings.Join(lines[footerStart:len(lines)-statusBarLines], "\n")
	for _, want := range []string{"space toggle", "DSL:"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("picker footer above status missing %q:\n%s", want, footer)
		}
	}
	if !strings.Contains(out, "dir:") {
		t.Fatalf("expected picker preview pane to remain visible:\n%s", out)
	}
}

func TestPickerProfileOverrideUpdatesLaunchProfile(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C in picker, got %d", m.mode)
	}
	if m.configPickerReturnMode != ModePicker {
		t.Fatalf("expected config picker to return to ModePicker, got %d", m.configPickerReturnMode)
	}

	// Picker starts on "dotfiles" with default profile "personal". Move to work.
	m = pressKey(m, "k")
	m = pressKey(m, "enter")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after choosing override profile, got %d", m.mode)
	}
	if got := m.pickerEffectiveProfile(0); got != "work" {
		t.Fatalf("expected picker override to switch launch profile to work, got %q", got)
	}

	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "(override)") {
		t.Errorf("expected picker row to show override marker, got:\n%s", out)
	}
	if !strings.Contains(out, "launch profile: work") {
		t.Errorf("expected preview to show launch profile after override, got:\n%s", out)
	}
}

func TestPickerPreviewRenderAutoDisableNarrow(t *testing.T) {
	m := pickerModel(t)
	cfg := pickerPreviewCfg()
	m.cfg.Defaults.Preview = cfg
	m.width, m.height = 30, 40 // below MinCols (40)
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "dir:") {
		t.Errorf("expected preview pane to auto-disable at narrow width, got:\n%s", out)
	}
}

func TestPickerPreviewRunningPeek(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 99, Base: "api", Account: "work"},
	})
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	m.pickerPreviewEnabled = true
	m.previewWindowID = 99
	m.previewText = "alpha\nbeta\ngamma"
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "── running ──") {
		t.Errorf("expected running divider when picker highlight matches a running session, got:\n%s", out)
	}
	if !strings.Contains(out, "gamma") {
		t.Errorf("expected scrollback tail in preview pane, got:\n%s", out)
	}
}

func TestPickerPreviewUsesNewestDuplicateInSessionOrder(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work"},
		{TabID: 2, WindowID: 33, Base: "api", Account: "work"},
	})
	m = pressKey(m, "l")

	if got := m.pickerRunning[session.SessionKey("work", "api")]; got != 2 {
		t.Fatalf("expected picker running count 2, got %d", got)
	}
	if got := m.pickerCurrentWindowID(); got != 33 {
		t.Fatalf("expected picker preview to choose newest duplicate window 33, got %d", got)
	}

	m.width, m.height = 120, 40
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "(2 running)") {
		t.Errorf("expected picker row to show duplicate running count, got:\n%s", out)
	}
}

func TestPickerPreviewMovementRefetchesWindow(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "yellow"}},
		Sessions: []config.Session{
			{Title: "api", Account: "work"},
			{Title: "notes", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "notes", Account: "personal"},
	})
	m.width, m.height = 120, 40
	m = pressKey(m, "l")
	// Sorted: personal first ("notes") then work ("api"). Cursor=0 → notes.
	if !m.pickerPreviewEnabled {
		// Enable preview from within the picker, and answer the read that
		// opening it dispatches: the controller keeps one read in flight, so an
		// unanswered reply would make the move below coalesce into it rather
		// than fetch.
		updated, cmd := m.Update(keyPress("p"))
		m = updated.(Model)
		settlePreviewRead(t, &m, cmd)
	}
	if m.previewWindowID != 22 {
		t.Errorf("expected previewWindowID=22 (notes) on picker entry, got %d", m.previewWindowID)
	}
	// Move cursor → highlight api → preview should track its window.
	updated, cmd := m.Update(keyPress("j"))
	m = updated.(Model)
	if m.previewWindowID != 11 {
		t.Errorf("expected previewWindowID=11 (api) after j, got %d", m.previewWindowID)
	}
	if cmd == nil {
		t.Error("expected non-nil fetch cmd after picker cursor moved to a different window")
	}
}

func TestPickerExitRestartsListPreviewTicker(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work"},
	})
	m.width, m.height = 120, 40
	m.previewWindowID = 11
	m.mode = ModePicker
	m.pickerPreviewEnabled = false
	m.pickerFilteredIdx = []int{0}

	// Stamped with the live chain, so the tick is refused for what the picker
	// owns rather than for belonging to no chain: an unstamped tick is dropped
	// on identity alone and would assert nothing about the picker.
	m.previewPollSeq = 1
	updated, deadTick := m.Update(previewTickMsg{pollSeq: 1})
	m = updated.(Model)
	if deadTick != nil {
		t.Fatal("setup expected picker without picker preview to consume the list preview tick")
	}

	updated, cmd := m.Update(keyPress("esc"))
	m = updated.(Model)
	if m.mode != ModeList {
		t.Fatalf("expected esc to return to ModeList, got %d", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected exiting picker to reschedule the list preview ticker")
	}
}
