package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
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
	t.Parallel()

	for _, width := range []int{0, 80, 120, 160, 300} {
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				out := strings.Join(stripAnsiLines(listFooterRows(tc.m)), "\n")
				prev, dropped := -1, ""
				for _, frag := range shownFooterFragments(tc.m) {
					idx := strings.Index(out, frag)
					if idx < 0 {
						// Below the drop point the fragment is legitimately absent.
						// What is not legitimate is a later one surviving it: the
						// rendered row must be a PREFIX of the declared sequence, or
						// the operator is learning a position that moves.
						dropped = frag
						continue
					}
					if dropped != "" {
						t.Fatalf("fragment %q rendered after %q was dropped: %q", frag, dropped, out)
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
	if m.items.Len() == 0 {
		return []string{"l launch", "S past sessions", ": commands", "? help", "q quit"}
	}
	var out []string
	for _, c := range listFooterPalette() {
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

// TestListFooterIsExactlyOneRowAtEveryWidth asserts the row budget the dashboard
// spends on its footer: one, in every band and every cursor state. The cap is
// the whole point of the palette — every row the footer takes is a session the
// list does not show — so it is asserted per state and per width rather than at
// one representative size.
func TestListFooterIsExactlyOneRowAtEveryWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{20, 40, 60, 79, 80, 119, 120, 160, 300} {
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				rows := listFooterRows(tc.m)
				if len(rows) != 1 {
					t.Fatalf("width %d rendered %d rows, want exactly 1:\n%s", width, len(rows), strings.Join(rows, "\n"))
				}
				if got := layout.StyledWidth(rows[0]); got > width {
					t.Fatalf("width %d rendered a %d-cell row: %q", width, got, stripAnsi(rows[0]))
				}
			})
		}
	}
}

// The row never mixes the labelled spelling with the bare-key one. A row that
// abbreviated only the tokens it had to would make the operator re-read it to
// find out which half changed wording, which is the cost the all-or-nothing rule
// buys out.
func TestListFooterFallsBackToBareKeysWholesale(t *testing.T) {
	t.Parallel()

	m := withListRows(Model{mode: ModeList, height: 24}, session.RunningSession{Base: "a", State: "bolt", Account: "w"})
	// The narrow end of the sweep starts where the bare-key row still fits: below
	// that the row is hard-clamped, and a clamped row has no spelling to be
	// consistent about.
	for _, width := range []int{26, 40, 60, 71} {
		m.width = width
		row := stripAnsi(listFooterRows(m)[0])
		if strings.Contains(row, "up/down") {
			continue // still in the labelled spelling
		}
		for _, labelled := range []string{"actions", "commands", "filter", "help", "focus"} {
			if strings.Contains(row, labelled) {
				t.Errorf("width %d: bare-key row %q kept the label %q", width, row, labelled)
			}
		}
		if !strings.Contains(row, "j/k") || !strings.Contains(row, "?") {
			t.Errorf("width %d: bare-key row %q dropped an essential key", width, row)
		}
	}
}

// TestListFooterMarksOverflowWhenTruncated asserts the one-row budget is honest:
// a width that drops candidates says so, and a width wide enough for all of them
// does not cry overflow.
//
// The wide case measures the unbounded row rather than naming a number, so the
// assertion cannot rot the next time a binding joins the palette.
func TestListFooterMarksOverflowWhenTruncated(t *testing.T) {
	t.Parallel()

	m := withListRows(Model{mode: ModeList, height: 24}, session.RunningSession{Base: "a", State: "bolt", Account: "w"})

	narrow := m
	narrow.width = 100
	out := strings.Join(stripAnsiLines(listFooterRows(narrow)), "\n")
	if strings.Count(out, "?") != 1 {
		t.Errorf("expected exactly one help route: %s", out)
	}
	for _, want := range []string{"j/k up/down", "a actions", ": commands", "/ filter", "? help"} {
		if !strings.Contains(out, want) {
			t.Errorf("essential hint %q dropped at width 100: %q", want, out)
		}
	}

	unbounded := m
	unbounded.width = 0
	full := listFooterRows(unbounded)[0]

	wide := m
	wide.width = layout.StyledWidth(full)
	out = strings.Join(stripAnsiLines(listFooterRows(wide)), "\n")
	if strings.Contains(out, footerHintMore) {
		t.Errorf("a width that fits the whole palette marked overflow:\n%s", out)
	}
	if !strings.Contains(out, "l launch") {
		t.Errorf("a width that fits the whole palette dropped the last hint in declaration order:\n%s", out)
	}
}

// TestListFooterTruncationKeepsTokensWhole asserts the drop loop never leaves a
// half-written binding behind. A row ending mid-key would advertise a keystroke
// that does not exist, which is worse than the omission the marker admits to.
func TestListFooterTruncationKeepsTokensWhole(t *testing.T) {
	t.Parallel()

	for _, width := range []int{79, 80, 119, 120, 160} {
		for _, tc := range listFooterStates(t, width) {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				whole := map[string]bool{footerHintMore: true}
				for _, frag := range shownFooterFragments(tc.m) {
					whole[frag] = true
				}
				// The bare-key spelling is whole too: a fallback row is every
				// surviving key and nothing partial.
				for _, c := range listFooterPalette() {
					if c.textFor != nil {
						continue
					}
					key := c.key
					if c.keyFor != nil {
						key = c.keyFor(tc.m)
					}
					if key != "" {
						whole[listFooterShortKey(key)] = true
					}
				}
				for _, row := range stripAnsiLines(listFooterRows(tc.m)) {
					if strings.Contains(row, "…") {
						t.Fatalf("row was hard-clamped instead of dropped: %q", row)
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
	for _, c := range listFooterPalette() {
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

// TestListFooterIsOneRowAtEveryWidth asserts the dashboard footer never grows a
// second physical row. It replaces the jump-chord budget test: the chord had a
// footer of its own that could spend two rows, and both the chord and that
// second row are retired. The widths are the band boundaries — one below and one
// at each — because the footer picks its vocabulary per band and a regression
// would land on the edge rather than in the middle.
func TestListFooterIsOneRowAtEveryWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{79, 80, 119, 120, 160} {
		m := withListRows(Model{mode: ModeList, width: width, height: 24},
			session.RunningSession{Base: "a", State: "key", Account: "w"})
		rows := listFooterRows(m)
		if len(rows) != 1 {
			t.Errorf("width %d: footer spent %d rows, want exactly 1:\n%s", width, len(rows), strings.Join(rows, "\n"))
			continue
		}
		out := stripAnsi(rows[0])
		// The chord's vocabulary must not survive anywhere in the footer: these
		// are the three fragments only the pending-bracket row ever printed.
		for _, retired := range []string{"jump next:", "jump prev:", "esc cancels"} {
			if strings.Contains(out, retired) {
				t.Errorf("width %d: retired chord fragment %q still in the footer: %q", width, retired, out)
			}
		}
	}
}

// TestListFooterReclaimsListCapacity asserts the capped footer hands its saved
// rows to the list rather than merely hiding output. The two sizes are the ones
// the acceptance brief names: a standard 80×24 and a spacious 120×32.
func TestListFooterReclaimsListCapacity(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	const width = 110
	m := withListRows(Model{mode: ModeList, width: width}, session.RunningSession{State: "bolt"})

	rendered := renderFooter(m)
	plainLines := strings.Split(stripAnsi(rendered), "\n")
	if len(plainLines) != 1 {
		t.Fatalf("expected one dashboard footer row, got %d:\n%s", len(plainLines), stripAnsi(rendered))
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
	t.Parallel()

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
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 72}},
		{name: "session browser", m: Model{mode: ModeSessionSearch, width: 72}},
		{name: "transcript viewer", m: Model{mode: ModeSessionView, width: 72}},
		{name: "delete choice", m: Model{mode: ModeDeleteChoice, width: 72}},
		{name: "chip picker", m: Model{mode: ModeChipPicker, width: 72}},
		{name: "message reader", m: Model{mode: ModeMessageDetail, width: 72}},
		{name: "plan browser", m: Model{mode: ModePlanSearch, width: 72}},
		{name: "plan viewer", m: Model{mode: ModePlanView, width: 72}},
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
	t.Parallel()

	cases := []struct {
		name string
		m    Model
	}{
		{name: "detail overlay", m: Model{mode: ModeDetail, width: 72}},
		// The actions menu joined them: its own hint line names select, run, the
		// letters, and how to close, and a root row repeating those was the same
		// instructions twice in two wordings.
		{name: "actions menu", m: Model{mode: ModeActions, width: 72}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderFooter(tc.m); got != "" {
				t.Errorf("renderFooter() = %q, want no root footer", got)
			}
		})
	}

	// Neither inspector state is such a view, and that is a reversal worth
	// stating. The pane is on by default, so suppressing the footer beside a
	// split would leave a first-run dashboard with no key hints at all; and full
	// screen is a display state of the dashboard rather than a modal overlay —
	// the keys that leave it are `i` and escape, and the footer is the only place
	// either of them is written down.
	for _, tc := range []struct {
		name    string
		display inspectorDisplay
		width   int
	}{
		{name: "split inspector", display: inspectorAutomatic, width: 160},
		{name: "full-screen inspector", display: inspectorFullScreen, width: 72},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{mode: ModeList, width: tc.width, height: 40, cfg: &config.Config{},
				inspectorDisplay: tc.display}
			if got := renderFooter(m); got == "" {
				t.Error("the inspector suppressed the root footer")
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

// TestListFooterKeyHintsAlwaysPresent carries the same discoverability
// requirement the direct keymap used to: peek, launch, clear icon, and close
// must be findable from the dashboard in every state group without knowing they
// exist. What changed is where the footer points. Naming `e`, `C`, `x`, and `X`
// one by one is what made the row wide enough to need two of them; they are
// entries in the actions overlay now, so the footer names `a` and the overlay
// names them — and the overlay, unlike a dim footer key, states the reason a
// blocked entry is blocked.
//
// Both halves are asserted here: the footer offers the route in every state,
// and the route still reaches all four. A test that only checked the retired
// spellings were gone would pass just as well if the capabilities had gone with
// them.
func TestListFooterKeyHintsAlwaysPresent(t *testing.T) {
	t.Parallel()

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
			if m := tc.m; m.items.Len() > 0 && !strings.Contains(out, "a actions") {
				t.Errorf("expected the route to the per-row commands in footer; got %q", out)
			}
			// The direct spellings are retired, and a footer that still named
			// them would be advertising keys the dispatcher drops.
			for _, stale := range []string{"e peek", "C launch", "x clear", "X close", "F run plan", "t tail"} {
				if strings.Contains(out, stale) {
					t.Errorf("retired hint %q should not be advertised; got %q", stale, out)
				}
			}
			// And the route arrives: every capability the footer used to spell
			// out is an entry on the overlay `a` opens.
			overlay := map[string]string{}
			for _, a := range actionsFor(tc.m) {
				overlay[a.KeyMatch] = a.Label
			}
			for key, label := range map[string]string{
				keyE: "Peek scrollback", keyDashboardLaunch: "Launch another session",
				keyClearStatus: "Clear status", keyClose: "End Session",
			} {
				if got := overlay[key]; got != label {
					t.Errorf("actions overlay entry %q = %q, want %q", key, got, label)
				}
			}
		})
	}
}

func TestViewerFootersAdvertiseCopy(t *testing.T) {
	t.Parallel()

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
