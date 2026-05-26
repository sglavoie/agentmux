package tui

import (
	"strings"
	"testing"

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
