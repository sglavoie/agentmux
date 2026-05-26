package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestRenderChipPickerOverlayComposition exercises the full m.render() path
// for ModeChipPicker. It verifies the chip picker box is composited on top of
// renderSessionSearch (header bleeds through the dim) and that the total line
// count does not exceed the terminal height — the bug fixed in Session 3 was
// the overlay being pushed past the viewport.
func TestRenderChipPickerOverlayComposition(t *testing.T) {
	now := time.Now()
	m := Model{
		mode:              ModeChipPicker,
		width:             100,
		height:            40,
		chipPickerKind:    chipPickerHarness,
		chipPickerOptions: []string{"", "claude", "codex"},
		cfg:               &config.Config{},
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

	if !strings.Contains(plain, "Choose harness") {
		t.Errorf("expected chip picker header in overlay output; got %q", plain)
	}
	if !strings.Contains(plain, "Past Sessions") {
		t.Errorf("expected base session search header to bleed through dim; got %q", plain)
	}
	if !strings.Contains(out, "╭") {
		t.Errorf("expected rounded box top-left glyph in overlay output; got %q", out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) > m.height+1 {
		t.Errorf("overlay output exceeds terminal height: got %d lines, want <= %d",
			len(lines), m.height+1)
	}
}

// TestRenderChipPickerOverlayClampsOnTinyHeight ensures the overlay code path
// does not panic when overlayH collapses near zero. The base view is still
// rendered (so renderSessionSearch's chrome contract is exercised) and the
// chip picker box is best-effort composited.
func TestRenderChipPickerOverlayClampsOnTinyHeight(t *testing.T) {
	m := Model{
		mode:              ModeChipPicker,
		width:             60,
		height:            8,
		chipPickerKind:    chipPickerProfile,
		chipPickerOptions: []string{"", "work"},
		cfg:               &config.Config{},
	}

	out := m.render()
	if out == "" {
		t.Fatalf("expected non-empty output on tiny terminal; got empty string")
	}
}
