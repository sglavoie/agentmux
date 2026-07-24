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
	m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
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
	m.width = 80

	// Initial state: nav mode, empty filter → header hint, no cursor.
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "/█") {
		t.Errorf("expected no cursor block in initial render (not yet in search mode), got:\n%s", out)
	}
	firstLine := strings.Split(out, "\n")[0]
	if !strings.HasSuffix(firstLine, "press / to filter") {
		t.Errorf("expected header hint 'press / to filter' in initial render, got header=%q\n%s", firstLine, out)
	}
	if len(firstLine) != m.width {
		t.Errorf("expected header hint to be right-aligned to width %d, got width %d header=%q", m.width, len(firstLine), firstLine)
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

func TestPickerFooterShowsFilterControls(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	footer := stripAnsi(renderFooter(m))
	if strings.Contains(footer, "press / to filter") {
		t.Errorf("picker footer should not duplicate the header filter hint, got:\n%s", footer)
	}
	if strings.Contains(footer, "/: filter") {
		t.Errorf("picker footer should not duplicate the header filter key, got:\n%s", footer)
	}
	if !strings.Contains(footer, "DSL: @profile, word") {
		t.Errorf("expected picker footer to retain DSL hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "f: profile") {
		t.Errorf("expected picker footer to advertise profile cycling, got:\n%s", footer)
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

// TestPickerRowEmojiTitleColumnAlignment locks in that picker rows whose
// titles begin with the live dashboard's project-icon set (🐱amX, 🧠BrainNotes,
// 🗣commscoach, 🧬curator, 🧰dev-helpers, •dotfiles) keep the harness column
// at the same visual offset across rows, and that pictographic leading
// graphemes occupy the fixed 2-cell title-icon column. Mirrors the
// dashboard-list regression in list_test.go so the picker shares the same
// visual contract.
func TestPickerRowEmojiTitleColumnAlignment(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "🐱amX", Directory: "/work/amX", Account: "work"},
			{Title: "🧠BrainNotes", Directory: "/work/brain", Account: "work"},
			{Title: "🗣commscoach", Directory: "/work/comms", Account: "work"},
			{Title: "🧬curator", Directory: "/work/curator", Account: "work"},
			{Title: "🧰dev-helpers", Directory: "/work/dev", Account: "work"},
			{Title: "•dotfiles", Directory: "/work/dotfiles", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	// Move cursor off any row so styleSelected does not mask measurements.
	m.pickerCursor = -1

	const harnessCellText = "🦊 work/claude"
	out := stripAnsi(renderPickerList(m, 0))
	lines := strings.Split(out, "\n")

	// Identify rows by the title body (the substring after any leading
	// pictograph/marker) so the check is independent of how each title icon
	// is internally measured.
	titlesByBody := map[string]string{
		"amX":         "🐱amX",
		"BrainNotes":  "🧠BrainNotes",
		"commscoach":  "🗣commscoach",
		"curator":     "🧬curator",
		"dev-helpers": "🧰dev-helpers",
		"dotfiles":    "•dotfiles",
	}
	rowsByTitle := map[string]string{}
	for body, title := range titlesByBody {
		for _, line := range lines {
			if strings.Contains(line, body) {
				rowsByTitle[title] = line
				break
			}
		}
		if _, ok := rowsByTitle[title]; !ok {
			t.Fatalf("no rendered row found for title %q (body %q) in:\n%s", title, body, out)
		}
	}

	// Harness column must land at the same offset across all rows so the
	// checkbox + title + title-pad block widths line up.
	var harnessOffset int
	first := true
	for _, title := range []string{"🐱amX", "🧠BrainNotes", "🗣commscoach", "🧬curator", "🧰dev-helpers", "•dotfiles"} {
		row := rowsByTitle[title]
		idx := strings.Index(row, harnessCellText)
		if idx < 0 {
			t.Fatalf("harness cell %q not found in row for title %q: %q", harnessCellText, title, row)
		}
		offset := displayWidth(row[:idx])
		if first {
			harnessOffset = offset
			first = false
			continue
		}
		if offset != harnessOffset {
			t.Errorf("harness column misaligned for %q: got cell %d, want %d (row=%q)", title, offset, harnessOffset, row)
		}
	}

	// Emoji-prefixed title bodies (the substring after the leading
	// pictograph) must start at the same body offset across rows.
	emojiBodies := map[string]string{
		"🐱amX":         "amX",
		"🧠BrainNotes":  "BrainNotes",
		"🗣commscoach":  "commscoach",
		"🧬curator":     "curator",
		"🧰dev-helpers": "dev-helpers",
	}
	var bodyOffset int
	first = true
	for title, body := range emojiBodies {
		row := rowsByTitle[title]
		idx := strings.Index(row, body)
		if idx < 0 {
			t.Fatalf("title body %q not found in picker row for title %q: %q", body, title, row)
		}
		offset := displayWidth(row[:idx])
		if first {
			bodyOffset = offset
			first = false
			continue
		}
		if offset != bodyOffset {
			t.Errorf("picker emoji-prefixed body offset drift for %q: got %d, want %d (row=%q)", title, offset, bodyOffset, row)
		}
	}

	// The bullet '•' is intentionally not promoted to the 2-cell title-icon
	// column, so its body sits one cell earlier than the emoji bodies above.
	bulletRow := rowsByTitle["•dotfiles"]
	bulletBody := strings.Index(bulletRow, "dotfiles")
	if bulletBody < 0 {
		t.Fatalf("bullet-prefixed body not found in picker row: %q", bulletRow)
	}
	if got := displayWidth(bulletRow[:bulletBody]); got != bodyOffset-1 {
		t.Errorf("picker bullet-prefixed body offset = %d, want %d (one cell before emoji bodies; row=%q)", got, bodyOffset-1, bulletRow)
	}
}
