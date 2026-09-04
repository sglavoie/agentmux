package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// listFooterStates are the cursor and filter states the footer is asserted
// against. Every state has to reach the same fixed order and the same row
// budget, so they are declared once and reused by the tests below rather than
// re-listed per test, where one of them would eventually be forgotten.
func listFooterStates(t *testing.T, width int) []struct {
	name string
	m    Model
} {
	t.Helper()
	base := Model{mode: ModeList, width: width, height: 24}
	filtered := withListRows(base, session.RunningSession{Base: "a", State: "key", Account: "w"})
	filtered.activeFilter = dashboard.Parse("@w api")
	twoChips := filtered
	oneChip := filtered
	oneChip.activeFilter = dashboard.Parse("api")
	summary, _, _ := summaryModel(t)
	summary.width, summary.height, summary.mode = width, 24, ModeList
	return []struct {
		name string
		m    Model
	}{
		{"empty list", base},
		{"attention row", withListRows(base, session.RunningSession{Base: "a", State: "key", Account: "w"})},
		{"active row", withListRows(base, session.RunningSession{Base: "a", State: "bolt", Account: "w"})},
		{"idle row", withListRows(base, session.RunningSession{Base: "a", State: "", Account: "w"})},
		{"summary row", summary},
		{"one filter chip", oneChip},
		{"two filter chips", twoChips},
	}
}

// listFooterRows renders the ModeList footer as its physical rows, stripped of
// the zebra striping renderFooter adds. It is the unit every footer assertion
// below counts and reads, because "two rows" is a claim about what the terminal
// draws rather than about how many strings the builder returned.
func listFooterRows(m Model) []string {
	lines := listModeFooterLines(m, "")
	if len(lines) == 0 {
		return nil
	}
	return strings.Split(strings.Join(lines, "\n"), "\n")
}

// TestListFooterPaletteOrder asserts the rendered footer follows the palette's
// declaration order in every state. A key that moved as the cursor moved would
// defeat the point of a fixed palette: the operator learns a position, not a
// lookup. Widths where truncation drops the tail are included, because a drop
// must shorten the sequence rather than reorder what is left.
func TestListFooterPaletteOrder(t *testing.T) {
	for _, width := range []int{0, 80, 120, 160, 300} {
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				out := strings.Join(stripAnsiLines(listFooterRows(tc.m)), "\n")
				prev := -1
				for _, frag := range shownFooterFragments(tc.m) {
					idx := strings.Index(out, frag)
					if idx < 0 {
						// Below the drop point the fragment is legitimately absent;
						// the marker is what promises it is still reachable.
						if strings.Contains(out, footerHintMore) {
							break
						}
						t.Fatalf("fragment %q missing with no overflow marker: %q", frag, out)
					}
					if idx <= prev {
						t.Errorf("fragment %q out of order (idx=%d, prev=%d) in %q", frag, idx, prev, out)
					}
					prev = idx
				}
			})
		}
	}
}

// shownFooterFragments is the ordered `key label` text the palette would draw
// for m if width were unlimited: the sequence any actual render must be a
// prefix of.
func shownFooterFragments(m Model) []string {
	var out []string
	for _, c := range listFooterPalette {
		if c.textFor != nil {
			continue
		}
		if c.shownFn != nil && !c.shownFn(m) {
			continue
		}
		key, label := c.key, c.label
		if c.keyFor != nil {
			key = c.keyFor(m)
		}
		if c.labelFor != nil {
			label = c.labelFor(m)
		}
		out = append(out, key+" "+label)
	}
	return out
}

func stripAnsiLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, stripAnsi(line))
	}
	return out
}

// TestListFooterCapsHintRowsPerBand asserts the row budget each band spends:
// exactly one row in compact, and never more than two in standard and spacious.
// The cap is the whole point of the palette — every row the footer takes is a
// session the list does not show — so it is asserted per state and per width
// rather than at one representative size.
func TestListFooterCapsHintRowsPerBand(t *testing.T) {
	for _, width := range []int{79, 80, 119, 120, 160} {
		want := footerHintMaxLines
		if dashboard.BandFor(width) == dashboard.BandCompact {
			want = 1
		}
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				rows := listFooterRows(tc.m)
				if dashboard.BandFor(width) == dashboard.BandCompact {
					if len(rows) != want {
						t.Fatalf("compact width %d rendered %d rows, want exactly %d:\n%s", width, len(rows), want, strings.Join(rows, "\n"))
					}
					return
				}
				if len(rows) == 0 || len(rows) > want {
					t.Fatalf("width %d rendered %d rows, want 1..%d:\n%s", width, len(rows), want, strings.Join(rows, "\n"))
				}
			})
		}
	}
}

// TestListFooterMarksOverflowWhenTruncated asserts the two-row budget is honest:
// a width that drops candidates says so, and a width wide enough for all of them
// does not cry overflow.
func TestListFooterMarksOverflowWhenTruncated(t *testing.T) {
	m := withListRows(Model{mode: ModeList, height: 24}, session.RunningSession{Base: "a", State: "bolt", Account: "w"})

	narrow := m
	narrow.width = 80
	out := strings.Join(stripAnsiLines(listFooterRows(narrow)), "\n")
	if !strings.Contains(out, footerHintMore) {
		t.Errorf("width 80 fits the whole palette in two rows? got:\n%s", out)
	}
	for _, want := range []string{"j/k up/down", "a actions", ": commands", "/ filter", "? help"} {
		if !strings.Contains(out, want) {
			t.Errorf("essential hint %q dropped at width 80: %q", want, out)
		}
	}

	wide := m
	wide.width = 300
	out = strings.Join(stripAnsiLines(listFooterRows(wide)), "\n")
	if strings.Contains(out, footerHintMore) {
		t.Errorf("width 300 marked overflow with room to spare:\n%s", out)
	}
	if !strings.Contains(out, "R resort") {
		t.Errorf("width 300 dropped the last extra-band hint:\n%s", out)
	}
}

// TestListFooterTruncationKeepsTokensWhole asserts the drop loop never leaves a
// half-written binding behind. A row ending mid-key would advertise a keystroke
// that does not exist, which is worse than the omission the marker admits to.
func TestListFooterTruncationKeepsTokensWhole(t *testing.T) {
	for _, width := range []int{79, 80, 119, 120, 160} {
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				whole := map[string]bool{footerHintMore: true}
				for _, frag := range shownFooterFragments(tc.m) {
					whole[frag] = true
				}
				for _, row := range stripAnsiLines(listFooterRows(tc.m)) {
					if strings.Contains(row, "…") {
						t.Fatalf("row was hard-clamped instead of dropped: %q", row)
					}
					if dashboard.BandFor(width) == dashboard.BandCompact {
						continue // the compact row has its own abbreviated vocabulary
					}
					for _, tok := range strings.Split(strings.TrimSpace(row), strings.TrimSpace(footerHintSeparator)) {
						tok = strings.TrimSpace(tok)
						if tok == "" || whole[tok] {
							continue
						}
						// The position fragment and state gloss bind no key and so are
						// not in the fragment list; both are self-delimiting text.
						if strings.Contains(tok, " of ") || strings.HasPrefix(tok, "…") {
							continue
						}
						if !isFooterMetadataToken(tc.m, tok) {
							t.Errorf("row %q contains partial token %q", row, tok)
						}
					}
				}
			})
		}
	}
}

// isFooterMetadataToken reports whether tok is one of the two non-binding
// candidates: the position fragment or the selected row's state gloss.
func isFooterMetadataToken(m Model, tok string) bool {
	for _, c := range listFooterPalette {
		if c.textFor == nil {
			continue
		}
		for _, position := range []string{"", "1–2 of 7"} {
			if text := stripAnsi(c.textFor(m, position)); text != "" && text == tok {
				return true
			}
		}
	}
	return false
}

// TestListFooterJumpChordCapsAtTwoRows asserts the pending-bracket footer obeys
// the same budget as the palette it replaces, in both directions. The heading
// and `esc cancels` are essential: a chord with no visible escape hatch is a
// trap, and one with no visible direction is a coin flip.
func TestListFooterJumpChordCapsAtTwoRows(t *testing.T) {
	for _, dir := range []int{1, -1} {
		heading := "jump next:"
		if dir == -1 {
			heading = "jump prev:"
		}
		for _, width := range []int{79, 80, 119, 120, 160} {
			m := withListRows(Model{mode: ModeList, width: width, height: 24, pendingJumpDir: dir},
				session.RunningSession{Base: "a", State: "key", Account: "w"})
			rows := listFooterRows(m)
			want := footerHintMaxLines
			if dashboard.BandFor(width) == dashboard.BandCompact {
				want = 1
			}
			if len(rows) == 0 || len(rows) > want {
				t.Errorf("dir %d width %d: chord spent %d rows, want 1..%d:\n%s", dir, width, len(rows), want, strings.Join(rows, "\n"))
			}
			out := strings.Join(stripAnsiLines(rows), "\n")
			if dashboard.BandFor(width) == dashboard.BandCompact {
				continue // the compact chord row has its own abbreviated vocabulary
			}
			if !strings.Contains(out, heading) {
				t.Errorf("dir %d width %d: chord footer lost its heading: %q", dir, width, out)
			}
			if !strings.Contains(out, "esc cancels") {
				t.Errorf("dir %d width %d: chord footer lost its escape hatch: %q", dir, width, out)
			}
			// The chord vocabulary replaces the palette; showing both would print
			// two meanings for `p`, `e`, `a`, `q`, `s`, and `c` at once.
			for _, leaked := range []string{"j/k up/down", "a actions", "p preview"} {
				if strings.Contains(out, leaked) {
					t.Errorf("dir %d width %d: palette hint %q leaked into the chord footer: %q", dir, width, leaked, out)
				}
			}
		}
	}
}

// TestListFooterReclaimsListCapacity asserts the capped footer hands its saved
// rows to the list rather than merely hiding output. The two sizes are the ones
// the acceptance brief names: a standard 80×24 and a spacious 120×32.
func TestListFooterReclaimsListCapacity(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 32}} {
		m := scrollModel()
		m.width, m.height = size.w, size.h
		m.filtered = makeRowsForScroll(40)
		m.rebuildItems()

		footerRows := len(rootFooterLines(m))
		if footerRows > footerHintMaxLines {
			t.Errorf("%dx%d: footer spent %d rows, want at most %d", size.w, size.h, footerRows, footerHintMaxLines)
		}
		if got := footerBudgetLines(m); got != footerRows {
			t.Errorf("%dx%d: layout budgeted %d footer rows but %d were rendered", size.w, size.h, got, footerRows)
		}

		// The list pane must actually receive the difference: chrome is the
		// terminal height minus the visible list, so a footer that shrank without
		// the list growing would show up here as unchanged capacity.
		visible := listVisibleHeight(m)
		if visible != size.h-listChromeLines(m) {
			t.Errorf("%dx%d: visible=%d does not account for chrome=%d", size.w, size.h, visible, listChromeLines(m))
		}
		rendered := len(strings.Split(strings.TrimRight(stripAnsi(renderListAt(m, m.width, visible)), "\n"), "\n"))
		if rendered != visible {
			t.Errorf("%dx%d: list rendered %d rows into a %d-row pane", size.w, size.h, rendered, visible)
		}
	}
}

func TestListFooterHasNoSpacerAndZebraRowsSpanViewport(t *testing.T) {
	const width = 110
	m := withListRows(Model{mode: ModeList, width: width}, session.RunningSession{State: "bolt"})

	rendered := renderFooter(m)
	plainLines := strings.Split(stripAnsi(rendered), "\n")
	if len(plainLines) != footerHintMaxLines {
		t.Fatalf("expected %d capped footer rows, got %d:\n%s", footerHintMaxLines, len(plainLines), stripAnsi(rendered))
	}
	for i, line := range plainLines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("unexpected blank footer row %d in:\n%s", i, stripAnsi(rendered))
		}
	}
	if !strings.Contains(plainLines[0], "j/k up/down") {
		t.Fatalf("expected navigation to lead the footer, got %q", plainLines[0])
	}

	assertZebraFooterRows(t, rendered, width)
}

func TestRootFootersHaveZebraRows(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "dashboard list", m: Model{mode: ModeList, width: 72}},
		{name: "dashboard search", m: Model{mode: ModeSearch, width: 72}},
		{name: "confirmation", m: Model{mode: ModeConfirm, width: 72}},
		{name: "session launcher", m: Model{mode: ModePicker, width: 72}},
		{name: "config picker", m: Model{mode: ModeConfigPicker, width: 72}},
		{name: "help", m: Model{mode: ModeHelp, width: 72}},
		{name: "actions", m: Model{mode: ModeActions, width: 72}},
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 72}},
		{name: "session browser", m: Model{mode: ModeSessionSearch, width: 72}},
		{name: "transcript viewer", m: Model{mode: ModeSessionView, width: 72}},
		{name: "delete choice", m: Model{mode: ModeDeleteChoice, width: 72}},
		{name: "chip picker", m: Model{mode: ModeChipPicker, width: 72}},
		{name: "message reader", m: Model{mode: ModeMessageDetail, width: 72}},
		{name: "plan browser", m: Model{mode: ModePlanSearch, width: 72}},
		{name: "plan viewer", m: Model{mode: ModePlanView, width: 72}},
		{name: "jump chord", m: Model{mode: ModeList, width: 72, pendingJumpDir: 1}},
		{
			name: "active filter",
			m: Model{
				mode:         ModeList,
				width:        72,
				activeFilter: dashboard.Filter{Substrings: []string{"api"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertZebraFooterRows(t, renderFooter(tc.m), tc.m.width)
		})
	}
}

func TestFooterZebraExcludesViewsWithoutRootFooter(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "detail overlay", m: Model{mode: ModeDetail, width: 72}},
		{name: "dashboard preview", m: Model{mode: ModeList, width: 72, previewEnabled: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderFooter(tc.m); got != "" {
				t.Errorf("renderFooter() = %q, want no root footer", got)
			}
		})
	}
}

func assertZebraFooterRows(t *testing.T, rendered string, width int) {
	t.Helper()
	if rendered == "" {
		t.Fatal("expected a root footer")
	}

	background := footerStripeBackgroundSequence()
	for i, line := range strings.Split(rendered, "\n") {
		if i%2 == 0 {
			if !strings.HasPrefix(line, background) {
				t.Errorf("row %d should start with zebra background", i)
			}
			if got := layout.StyledWidth(line); got != width {
				t.Errorf("striped row %d width = %d, want full viewport width %d", i, got, width)
			}
			if !strings.Contains(line, ansi.ResetStyle+background) {
				t.Errorf("striped row %d should reapply background after nested style resets", i)
			}
			continue
		}
		if strings.Contains(line, background) {
			t.Errorf("row %d should retain the terminal's default background", i)
		}
	}
}

// TestListFooterKeyHintsAlwaysPresent locks in the discoverability
// requirement from the brief: `e` (peek), `C` (launch), `x` (clear), and
// `X` (close) must appear in the footer in every state group, even when dimmed.
// The models carry no width, which is the state that shows the whole palette:
// once a terminal size is known these four are in the bands the budget may
// shed, and the `? more` marker is what carries the requirement instead.
func TestListFooterKeyHintsAlwaysPresent(t *testing.T) {
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
			m:    withListRows(Model{}, session.RunningSession{State: "key"}),
		},
		{
			name: "active",
			m:    withListRows(Model{}, session.RunningSession{State: "bolt"}),
		},
		{
			name: "idle",
			m:    withListRows(Model{}, session.RunningSession{State: ""}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.Join(stripAnsiLines(listFooterRows(tc.m)), "\n")
			if !strings.Contains(out, "e peek") {
				t.Errorf("expected `e peek` in footer; got %q", out)
			}
			for _, frag := range []string{"C launch", "x clear", "X close"} {
				if !strings.Contains(out, frag) {
					t.Errorf("expected %q in footer; got %q", frag, out)
				}
			}
			for _, stale := range []string{"F run plan", "t tail"} {
				if strings.Contains(out, stale) {
					t.Errorf("stale loop command %q should not be advertised; got %q", stale, out)
				}
			}
		})
	}
}

func TestViewerFootersAdvertiseCopy(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		wants []string
	}{
		{name: "session search", mode: ModeSessionSearch, wants: []string{"i ID", "Y path"}},
		{name: "session viewer", mode: ModeSessionView, wants: []string{"y copy", "Y path"}},
		{name: "plan search", mode: ModePlanSearch, wants: []string{"Y path"}},
		{name: "plan viewer", mode: ModePlanView, wants: []string{"y copy", "Y path"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(renderFooter(Model{mode: tc.mode}))
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("footer = %q, want %q hint", out, want)
				}
			}
		})
	}
}
