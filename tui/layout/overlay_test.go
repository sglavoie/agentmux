package layout

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripAnsi removes ANSI CSI escape sequences from a Lipgloss-rendered
// string so tests can assert against the visible content.
func stripAnsi(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// makeBase returns a width×height block of distinct ASCII characters so tests
// can spot which cells were overwritten by the overlay.
func makeBase(width, height int) string {
	rows := make([]string, height)
	for r := 0; r < height; r++ {
		row := make([]byte, width)
		for c := 0; c < width; c++ {
			row[c] = 'a' + byte((r*width+c)%26)
		}
		rows[r] = string(row)
	}
	return strings.Join(rows, "\n")
}

func TestComposeOverlaySingleCellMiddle(t *testing.T) {
	base := makeBase(5, 5)
	got := ComposeOverlay(base, "X", 5, 5, 2, 2, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines; got %d (%q)", len(lines), got)
	}
	for i, want := range []string{
		"abcde",
		"fghij",
		"klXno",
		"pqrst",
		"uvwxy",
	} {
		if lines[i] != want {
			t.Errorf("row %d: expected %q; got %q", i, want, lines[i])
		}
	}
}

func TestComposeOverlayClampsBaseHeight(t *testing.T) {
	base := makeBase(5, 5)
	got := ComposeOverlay(base, "", 5, 3, 0, 0, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected base to clamp to 3 lines; got %d (%q)", len(lines), got)
	}
	for i, want := range []string{"abcde", "fghij", "klmno"} {
		if lines[i] != want {
			t.Errorf("row %d: expected %q; got %q", i, want, lines[i])
		}
	}
}

func TestComposeOverlayMultiLinePreservesAnsiOutsideRegion(t *testing.T) {
	styled := styleSelected.Render("hello")
	base := styled + "\n" + styled + "\n" + styled
	popup := "XX\nYY"
	got := ComposeOverlay(base, popup, 10, 3, 2, 1, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines; got %d", len(lines))
	}
	// Row 0 is untouched: must still carry the original ANSI markers.
	if !strings.Contains(lines[0], "\x1b[") {
		t.Errorf("row 0 should preserve ANSI markers; got %q", lines[0])
	}
	// Rows 1 and 2 had popup spliced in starting at column 2 — the visible
	// content at that range should be the popup text.
	if !strings.Contains(stripAnsi(lines[1]), "XX") {
		t.Errorf("row 1 should contain popup XX; got %q", stripAnsi(lines[1]))
	}
	if !strings.Contains(stripAnsi(lines[2]), "YY") {
		t.Errorf("row 2 should contain popup YY; got %q", stripAnsi(lines[2]))
	}
	// The right-side slice of the styled row should still carry ANSI bytes
	// from the original render since columns 4-9 weren't overwritten.
	if !strings.Contains(lines[1], "\x1b[") {
		t.Errorf("row 1 should preserve ANSI markers in untouched slice; got %q", lines[1])
	}
}

func TestComposeOverlayClipsPopupWiderThanBase(t *testing.T) {
	base := makeBase(5, 3)
	popup := "ABCDEFGH" // 8 wide, base is 5 wide
	got := ComposeOverlay(base, popup, 5, 3, 0, 1, false)
	lines := strings.Split(got, "\n")
	if lipgloss.Width(lines[1]) != 5 {
		t.Errorf("row 1 should be clipped to width 5; got width %d (%q)", lipgloss.Width(lines[1]), lines[1])
	}
	if lines[1] != "ABCDE" {
		t.Errorf("row 1: expected %q; got %q", "ABCDE", lines[1])
	}
}

func TestComposeOverlayTopLeftCorner(t *testing.T) {
	base := makeBase(4, 4)
	got := ComposeOverlay(base, "Z", 4, 4, 0, 0, false)
	lines := strings.Split(got, "\n")
	if lines[0] != "Zbcd" {
		t.Errorf("top-left placement: expected %q; got %q", "Zbcd", lines[0])
	}
}

func TestComposeOverlayBottomRightCorner(t *testing.T) {
	base := makeBase(4, 4)
	popup := "MN\nOP"
	popupW := PopupWidth(popup)
	popupH := 2
	got := ComposeOverlay(base, popup, 4, 4, 4-popupW, 4-popupH, false)
	lines := strings.Split(got, "\n")
	if lines[2] != "ijMN" {
		t.Errorf("bottom-right row 2: expected %q; got %q", "ijMN", lines[2])
	}
	if lines[3] != "mnOP" {
		t.Errorf("bottom-right row 3: expected %q; got %q", "mnOP", lines[3])
	}
}

func TestCenterOverlayEvenDimensions(t *testing.T) {
	base := makeBase(10, 6)
	popup := "AB\nCD"
	got := CenterOverlay(base, popup, 10, 6, false)
	lines := strings.Split(got, "\n")
	// popup 2x2 in a 10x6 area: startCol = (10-2)/2 = 4, startRow = (6-2)/2 = 2.
	if !strings.HasPrefix(stripAnsi(lines[2])[4:], "AB") {
		t.Errorf("row 2 expected AB at col 4; got %q", lines[2])
	}
	if !strings.HasPrefix(stripAnsi(lines[3])[4:], "CD") {
		t.Errorf("row 3 expected CD at col 4; got %q", lines[3])
	}
}

func TestCenterOverlayOddDimensions(t *testing.T) {
	base := makeBase(9, 5)
	popup := "X"
	got := CenterOverlay(base, popup, 9, 5, false)
	lines := strings.Split(got, "\n")
	// popup 1x1 in 9x5: startCol = (9-1)/2 = 4, startRow = (5-1)/2 = 2.
	if lines[2][4] != 'X' {
		t.Errorf("row 2 col 4 expected X; got %q", lines[2])
	}
}

func TestDimContentPreservesAnsiSequences(t *testing.T) {
	styled := styleSelected.Render("hello")
	got := dimContent(styled)
	// Dimming should not strip the upstream ANSI sequences.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("dimContent should preserve ANSI markers; got %q", got)
	}
	// The visible text should remain intact.
	if !strings.Contains(stripAnsi(got), "hello") {
		t.Errorf("dimContent should preserve visible text; got %q", stripAnsi(got))
	}
}

func TestDimContentMultiLine(t *testing.T) {
	got := dimContent("one\ntwo\nthree")
	if strings.Count(got, "\n") != 2 {
		t.Errorf("dimContent should preserve line count; got %q", got)
	}
}

func TestPopupWidthWithAnsi(t *testing.T) {
	popup := styleSelected.Render("hello") + "\n" + styleSelected.Render("hi")
	if w := PopupWidth(popup); w != 5 {
		t.Errorf("PopupWidth on ANSI-wrapped lines: expected 5; got %d", w)
	}
}

func TestPopupWidthEmpty(t *testing.T) {
	if w := PopupWidth(""); w != 0 {
		t.Errorf("PopupWidth(\"\") expected 0; got %d", w)
	}
}

func TestComposeOverlayDimAppliesToBase(t *testing.T) {
	base := "plain"
	got := ComposeOverlay(base, "", 5, 1, 0, 0, true)
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("dim=true on empty popup should still dim base; got %q", got)
	}
}

// TestPopupWidthEmojiGraphemeAware locks in that PopupWidth measures
// emoji popups via StyledWidth so wide glyphs count for their
// full cell width (2), not their byte length.
func TestPopupWidthEmojiGraphemeAware(t *testing.T) {
	popup := "🐱🐱\n🐱"
	// Two cats per line == 4 cells; tallest line == 4.
	if got := PopupWidth(popup); got != 4 {
		t.Errorf("PopupWidth(emoji) = %d, want 4", got)
	}
	// Styled emoji must still measure as 2 cells.
	styled := styleSelected.Render("⚡")
	if got := PopupWidth(styled); got != 2 {
		t.Errorf("PopupWidth(styled ⚡) = %d, want 2", got)
	}
}

// TestComposeOverlayPreservesEmojiAtBoundary verifies the overlay
// splicing path measures both popup and base lines through grapheme-
// aware width so a wide emoji at the popup boundary survives the slice
// without losing or duplicating cells.
func TestComposeOverlayPreservesEmojiAtBoundary(t *testing.T) {
	// 10-wide base, popup carries a single cat (2 cells) starting at col 4.
	base := makeBase(10, 3)
	popup := "🐱"
	got := ComposeOverlay(base, popup, 10, 3, 4, 1, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines; got %d (%q)", len(lines), got)
	}
	plain := stripAnsi(lines[1])
	if Width(plain) != 10 {
		t.Errorf("row 1 width after emoji overlay = %d, want 10 (line=%q)", Width(plain), plain)
	}
	if !strings.Contains(plain, "🐱") {
		t.Errorf("row 1 should contain emoji from popup, got %q", plain)
	}
}
