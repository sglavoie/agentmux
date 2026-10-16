package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/layout"
)

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
	if layout.Width(firstLine) != m.width {
		t.Errorf("expected header hint to be right-aligned to width %d, got width %d header=%q", m.width, layout.Width(firstLine), firstLine)
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
	if strings.Contains(footer, "/ filter") {
		t.Errorf("picker footer should not duplicate the header filter key, got:\n%s", footer)
	}
	if !strings.Contains(footer, "DSL: @profile, word") {
		t.Errorf("expected picker footer to retain DSL hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "f profile") {
		t.Errorf("expected picker footer to advertise profile cycling, got:\n%s", footer)
	}
	if !strings.Contains(footer, "esc clear/exit") {
		t.Errorf("expected picker footer to advertise esc behavior, got:\n%s", footer)
	}
	if !strings.Contains(footer, "g/G top/bottom") {
		t.Errorf("expected picker footer to advertise boundary navigation, got:\n%s", footer)
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
		offset := layout.Width(row[:idx])
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
		offset := layout.Width(row[:idx])
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
	if got := layout.Width(bulletRow[:bulletBody]); got != bodyOffset-1 {
		t.Errorf("picker bullet-prefixed body offset = %d, want %d (one cell before emoji bodies; row=%q)", got, bodyOffset-1, bulletRow)
	}
}
