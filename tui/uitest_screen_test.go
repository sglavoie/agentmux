package tui

import (
	"reflect"
	"testing"
)

// TestParseUITestSize covers well-formed size specs, including case-insensitive
// separators and surrounding whitespace.
func TestParseUITestSize(t *testing.T) {
	cases := []struct {
		spec string
		want UITestSize
	}{
		{"80x24", UITestSize{Width: 80, Height: 24}},
		{"120X40", UITestSize{Width: 120, Height: 40}},
		{" 100 x 30 ", UITestSize{Width: 100, Height: 30}},
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			got, err := ParseUITestSize(c.spec)
			if err != nil {
				t.Fatalf("ParseUITestSize(%q): %v", c.spec, err)
			}
			if got != c.want {
				t.Errorf("ParseUITestSize(%q) = %+v, want %+v", c.spec, got, c.want)
			}
		})
	}
}

// TestParseUITestSizeErrors confirms malformed or non-positive sizes fail.
func TestParseUITestSizeErrors(t *testing.T) {
	for _, spec := range []string{"", "80", "80x", "x24", "0x24", "80x-1", "axb", "80x24x2"} {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseUITestSize(spec); err == nil {
				t.Errorf("ParseUITestSize(%q) = nil error, want error", spec)
			}
		})
	}
}

// TestNormalizeUITestScreenStripsAnsiAndMeasures verifies ANSI is stripped, the
// widest line drives Width, Height is the line count, and a trailing newline
// does not create a phantom empty final line.
func TestNormalizeUITestScreenStripsAnsiAndMeasures(t *testing.T) {
	raw := "\x1b[31mred\x1b[0m\nwiderline\n"
	screen := NormalizeUITestScreen(raw, UITestSize{Width: 80, Height: 24})

	if got := screen.Lines; !reflect.DeepEqual(got, []string{"red", "widerline"}) {
		t.Fatalf("lines = %q, want [red widerline]", got)
	}
	if screen.Height != 2 {
		t.Errorf("height = %d, want 2", screen.Height)
	}
	if screen.Width != len("widerline") {
		t.Errorf("width = %d, want %d", screen.Width, len("widerline"))
	}
	if len(screen.Issues) != 0 {
		t.Errorf("expected no layout issues, got %+v", screen.Issues)
	}
}

// TestNormalizeUITestScreenEmojiWidth confirms wide glyphs are measured by
// display cells via the shared displayWidth helper, not by rune count.
func TestNormalizeUITestScreenEmojiWidth(t *testing.T) {
	screen := NormalizeUITestScreen("⚡bolt", UITestSize{Width: 80, Height: 24})
	// ⚡ is 2 cells + "bolt" (4) = 6.
	if screen.Width != 6 {
		t.Errorf("emoji line width = %d, want 6", screen.Width)
	}
}

// TestNormalizeUITestScreenLineWidthOverflow flags a line wider than the
// declared terminal width and records the measured cells and limit.
func TestNormalizeUITestScreenLineWidthOverflow(t *testing.T) {
	screen := NormalizeUITestScreen("okay\ntoo-wide-line", UITestSize{Width: 8, Height: 24})
	var found *UITestLayoutIssue
	for i := range screen.Issues {
		if screen.Issues[i].Type == UITestIssueLineWidthOverflow {
			found = &screen.Issues[i]
		}
	}
	if found == nil {
		t.Fatalf("expected line_width_overflow issue, got %+v", screen.Issues)
	}
	if found.Line != 1 {
		t.Errorf("overflow line = %d, want 1", found.Line)
	}
	if found.Actual != len("too-wide-line") || found.Limit != 8 {
		t.Errorf("overflow actual/limit = %d/%d, want %d/8", found.Actual, found.Limit, len("too-wide-line"))
	}
}

// TestNormalizeUITestScreenHeightOverflow flags a view taller than the
// terminal height.
func TestNormalizeUITestScreenHeightOverflow(t *testing.T) {
	screen := NormalizeUITestScreen("a\nb\nc\nd", UITestSize{Width: 80, Height: 2})
	var found *UITestLayoutIssue
	for i := range screen.Issues {
		if screen.Issues[i].Type == UITestIssueScreenHeightOverflow {
			found = &screen.Issues[i]
		}
	}
	if found == nil {
		t.Fatalf("expected screen_height_overflow issue, got %+v", screen.Issues)
	}
	if found.Actual != 4 || found.Limit != 2 {
		t.Errorf("height overflow actual/limit = %d/%d, want 4/2", found.Actual, found.Limit)
	}
}

// TestNormalizeUITestScreenInvalidUTF8 flags lines that are not valid UTF-8.
func TestNormalizeUITestScreenInvalidUTF8(t *testing.T) {
	raw := "good\n" + string([]byte{0xff, 0xfe})
	screen := NormalizeUITestScreen(raw, UITestSize{Width: 80, Height: 24})
	var found *UITestLayoutIssue
	for i := range screen.Issues {
		if screen.Issues[i].Type == UITestIssueInvalidUTF8 {
			found = &screen.Issues[i]
		}
	}
	if found == nil {
		t.Fatalf("expected invalid_utf8 issue, got %+v", screen.Issues)
	}
	if found.Line != 1 {
		t.Errorf("invalid utf8 line = %d, want 1", found.Line)
	}
}

// TestNormalizeUITestScreenZeroSizeSkipsOverflow confirms an unset (zero)
// terminal size disables overflow reporting while still normalizing content.
func TestNormalizeUITestScreenZeroSizeSkipsOverflow(t *testing.T) {
	screen := NormalizeUITestScreen("a very wide line indeed\nsecond", UITestSize{})
	if len(screen.Issues) != 0 {
		t.Errorf("zero size should not report overflow, got %+v", screen.Issues)
	}
	if screen.Height != 2 {
		t.Errorf("height = %d, want 2", screen.Height)
	}
}
