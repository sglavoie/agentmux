package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestListFooterPaletteOrder asserts that the palette is rendered in its
// declaration order regardless of the cursor row's state. The test
// reconstructs the expected fragment positions and verifies each entry's
// `key label` substring appears in order on the stripped line.
func TestListFooterPaletteOrder(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "key", Account: "w"}},
			},
		},
		{
			name: "active row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "bolt", Account: "w"}},
			},
		},
		{
			name: "idle row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "", Account: "w"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(listFooterLine1(tc.m))
			prev := -1
			for _, e := range listFooterPalette {
				frag := e.key + " " + e.label
				idx := strings.Index(out, frag)
				if idx < 0 {
					t.Errorf("expected fragment %q in footer; got %q", frag, out)
					continue
				}
				if idx <= prev {
					t.Errorf("fragment %q out of order (idx=%d, prev=%d) in %q", frag, idx, prev, out)
				}
				prev = idx
			}
		})
	}
}

// TestListFooterSections asserts the section separator glyph appears between
// each rendered section group.
func TestListFooterSections(t *testing.T) {
	out := stripAnsi(listFooterLine1(Model{
		filtered: []session.RunningSession{{State: "bolt"}},
	}))

	sepCount := strings.Count(out, strings.TrimSpace(listFooterSectionSeparator))
	wantSeps := len(listFooterSectionOrder) - 1
	if sepCount != wantSeps {
		t.Errorf("expected %d section separators, got %d in %q", wantSeps, sepCount, out)
	}

	rowEntries := footerEntriesForSection(footerSectionRow)

	// Section breaks must come after the row section's last visible entry
	// and before the launch entry.
	rowLast := rowEntries[len(rowEntries)-1]
	rowLastIdx := strings.Index(out, rowLast.key+" "+rowLast.label)
	launchIdx := strings.Index(out, "l launch")
	sepIdx := strings.Index(out, strings.TrimSpace(listFooterSectionSeparator))
	if rowLastIdx < 0 || launchIdx < 0 || sepIdx < 0 {
		t.Fatalf("missing landmark fragments in %q (rowLast=%d, launch=%d, sep=%d)",
			out, rowLastIdx, launchIdx, sepIdx)
	}
	if rowLastIdx >= sepIdx || sepIdx >= launchIdx {
		t.Errorf("section separator out of place between row and launch: rowLast=%d sep=%d launch=%d in %q",
			rowLastIdx, sepIdx, launchIdx, out)
	}
}

// TestListFooterEAndCAlwaysPresent locks in the discoverability requirement
// from the brief: `e` (peek) and `C` (clear) must appear in the footer in
// every state group, even when dimmed.
func TestListFooterEAndCAlwaysPresent(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention",
			m: Model{
				filtered: []session.RunningSession{{State: "key"}},
			},
		},
		{
			name: "active",
			m: Model{
				filtered: []session.RunningSession{{State: "bolt"}},
			},
		},
		{
			name: "idle",
			m: Model{
				filtered: []session.RunningSession{{State: ""}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(listFooterLine1(tc.m))
			if !strings.Contains(out, "e peek") {
				t.Errorf("expected `e peek` in footer; got %q", out)
			}
			if !strings.Contains(out, "C clear") {
				t.Errorf("expected `C clear` in footer; got %q", out)
			}
		})
	}
}

func TestViewerFootersAdvertiseCopy(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
	}{
		{name: "session viewer", mode: ModeSessionView},
		{name: "plan viewer", mode: ModePlanView},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(renderFooter(Model{mode: tc.mode}))
			if !strings.Contains(out, "y copy") {
				t.Fatalf("footer = %q, want y copy hint", out)
			}
		})
	}
}

// TestListFooterLine1WrapsNarrow asserts that line 1 emits at least one
// newline and every line fits within the configured terminal width when the
// width is too narrow for the full palette.
func TestListFooterLine1WrapsNarrow(t *testing.T) {
	const width = 60
	m := Model{width: width}
	out := listFooterLine1(m)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected line1 to wrap at width %d, got single line:\n%s", width, out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds max %d: %q", i, w, width, line)
		}
	}
}

// TestListFooterLine1NoWrapWide asserts no wrapping happens when the width
// comfortably exceeds the rendered palette.
func TestListFooterLine1NoWrapWide(t *testing.T) {
	m := Model{width: 200}
	out := listFooterLine1(m)
	if strings.Contains(out, "\n") {
		t.Errorf("expected line1 to fit on one line at width 200, got:\n%s", out)
	}
}

// TestListFooterLine1ZeroWidth asserts that a zero-width model (before the
// first WindowSizeMsg) emits a single unwrapped line — preserving the
// pre-wrap fallback behavior.
func TestListFooterLine1ZeroWidth(t *testing.T) {
	m := Model{}
	out := listFooterLine1(m)
	if strings.Contains(out, "\n") {
		t.Errorf("expected single line at zero width, got:\n%s", out)
	}
}

// TestListFooterLine2WrapsNarrow asserts that line 2 wraps when the nav
// fragments overflow the configured width.
func TestListFooterLine2WrapsNarrow(t *testing.T) {
	const width = 40
	m := Model{width: width}
	nav := []string{
		"j/k: up/down", "]/[: jump-chord", "}/{: nav+focus",
		"enter: focus", "a: actions", "/: filter",
		"?: help", "q: quit", "p: preview",
	}
	out := listFooterLine2(m, nav)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected line2 to wrap at width %d, got single line:\n%s", width, out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds max %d: %q", i, w, width, line)
		}
	}
}

// TestListFooterLine2ZeroWidth asserts that a zero-width model emits a single
// unwrapped line for line 2.
func TestListFooterLine2ZeroWidth(t *testing.T) {
	m := Model{}
	nav := []string{"j/k: up/down", "enter: focus", "q: quit"}
	out := listFooterLine2(m, nav)
	if strings.Contains(out, "\n") {
		t.Errorf("expected single line at zero width, got:\n%s", out)
	}
}

// TestListFooterAvailability verifies the per-entry availability rules: rows
// in the attention group activate `C`; bolt rows activate `e`; empty lists
// keep every row-targeted entry unavailable.
func TestListFooterAvailability(t *testing.T) {
	cases := []struct {
		name  string
		m     Model
		want  map[string]bool // expected availability per entry key
		extra map[string]bool // optional additional checks not driven by the map default
	}{
		{
			name: "empty list — only global actions available",
			m:    Model{},
			want: map[string]bool{
				"e": false, "T": false, "r": false, "x": false,
				"c": false, "C": false,
				"o": false, "O": false,
				"l": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "attention row — C active, e unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "key"}},
			},
			want: map[string]bool{
				"e": false, "T": true, "r": true, "x": true,
				"c": true, "C": true,
				"o": false, "O": false,
				"l": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "active row — e active, C unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "bolt"}},
			},
			want: map[string]bool{
				"e": true, "T": true, "r": true, "x": true,
				"c": true, "C": false,
				"o": false, "O": false,
				"l": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "idle row — neither e nor C available",
			m: Model{
				filtered: []session.RunningSession{{State: ""}},
			},
			want: map[string]bool{
				"e": false, "T": true, "r": true, "x": true,
				"c": true, "C": false,
				"o": false, "O": false,
				"l": true, "S": true, "P": true, "R": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, e := range listFooterPalette {
				want, ok := tc.want[e.key]
				if !ok {
					t.Fatalf("test case missing entry for key %q", e.key)
				}
				got := e.availFn(tc.m)
				if got != want {
					t.Errorf("availability for %q (%s): got %v, want %v", e.key, e.label, got, want)
				}
			}
		})
	}
}
