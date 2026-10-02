package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestRenderDeleteChoiceBox is a smoke test for the bordered wrapper: the
// rounded top-left glyph appears and the prompt text survives the wrap.
func TestRenderDeleteChoiceBox(t *testing.T) {
	target := transcript.SessionMeta{
		ID:      "abc123",
		Harness: "claude",
		Profile: "work",
		Project: "demo",
		Path:    "/tmp/demo.jsonl",
	}
	m := Model{
		mode:         ModeDeleteChoice,
		width:        100,
		height:       40,
		deleteTarget: &target,
	}
	raw := renderDeleteChoiceBox(m)
	if !strings.Contains(raw, "╭") {
		t.Errorf("expected rounded top-left glyph in box output; got %q", raw)
	}
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Delete") {
		t.Errorf("expected prompt header in box output; got %q", plain)
	}
	if !strings.Contains(plain, "[f] also delete file") {
		t.Errorf("expected destructive option text in box output; got %q", plain)
	}
}

func TestRenderDeleteChoiceCompactsSharedHomeProfiles(t *testing.T) {
	target := transcript.SessionMeta{
		ID:       "abc123",
		Harness:  "claude",
		Profile:  "work",
		Profiles: []string{"work", "personal", "ops"},
		Project:  "demo",
		Path:     "/tmp/demo.jsonl",
	}
	out := stripAnsi(renderDeleteChoice(Model{deleteTarget: &target}))
	if !strings.Contains(out, "claude · work+2 · /tmp/demo.jsonl") {
		t.Fatalf("delete prompt did not render compact shared-home profile label:\n%s", out)
	}
}

// TestDeleteChoiceQCancels pins q as a way back out of the delete prompt:
// the prompt asks about destroying a transcript, and a key that means "back"
// everywhere else must not close amx from here.
func TestDeleteChoiceQCancels(t *testing.T) {
	target := transcript.SessionMeta{Harness: "claude", Path: "/tmp/demo.jsonl"}
	m := Model{mode: ModeDeleteChoice, deleteTarget: &target}

	updated, cmd := m.Update(keyPress("q"))
	if cmd != nil {
		if msg := cmd(); msg == (tea.QuitMsg{}) {
			t.Fatal("q in delete choice quit the program; want cancel")
		}
	}
	got := updated.(Model)
	if got.mode != ModeSessionSearch || got.deleteTarget != nil {
		t.Fatalf("q in delete choice: mode=%d target=%v; want ModeSessionSearch with no target", got.mode, got.deleteTarget)
	}
}

// TestDeleteChoiceCtrlCQuits keeps ctrl+c as the one quit key in the prompt.
func TestDeleteChoiceCtrlCQuits(t *testing.T) {
	m := Model{mode: ModeDeleteChoice}

	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c in delete choice returned nil cmd; want tea.Quit")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("ctrl+c in delete choice produced %T; want tea.QuitMsg", msg)
	}
}
