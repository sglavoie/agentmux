package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// pickerModel returns a model preloaded with a picker open and seeded with a
// few config sessions across two accounts. Each test starts from this state
// so the picker is already focused.
func pickerModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
			{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
			{Title: "notes", Directory: "/home/user/notes", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	return m
}

func TestPickerTypingNarrowsLive(t *testing.T) {
	m := pickerModel(t)
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected 4 picker sessions initially, got %d", len(m.pickerFilteredIdx))
	}
	// Use "tes" (substring of "notes" only) — j/k/o/p are picker shortcuts.
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	m = pressKey(m, "s")
	if m.pickerFilter != "tes" {
		t.Fatalf("expected pickerFilter=tes, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 match for 'tes', got %d", len(m.pickerFilteredIdx))
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker while typing, got %d", m.mode)
	}
}

func TestPickerBackspaceShrinksBuffer(t *testing.T) {
	m := pickerModel(t)
	for _, k := range []string{"t", "e", "s"} {
		m = pressKey(m, k)
	}
	m = pressKey(m, "backspace")
	if m.pickerFilter != "te" {
		t.Fatalf("expected pickerFilter=te, got %q", m.pickerFilter)
	}
	// "te" still matches "notes" only.
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 match for 'te', got %d", len(m.pickerFilteredIdx))
	}
	// Backspace on empty buffer is a no-op (does not exit).
	for i := 0; i < 5; i++ {
		m = pressKey(m, "backspace")
	}
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after over-backspace, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("backspace on empty buffer must not exit picker, mode=%d", m.mode)
	}
}

func TestPickerEscClearsBufferThenExits(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	if m.pickerFilter == "" {
		t.Fatal("expected buffer populated before esc")
	}
	// First esc clears the buffer but stays in picker.
	m = pressKey(m, "esc")
	if m.pickerFilter != "" {
		t.Fatalf("expected buffer cleared after first esc, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after first esc, got %d", m.mode)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions visible after clear, got %d", len(m.pickerFilteredIdx))
	}
	// Second esc exits to list.
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after second esc, got %d", m.mode)
	}
}

func TestPickerSelectionPersistsAcrossFilterChanges(t *testing.T) {
	m := pickerModel(t)
	// Default sort: personal, then work — so cursor=0 is the first personal
	// session ("dotfiles"). Just toggle whatever is at cursor=0.
	first := m.pickerFilteredIdx[m.pickerCursor]
	m = pressKey(m, " ")
	if !m.pickerSelected[first] {
		t.Fatal("expected first row selected after space")
	}
	// Type a filter that hides the selected row. Buffer ends as "ac"
	// (the trailing 'k' is a picker shortcut and doesn't get appended).
	for _, k := range []string{"a", "c"} {
		m = pressKey(m, k)
	}
	for _, idx := range m.pickerFilteredIdx {
		if idx == first {
			t.Fatal("expected the toggled row to be filtered out by 'ac'")
		}
	}
	// Selection is keyed by original index, so it must persist.
	if !m.pickerSelected[first] {
		t.Fatal("selection must persist even when the row is filtered out")
	}
	// Clear the filter — the row reappears, still selected.
	m = pressKey(m, "esc")
	if !m.pickerSelected[first] {
		t.Fatal("selection lost after clearing filter")
	}
}

func TestPickerEnterLaunchesWhileFiltered(t *testing.T) {
	m := pickerModel(t)
	for _, k := range []string{"t", "e", "s"} {
		m = pressKey(m, k)
	}
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 visible row, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, " ") // select the visible row
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil launch cmd after enter in picker")
	}
}

func TestPickerDSLAccountFilter(t *testing.T) {
	// The picker reserves j/k/o/space/enter/esc/backspace as shortcuts, so
	// typing "@personal" through pressKey would lose `o`. Drive the DSL
	// filter via applyPickerFilter directly to verify the underlying
	// semantics — the live-typing path is covered by TestPickerTypingNarrowsLive.
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
		{Title: "notes", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "@personal")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("expected [1,2] for @personal, got %v", got)
	}
}

func TestPickerStickyInputRendered(t *testing.T) {
	m := pickerModel(t)
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/█") {
		t.Errorf("expected sticky `/` prompt with cursor in render, got:\n%s", out)
	}
	for _, k := range []string{"t", "e"} {
		m = pressKey(m, k)
	}
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/te█") {
		t.Errorf("expected buffer 'te' rendered next to the prompt, got:\n%s", out)
	}
}

func TestPickerSlashIsAppendedToBuffer(t *testing.T) {
	// `/` is a printable char with no special meaning in the picker now —
	// it should land in the buffer like any other character.
	m := pickerModel(t)
	m = pressKey(m, "/")
	if m.pickerFilter != "/" {
		t.Fatalf("expected pickerFilter=/, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker, got mode=%d", m.mode)
	}
}

func TestPickerFooterShowsFilterHint(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	footer := renderFooter(m)
	if !strings.Contains(footer, "type to filter") {
		t.Errorf("expected picker footer to advertise live filter, got:\n%s", footer)
	}
	if !strings.Contains(footer, "esc: clear/exit") {
		t.Errorf("expected picker footer to advertise esc behavior, got:\n%s", footer)
	}
}

func TestApplyPickerFilterDSLSubstring(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Directory: "/home/user/api", Account: "work"},
		{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "back")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for 'back', got %v", got)
	}
}

func TestApplyPickerFilterDSLAccount(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "@personal")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for '@personal', got %v", got)
	}
}

func TestApplyPickerFilterEmptyReturnsAll(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "")
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("expected [0,1] for empty query, got %v", got)
	}
}

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
	if m.previewEnabled {
		t.Fatal("preview should start disabled")
	}
	m = pressKey(m, "p")
	if !m.previewEnabled {
		t.Fatal("expected preview enabled after p in picker")
	}
	// `p` should not have landed in the filter buffer.
	if strings.Contains(m.pickerFilter, "p") {
		t.Errorf("expected p to act as toggle, not buffer append; pickerFilter=%q", m.pickerFilter)
	}
	m = pressKey(m, "p")
	if m.previewEnabled {
		t.Fatal("expected preview disabled after second p in picker")
	}
}

func TestPickerPreviewRenderShowsConfigBlock(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.previewEnabled = true
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
	m.previewEnabled = true
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
	m.previewEnabled = true
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "dir:") {
		t.Errorf("expected preview pane to auto-disable at narrow width, got:\n%s", out)
	}
}

func TestPickerPreviewRunningPeek(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		Accounts: []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 99, Base: "W:api", Account: "work"},
	})
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	m.previewEnabled = true
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

func TestPickerPreviewMovementRefetchesWindow(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		Accounts: []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "green"}},
		Sessions: []config.Session{
			{Title: "api", Account: "work"},
			{Title: "notes", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "W:api", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "P:notes", Account: "personal"},
	})
	m.width, m.height = 120, 40
	m = pressKey(m, "l")
	// Sorted: personal first ("notes") then work ("api"). Cursor=0 → notes.
	if !m.previewEnabled {
		// Enable preview from within the picker.
		m = pressKey(m, "p")
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
