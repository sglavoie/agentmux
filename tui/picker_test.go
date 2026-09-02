package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/launch"
)

// pickerModel returns a model preloaded with a picker open and seeded with a
// few config sessions across two accounts. Each test starts from this state
// so the picker is already focused.
func pickerModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
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

func TestPickerProjectSessionSearchUsesFilteredRowTitle(t *testing.T) {
	m := sessionSearchFilterModel(t)
	m.mode = ModePicker
	m.cfg.Sessions = []config.Session{
		{Title: "Foo", Directory: "/home/user/foo", Account: "work"},
		{Title: "Bar", Directory: "/home/user/bar", Account: "work"},
	}
	m.pickerSessions = m.cfg.Sessions
	m.pickerFilter = "Bar"
	m.pickerFilteredIdx = launch.ApplyFilter(m.pickerSessions, m.pickerFilter)
	m.pickerCursor = 0
	seq := m.sessionSearchSeq

	updated, cmd := m.Update(keyPress("s"))
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("picker project session search returned nil cmd; want debounced search")
	}
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if got.sessionSearchSeq != seq+1 {
		t.Fatalf("sessionSearchSeq = %d, want %d", got.sessionSearchSeq, seq+1)
	}
	if singleChipValue(got.filterProject) != "Bar" {
		t.Fatalf("filterProject = %#v, want Bar", got.filterProject)
	}
	if out := stripAnsi(renderSessionSearch(got)); !strings.Contains(out, "project: Bar") {
		t.Fatalf("project chip was not visible after picker project search entry:\n%s", out)
	}

	updated, searchCmd := got.Update(sessionSearchDebounceMsg{seq: got.sessionSearchSeq})
	got = updated.(Model)
	doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("project debounce produced %T, want sessionSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("project-filtered search returned error: %v", doneMsg.err)
	}
	updated, _ = got.Update(doneMsg)
	got = updated.(Model)
	if len(got.sessionResults) != 1 || got.sessionResults[0].Meta.Project != "Bar" {
		t.Fatalf("project-filtered results = %+v, want only Bar", got.sessionResults)
	}
}

func TestPickerProjectSessionSearchSIsTextInSearchMode(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")

	updated, cmd := m.Update(keyPress("s"))
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("typing s in picker search mode returned cmd %T, want nil", cmd)
	}
	if got.mode != ModePicker {
		t.Fatalf("mode = %v, want ModePicker", got.mode)
	}
	if !got.pickerSearchMode {
		t.Fatal("pickerSearchMode=false after typing s")
	}
	if got.pickerFilter != "s" {
		t.Fatalf("pickerFilter = %q, want s", got.pickerFilter)
	}
}

func TestPickerProjectSessionSearchEmptyFilterIsNoOp(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	m = typeString(m, "zzzz")
	m = pressKey(m, "esc")
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}

	updated, cmd := m.Update(keyPress("s"))
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("project session search on empty picker returned cmd %T, want nil", cmd)
	}
	if got.mode != ModePicker {
		t.Fatalf("mode = %v, want ModePicker", got.mode)
	}
	if got.pickerFilter != "zzzz" {
		t.Fatalf("pickerFilter = %q, want unchanged zzzz", got.pickerFilter)
	}
}
