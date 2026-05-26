package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestRenderDeleteChoiceOverlayComposition exercises the full m.render() path
// for ModeDeleteChoice. Mirrors the chip picker overlay test: the prompt is
// composited on top of renderSessionSearch (the base header bleeds through
// the dim) and the total line count stays within the terminal height.
func TestRenderDeleteChoiceOverlayComposition(t *testing.T) {
	now := time.Now()
	target := transcript.SessionMeta{
		ID:      "abc",
		Harness: "claude",
		Profile: "work",
		Project: "agentmux",
		Path:    "/tmp/demo.jsonl",
	}
	m := Model{
		mode:         ModeDeleteChoice,
		width:        100,
		height:       40,
		deleteTarget: &target,
		cfg:          &config.Config{},
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{
				ID:        "abc",
				Harness:   "claude",
				Profile:   "work",
				Project:   "agentmux",
				StartedAt: now.Add(-time.Hour),
				LastAt:    now,
			}},
		},
	}

	out := m.render()
	plain := stripAnsi(out)

	if !strings.Contains(plain, "Delete") {
		t.Errorf("expected delete prompt header in overlay output; got %q", plain)
	}
	if !strings.Contains(plain, "Past Sessions") {
		t.Errorf("expected base session search header to bleed through dim; got %q", plain)
	}
	if !strings.Contains(out, "╭") {
		t.Errorf("expected rounded box top-left glyph in overlay output; got %q", out)
	}
	if !strings.Contains(plain, "[f] also delete file") {
		t.Errorf("expected destructive option text in overlay output; got %q", plain)
	}

	lines := strings.Split(out, "\n")
	if len(lines) > m.height+1 {
		t.Errorf("overlay output exceeds terminal height: got %d lines, want <= %d",
			len(lines), m.height+1)
	}
}

// TestRenderChipPickerOverlayNarrowTerminal confirms the overlay path clamps
// without panicking when the terminal is far narrower than the box's preferred
// width. The chip picker header still survives the truncation.
func TestRenderChipPickerOverlayNarrowTerminal(t *testing.T) {
	m := Model{
		mode:              ModeChipPicker,
		width:             20,
		height:            10,
		chipPickerKind:    chipPickerHarness,
		chipPickerOptions: []string{"", "claude", "codex"},
		cfg:               &config.Config{},
	}

	out := m.render()
	if out == "" {
		t.Fatalf("expected non-empty output on narrow terminal; got empty string")
	}
	plain := stripAnsi(out)
	if !strings.Contains(plain, "Choose") {
		t.Errorf("expected chip picker header to survive narrow-terminal clamp; got %q", plain)
	}
}

// TestRenderDeleteChoiceOverlayNarrowTerminal confirms the delete-choice
// overlay also clamps without panicking on a very narrow terminal.
func TestRenderDeleteChoiceOverlayNarrowTerminal(t *testing.T) {
	target := transcript.SessionMeta{
		ID:      "abc",
		Harness: "claude",
		Profile: "work",
		Project: "agentmux",
		Path:    "/tmp/demo.jsonl",
	}
	m := Model{
		mode:         ModeDeleteChoice,
		width:        20,
		height:       10,
		deleteTarget: &target,
		cfg:          &config.Config{},
	}

	out := m.render()
	if out == "" {
		t.Fatalf("expected non-empty output on narrow terminal; got empty string")
	}
}
