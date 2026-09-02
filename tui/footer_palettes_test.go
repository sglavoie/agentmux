package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// hintTestPalette exercises the truncation engine with known priorities:
// two essential entries that must always survive and three droppable ones,
// two of which tie so the last-declared-first rule is observable.
var hintTestPalette = []footerEntry{
	{"enter", "view", footerSectionRow, footerAlways, footerPriorityEssential},
	{"a", "alpha", footerSectionRow, footerAlways, footerPriorityPrimary},
	{"b", "bravo", footerSectionRow, footerAlways, footerPrioritySecondary},
	{"c", "charlie", footerSectionRow, footerAlways, footerPrioritySecondary},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
}

func hintText(lines []string) string {
	return stripAnsi(strings.Join(lines, "\n"))
}

func TestFooterHintLinesFitWideTerminal(t *testing.T) {
	cases := []struct {
		name    string
		palette []footerEntry
	}{
		{name: "session search", palette: sessionSearchFooterPalette},
		{name: "session view", palette: sessionViewFooterPalette},
		{name: "plan search", palette: planSearchFooterPalette},
		{name: "plan view", palette: planViewFooterPalette},
		{name: "worktrees", palette: worktreesFooterPalette},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := footerHintLines(Model{width: 200}, tc.palette, 2)
			if len(lines) > 2 {
				t.Fatalf("palette rendered %d rows at width 200, want at most 2:\n%s", len(lines), hintText(lines))
			}
			if strings.Contains(hintText(lines), footerHintMore) {
				t.Fatalf("wide palette should not truncate; got %q", hintText(lines))
			}
		})
	}
}

func TestFooterHintPalettesAdvertiseModeHints(t *testing.T) {
	cases := []struct {
		name    string
		palette []footerEntry
		wants   []string
	}{
		{
			name:    "session search",
			palette: sessionSearchFooterPalette,
			wants: []string{"/ filter", "h/p/r chips", "enter view", "c continue", "f fork",
				"w worktree", "H handoff", "E edit handoff", "d delete", "i ID", "Y path", "esc back"},
		},
		{
			name:    "session view",
			palette: sessionViewFooterPalette,
			wants: []string{"j/k move", "n/N match", "enter view message", "c continue", "f fork",
				"w worktree", "H handoff", "E edit handoff", "i ID", "y copy", "Y path", "esc back"},
		},
		{
			name:    "plan search",
			palette: planSearchFooterPalette,
			wants:   []string{"/ filter", "r project", "enter view", "o sort", "e editor", "Y path", "esc back"},
		},
		{
			name:    "plan view",
			palette: planViewFooterPalette,
			wants:   []string{"/ search", "n/N match", "y copy", "Y path", "e editor", "esc back"},
		},
		{
			name:    "worktrees",
			palette: worktreesFooterPalette,
			wants:   []string{"/ filter", "o shell", "O editor", "Y path", "r refresh", "? help", "esc back"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The zero Model makes every context-dependent entry unavailable;
			// unavailable entries change style only, never presence.
			out := hintText(footerHintLines(Model{width: 400}, tc.palette, 2))
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("palette = %q, want %q hint", out, want)
				}
			}
		})
	}
}

func TestFooterHintLinesDropsHighestPriorityFirst(t *testing.T) {
	full := footerHintLines(Model{width: 500}, hintTestPalette, 1)
	if len(full) != 1 {
		t.Fatalf("wide render = %d rows, want 1:\n%s", len(full), hintText(full))
	}
	fullWidth := layout.StyledWidth(full[0])

	lines := footerHintLines(Model{width: fullWidth - 1}, hintTestPalette, 1)
	if len(lines) != 1 {
		t.Fatalf("truncated render = %d rows, want 1:\n%s", len(lines), hintText(lines))
	}
	out := hintText(lines)
	if strings.Contains(out, "c charlie") {
		t.Fatalf("last-declared tie should drop first; got %q", out)
	}
	for _, want := range []string{"enter view", "a alpha", "b bravo", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("truncated palette = %q, want %q", out, want)
		}
	}
}

func TestFooterHintLinesNeverDropsEssentialEntries(t *testing.T) {
	lines := footerHintLines(Model{width: 20}, hintTestPalette, 1)
	out := hintText(lines)
	for _, want := range []string{"enter view", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("essential palette = %q, want %q", out, want)
		}
	}
	for _, gone := range []string{"a alpha", "b bravo", "c charlie"} {
		if strings.Contains(out, gone) {
			t.Fatalf("droppable entry %q survived at width 20: %q", gone, out)
		}
	}
}

func TestFooterHintLinesDegenerateWidth(t *testing.T) {
	lines := footerHintLines(Model{width: 0}, hintTestPalette, 1)
	if len(lines) != 1 {
		t.Fatalf("width 0 render = %d rows, want the single unwrapped fallback:\n%s", len(lines), hintText(lines))
	}
	want := footerHintIndent + strings.Join([]string{
		"enter view", "a alpha", "b bravo", "c charlie", "esc back",
	}, footerHintSeparator)
	if got := hintText(lines); got != want {
		t.Fatalf("width 0 render = %q, want %q", got, want)
	}
}
