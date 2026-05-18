package tui

import (
	"strings"
	"testing"

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
			rowCount := 0
			for _, e := range listFooterPalette {
				if e.section == footerSectionRow {
					rowCount++
					if rowCount > listFooterRowCap {
						// Entry collapsed into the overflow indicator;
						// not expected to appear verbatim in the line.
						continue
					}
				}
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
// each rendered section group, and that the row-section overflow indicator
// appears whenever the row palette exceeds the visible cap.
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
	if len(rowEntries) > listFooterRowCap && !strings.Contains(out, "…") {
		t.Errorf("expected overflow indicator '…' in %q", out)
	}

	// Section breaks must come after the row section's last visible entry
	// and before the launch entry.
	rowLast := rowEntries[listFooterRowCap-1]
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
				"enter": false, "a": false, "e": false, "r": false, "x": false,
				"c": false, "C": false,
				"o": false, "y": false,
				"l": true, "/": true, "p": true, "R": true, "?": true,
			},
		},
		{
			name: "attention row — C active, e unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "key"}},
			},
			want: map[string]bool{
				"enter": true, "a": true, "e": false, "r": true, "x": true,
				"c": true, "C": true,
				"o": false, "y": true,
				"l": true, "/": true, "p": true, "R": true, "?": true,
			},
		},
		{
			name: "active row — e active, C unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "bolt"}},
			},
			want: map[string]bool{
				"enter": true, "a": true, "e": true, "r": true, "x": true,
				"c": true, "C": false,
				"o": false, "y": true,
				"l": true, "/": true, "p": true, "R": true, "?": true,
			},
		},
		{
			name: "idle row — neither e nor C available",
			m: Model{
				filtered: []session.RunningSession{{State: ""}},
			},
			want: map[string]bool{
				"enter": true, "a": true, "e": false, "r": true, "x": true,
				"c": true, "C": false,
				"o": false, "y": true,
				"l": true, "/": true, "p": true, "R": true, "?": true,
			},
		},
		{
			name: "type-to-filter active — row actions dimmed",
			m: Model{
				filtered:   []session.RunningSession{{State: "bolt"}},
				typeBuffer: "ap",
			},
			want: map[string]bool{
				"enter": false, "a": false, "e": false, "r": false, "x": false,
				"c": false, "C": false,
				"o": false, "y": false,
				"l": true, "/": true, "p": true, "R": true, "?": true,
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
