package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// collapse is the form a claim about wrapped prose can be made in: styling gone
// and every run of whitespace reduced to one space, so a sentence that the
// overlay broke across two lines still reads as the sentence it is.
func collapse(s string) string {
	return strings.Join(strings.Fields(ansi.Strip(s)), " ")
}

// helpText renders the overlay body at a width and returns it collapsed.
func helpText(width int) string { return collapse(strings.Join(helpLines(width), "\n")) }

// TestHelpCoversEveryBinding is what holds the generation honest: the overlay is
// built from the same table the dispatcher walks, so a binding that exists is a
// binding that is documented — by the keys it answers and by the sentence that
// says what bounds it.
//
// It is asserted at three widths because the sentences WRAP. A check at one
// comfortable width would pass on a keymap that became unreadable the moment the
// terminal narrowed.
func TestHelpCoversEveryBinding(t *testing.T) {
	for _, width := range []int{helpContentWidth(160), helpContentWidth(100), helpContentWidth(60)} {
		body := helpText(width)
		for _, b := range gridBindings() {
			if !strings.Contains(body, collapse(b.footerKeysText())) {
				t.Errorf("at width %d the overlay never names the keys %q:\n%s", width, b.footerKeysText(), body)
			}
			if !strings.Contains(body, collapse(b.help)) {
				t.Errorf("at width %d the overlay never says %q:\n%s", width, b.help, body)
			}
		}
		for _, section := range helpSectionOrder {
			if !strings.Contains(body, collapse(section)) {
				t.Errorf("at width %d the overlay drew no %q section", width, section)
			}
		}
	}
}

// TestHelpDocumentsTheKeyThatIsNotOnTheFooter is the point of separating the two
// projections, stated as an assertion. The undo is deliberately off the row —
// it is real for thirty seconds after a confirmed close — and the overlay is where
// an operator who has never closed anything finds out it exists at all.
func TestHelpDocumentsTheKeyThatIsNotOnTheFooter(t *testing.T) {
	body := helpText(helpContentWidth(120))
	if !strings.Contains(body, "u ") {
		t.Fatalf("the overlay does not document the undo:\n%s", body)
	}
	in := viewInput(120, 30, viewSessions(2))
	if row := collapse(renderFooter(in, MeasureMode(in.Width, in.Height, false), 0)); strings.Contains(row, "undo") {
		t.Fatalf("the footer advertises the undo: %q", row)
	}
}

// TestHelpOpensAndLeavesWithoutMovingTheGrid is the property the overlay is most
// likely to break: press ? and lose your place.
//
// The selection and the page are taken before and after, and the scroll keys are
// pressed WHILE it is open — those are the same keys the grid moves by, so an
// overlay that let them through would page the grid underneath and the operator
// would come back to a different screen.
func TestHelpOpensAndLeavesWithoutMovingTheGrid(t *testing.T) {
	m, _ := atSize(t, viewSessions(40), 0, 120, 30)
	m = press(t, m, "end")
	selected, tab := m.selected, m.selectedTabID
	title := strings.Split(frameOf(m), "\n")[0]

	m = press(t, m, "?")
	if !m.help.open {
		t.Fatal("? did not raise the overlay")
	}
	if frame := frameOf(m); !strings.Contains(frame, helpTitle) {
		t.Fatalf("the overlay is open but not drawn:\n%s", frame)
	}
	for _, name := range []string{"j", "k", "down", "up", "pgdown", "pgup", "g", "G", "home", "end", "right", "left", "1", "c", "a", "n"} {
		m = press(t, m, name)
		if m.selected != selected || m.selectedTabID != tab {
			t.Fatalf("%q reached the grid under the overlay: selection moved to %d (tab %d)", name, m.selected, m.selectedTabID)
		}
		if m.acts.surface != surfaceGrid {
			t.Fatalf("%q raised surface %v from under the overlay", name, m.acts.surface)
		}
		if m.compact {
			t.Fatalf("%q toggled compact mode from under the overlay", name)
		}
	}

	m = press(t, m, "esc")
	if m.help.open {
		t.Fatal("esc did not dismiss the overlay")
	}
	if m.selected != selected || m.selectedTabID != tab {
		t.Fatalf("the round trip moved the selection to %d (tab %d), want %d (tab %d)", m.selected, m.selectedTabID, selected, tab)
	}
	if got := strings.Split(frameOf(m), "\n")[0]; got != title {
		t.Fatalf("the round trip left the title %q, want the page it opened on %q", got, title)
	}
}

// TestHelpIsAToggleAndIsNotAnExit separates the three ways out of the overlay
// from the one way out of the program. `q` under the overlay closes the overlay
// — the footer's `q exit` is the base view's, and the overlay's own hint line is
// what says so — while ctrl+c is still the operator asking for the process.
func TestHelpIsAToggleAndIsNotAnExit(t *testing.T) {
	for _, name := range []string{"?", "esc", "q"} {
		m, _ := atSize(t, viewSessions(3), 0, 120, 30)
		m = press(t, m, "?")
		next, cmd := send(t, m, key(t, name))
		if next.help.open {
			t.Fatalf("%q left the overlay open", name)
		}
		if quits(t, cmd) {
			t.Fatalf("%q quit the program instead of closing the overlay", name)
		}
	}

	m, _ := atSize(t, viewSessions(3), 0, 120, 30)
	m = press(t, m, "?")
	_, cmd := send(t, m, key(t, "ctrl+c"))
	if !quits(t, cmd) {
		t.Fatal("ctrl+c under the overlay did not quit")
	}
}

// TestHelpScrollsWithinItsContentAndSaysWhereItIs drives the scroll on a
// terminal too short to show the keymap at once, which is the only state the
// markers mean anything in.
func TestHelpScrollsWithinItsContentAndSaysWhereItIs(t *testing.T) {
	m, _ := atSize(t, viewSessions(3), 0, 120, 14)
	m = press(t, m, "?")
	if len(m.helpContent()) <= m.helpVisibleHeight() {
		t.Fatal("this terminal shows the whole keymap; the scroll assertions below would be vacuous")
	}
	bottom := len(m.helpContent()) - m.helpVisibleHeight()

	if !strings.Contains(frameOf(m), "▼") {
		t.Fatalf("the top of a scrollable keymap does not say there is more below:\n%s", frameOf(m))
	}
	if strings.Contains(frameOf(m), "▲") {
		t.Fatalf("the top of the keymap says there is something above it:\n%s", frameOf(m))
	}

	m = press(t, m, "j")
	if m.help.offset != 1 {
		t.Fatalf("j scrolled to %d, want 1", m.help.offset)
	}
	m = press(t, m, "G")
	if m.help.offset != bottom {
		t.Fatalf("G landed on %d, want the last page at %d", m.help.offset, bottom)
	}
	m = press(t, m, "j")
	if m.help.offset != bottom {
		t.Fatalf("j past the end scrolled to %d, want to stay at %d", m.help.offset, bottom)
	}
	if frame := frameOf(m); !strings.Contains(frame, "▲") || strings.Contains(frame, "▼") {
		t.Fatalf("the last page's markers are wrong:\n%s", frame)
	}
	m = press(t, m, "g")
	if m.help.offset != 0 {
		t.Fatalf("g landed on %d, want the top", m.help.offset)
	}

	// The position survives a close and reopen: an operator who went back to the
	// grid to try a key is asking for the same place in the same list.
	m = press(t, m, "G")
	m = press(t, m, "esc")
	m = press(t, m, "?")
	if m.help.offset != bottom {
		t.Fatalf("reopening the overlay landed on %d, want the %d it was left at", m.help.offset, bottom)
	}
}

// TestHelpAdvertisesOnlyKeysItAnswers applies the footer's own rule to the
// overlay's hint line, which is the second place on this surface that prints
// keys.
//
// The bindings_test table keeps the grid's two projections honest against the
// grid's declaration; the overlay's hint has no such table behind it, so the
// keys it names are read back OFF the rendered line and each one is pressed. The
// set is checked in both directions: every group the line prints is exercised
// here, and a group added to the line without a case below fails rather than
// going unchecked — which is exactly how `ctrl+f/b` could otherwise be
// advertised by a handler that answered only pgdown.
func TestHelpAdvertisesOnlyKeysItAnswers(t *testing.T) {
	// printed is what the hint spells; key is what the dispatcher matches. The
	// two differ wherever the line compresses a pair, as `ctrl+f/b` does.
	down := []struct{ printed, key string }{{"j", "j"}, {"ctrl+f", "ctrl+f"}, {"G", "G"}}
	up := []struct{ printed, key string }{{"k", "k"}, {"b", "ctrl+b"}, {"g", "g"}}
	closes := []struct{ printed, key string }{{"esc", "esc"}, {"q", "q"}, {"?", "?"}}

	advertised := map[string]bool{}
	for _, entry := range strings.Split(strings.Trim(collapse(helpHintLine(helpContentWidth(160), true, true)), "▲▼ "), " · ") {
		for _, group := range strings.Split(strings.Fields(entry)[0], "/") {
			advertised[group] = true
		}
	}

	checked := map[string]bool{}
	for _, tc := range down {
		checked[tc.printed] = true
		m := scrollableHelp(t)
		if next := press(t, m, tc.key); next.help.offset <= m.help.offset {
			t.Errorf("the hint prints %q but %q did not scroll down: offset stayed at %d", tc.printed, tc.key, next.help.offset)
		}
	}
	for _, tc := range up {
		checked[tc.printed] = true
		m := press(t, scrollableHelp(t), "G")
		if next := press(t, m, tc.key); next.help.offset >= m.help.offset {
			t.Errorf("the hint prints %q but %q did not scroll up from %d", tc.printed, tc.key, m.help.offset)
		}
	}
	for _, tc := range closes {
		checked[tc.printed] = true
		if next := press(t, scrollableHelp(t), tc.key); next.help.open {
			t.Errorf("the hint prints %q as a way out but %q left the overlay open", tc.printed, tc.key)
		}
	}

	for group := range advertised {
		if !checked[group] {
			t.Errorf("the hint advertises %q and nothing here presses it", group)
		}
	}
	for group := range checked {
		if !advertised[group] {
			t.Errorf("%q is checked here but the hint no longer prints it", group)
		}
	}
}

// scrollableHelp is the overlay open on a terminal too short to hold it, which
// is the only state a scroll key can be observed in.
func scrollableHelp(t *testing.T) model {
	t.Helper()
	m, _ := atSize(t, viewSessions(3), 0, 120, 14)
	m = press(t, m, "?")
	if len(m.helpContent()) <= m.helpVisibleHeight() {
		t.Fatal("this terminal shows the whole keymap; the scroll assertions would be vacuous")
	}
	return m
}

// TestHelpShedsItsHeaderBeforeItsHint is the short-terminal rule, and it is the
// inverse of what "least useful first" would give: the header repeats the name
// of the surface the operator is standing on, while the hint is the only
// statement of how to scroll the list and how to leave it.
func TestHelpShedsItsHeaderBeforeItsHint(t *testing.T) {
	var sawBoth, sawHintOnly, sawNeither bool
	for height := 12; height >= 1; height-- {
		header, hint, visible := helpChrome(height)
		if visible < 1 {
			t.Fatalf("at body height %d the overlay reserved %d content rows", height, visible)
		}
		if header && !hint {
			t.Fatalf("at body height %d the overlay kept its header and shed its hint", height)
		}
		switch {
		case header && hint:
			sawBoth = true
		case hint:
			sawHintOnly = true
		default:
			sawNeither = true
		}
	}
	if !sawBoth || !sawHintOnly || !sawNeither {
		t.Fatalf("the shed ladder was not exercised: both=%v hintOnly=%v neither=%v", sawBoth, sawHintOnly, sawNeither)
	}
}

// TestHelpFitsTheTerminalItIsDrawnOn is the size contract for the overlay
// itself. renderLines already asserts the frame it is composited into, so what
// is left is that the box never asks for more than the terminal has — at the
// narrow and short sizes where a fixed natural width would otherwise clip.
func TestHelpFitsTheTerminalItIsDrawnOn(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {120, 30}, {100, 24}, {80, 20}, {60, 14}, {40, 10}, {24, 6}} {
		width, height := size[0], size[1]
		m, _ := atSize(t, viewSessions(6), 0, width, height)
		m = press(t, m, "?")
		box := m.renderHelpBox()
		for i, line := range strings.Split(box, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("at %dx%d the overlay's line %d is %d cells wide: %q", width, height, i, got, ansi.Strip(line))
			}
		}
		if rows := len(strings.Split(box, "\n")); rows > height {
			t.Fatalf("at %dx%d the overlay is %d rows tall", width, height, rows)
		}
		// The frame it is composited into still holds its shape, which is what
		// makes the overlay a modal rather than a second renderer.
		renderLines(t, overviewInput(m))
	}
}
