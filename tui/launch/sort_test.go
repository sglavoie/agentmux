package launch

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestSortPickerSessions_EmojiAndCaseInsensitive pins the picker sort
// contract: within each profile group, projects must order by their first
// alphabetic rune (emoji/punctuation/symbol prefixes ignored), with the
// remainder of the title compared case-insensitively. Profile groups stay
// in their own (also normalized) order.
func TestSortPickerSessions_EmojiAndCaseInsensitive(t *testing.T) {
	in := []config.Session{
		// tradition/codex group (intentionally interleaved with personal/codex).
		{Title: "Docs", Account: "tradition"},
		{Title: "AiBroker", Account: "tradition"},
		{Title: "diffusion", Account: "tradition"},
		{Title: "🐍embedding_python", Account: "tradition"},

		// personal/codex group.
		{Title: "spec", Account: "personal"},
		{Title: "•dotfiles", Account: "personal"},
		{Title: "🌐sglavoie.com", Account: "personal"},
		{Title: "🎵muse", Account: "personal"},
		{Title: "🐱amx", Account: "personal"},
		{Title: "📃scripts", Account: "personal"},
		{Title: "📝tt", Account: "personal"},
		{Title: "🤖sloppy-paste", Account: "personal"},
		{Title: "🦄next_unicorn", Account: "personal"},
		{Title: "🧔‍♂️Seneca", Account: "personal"},
		{Title: "🧠BrainNotes", Account: "personal"},
		{Title: "🧬curator", Account: "personal"},
		{Title: "🧬trail", Account: "personal"},
		{Title: "🧰dev-helpers", Account: "personal"},
	}

	got := SortSessions(in)

	// Expected: personal/codex group first (p < t), then tradition/codex group.
	// Within each group: by first alphabetic rune, case-insensitive.
	wantTitles := []string{
		// personal/codex
		"🐱amx",
		"🧠BrainNotes",
		"🧬curator",
		"🧰dev-helpers",
		"•dotfiles",
		"🎵muse",
		"🦄next_unicorn",
		"📃scripts",
		"🧔‍♂️Seneca",
		"🌐sglavoie.com",
		"🤖sloppy-paste",
		"spec",
		"🧬trail",
		"📝tt",
		// tradition/codex
		"AiBroker",
		"diffusion",
		"Docs",
		"🐍embedding_python",
	}

	if len(got) != len(wantTitles) {
		t.Fatalf("sortPickerSessions: got %d sessions, want %d", len(got), len(wantTitles))
	}
	for i, want := range wantTitles {
		if got[i].Title != want {
			t.Errorf("sortPickerSessions[%d]: got %q, want %q", i, got[i].Title, want)
		}
	}
}
