package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
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
	// Press / to enter search mode, then type "tes" (substring of "notes" only).
	m = pressKey(m, "/")
	if !m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=true after /")
	}
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
	m = pressKey(m, "/") // enter search mode
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
	// Over-backspace empties the buffer but stays in search mode.
	for i := 0; i < 5; i++ {
		m = pressKey(m, "backspace")
	}
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after over-backspace, got %q", m.pickerFilter)
	}
	// Exit search mode, then verify backspace in nav mode on empty buffer is a no-op.
	m = pressKey(m, "esc") // search → nav (filter already "")
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false after esc in search mode")
	}
	m = pressKey(m, "backspace") // nav mode, empty buffer — no-op
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after backspace on empty in nav mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("backspace on empty buffer must not exit picker, mode=%d", m.mode)
	}
}

func TestPickerEscClearsBufferThenExits(t *testing.T) {
	m := pickerModel(t)

	// Enter search mode and type a filter.
	m = pressKey(m, "/")
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	if m.pickerFilter == "" {
		t.Fatal("expected buffer populated before esc")
	}

	// Esc in search mode → exits search mode, filter KEPT, stays in picker.
	m = pressKey(m, "esc")
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false after esc in search mode")
	}
	if m.pickerFilter != "te" {
		t.Fatalf("expected filter 'te' kept after esc from search mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after esc from search mode, got %d", m.mode)
	}

	// Esc in nav mode with non-empty filter → clears filter, stays in picker.
	m = pressKey(m, "esc")
	if m.pickerFilter != "" {
		t.Fatalf("expected buffer cleared after esc in nav mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after filter-clear esc, got %d", m.mode)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions visible after clear, got %d", len(m.pickerFilteredIdx))
	}

	// Esc in nav mode with empty filter → exit to ModeList.
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc with empty filter, got %d", m.mode)
	}
}

func TestPickerSelectionPersistsAcrossFilterChanges(t *testing.T) {
	m := pickerModel(t)
	// Default sort: personal, then work — cursor=0 is "dotfiles".
	first := m.pickerFilteredIdx[m.pickerCursor]
	m = pressKey(m, " ")
	if !m.pickerSelected[first] {
		t.Fatal("expected first row selected after space")
	}
	// Enter search mode and type a filter that hides the selected row.
	m = pressKey(m, "/")
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
	// Exit search mode (filter kept), then clear filter.
	m = pressKey(m, "esc") // search → nav, filter "ac" kept
	m = pressKey(m, "esc") // nav + "ac" filter → clear filter
	if !m.pickerSelected[first] {
		t.Fatal("selection lost after clearing filter")
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions after filter cleared, got %d", len(m.pickerFilteredIdx))
	}
}

func TestPickerEnterLaunchesWhileFiltered(t *testing.T) {
	m := pickerModel(t)
	// Enter search mode, filter to one row, then exit search mode.
	m = pressKey(m, "/")
	for _, k := range []string{"t", "e", "s"} {
		m = pressKey(m, k)
	}
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 visible row, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // exit search mode → nav (filter kept)
	m = pressKey(m, " ")   // select the visible row
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil launch cmd after enter in picker")
	}
}

func TestPickerDSLAccountFilter(t *testing.T) {
	// Drive the DSL filter via applyPickerFilter directly to verify the underlying
	// semantics — the live-typing path (in search mode) is covered by TestPickerTypingNarrowsLive.
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

	// Initial state: nav mode, empty filter → hint text, no cursor.
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "/█") {
		t.Errorf("expected no cursor block in initial render (not yet in search mode), got:\n%s", out)
	}
	if !strings.Contains(out, "press / to filter") {
		t.Errorf("expected hint 'press / to filter' in initial render, got:\n%s", out)
	}

	// After pressing `/`: search mode active → prompt with cursor.
	m = pressKey(m, "/")
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/█") {
		t.Errorf("expected '/█' prompt after entering search mode, got:\n%s", out)
	}

	// After typing "te": buffer shown with cursor.
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/te█") {
		t.Errorf("expected buffer 'te' rendered next to the prompt, got:\n%s", out)
	}
}

func TestPickerSlashEntersSearchMode(t *testing.T) {
	m := pickerModel(t)
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false initially")
	}
	m = pressKey(m, "/")
	if !m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=true after /")
	}
	// The slash itself must NOT be added to the filter buffer.
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after /, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after /, got mode=%d", m.mode)
	}
}

func TestPickerNavModeIgnoresPrintableChars(t *testing.T) {
	m := pickerModel(t)
	// In nav mode, printable chars other than known shortcuts are silently ignored.
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	m = pressKey(m, "s")
	if m.pickerFilter != "" {
		t.Fatalf("expected empty filter in nav mode after typing, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions unfiltered in nav mode, got %d", len(m.pickerFilteredIdx))
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
