package layout

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestWrapWordsHardBreakIsRuneSafe pins that hard-breaking a word longer than
// the wrap width never slices mid-rune: every emitted line must be valid UTF-8
// and joining the lines must reproduce the original word. This case moved here
// with the wrapper itself; before the original fix, word[:width] cut by byte and
// garbled multi-byte words.
func TestWrapWordsHardBreakIsRuneSafe(t *testing.T) {
	word := "ééééééé" // 7 runes, 14 bytes — forces hard breaks at width 5
	lines := WrapWords(word, 5)
	if len(lines) < 2 {
		t.Fatalf("expected the word to hard-break into multiple lines, got %q", lines)
	}
	for _, line := range lines {
		if !utf8.ValidString(line) {
			t.Errorf("WrapWords emitted invalid UTF-8 line %q", line)
		}
	}
	if got := strings.Join(lines, ""); got != word {
		t.Errorf("WrapWords mangled the word: joined lines %q, want %q", got, word)
	}
}

// TestWrapWordsNeverExceedsWidth is the property every caller relies on to treat
// a wrapped line as fitting its column budget without re-measuring it.
func TestWrapWordsNeverExceedsWidth(t *testing.T) {
	inputs := []string{
		"Focus the Kitty window running this session",
		"~/projects/monorepo-services/authentication-gateway/cmd/server",
		"日本語のセッション名がとても長い場合でも折り返す",
		"a  b   c",
		"https://example.invalid/a/very/long/path/that/never/breaks",
	}
	// Width 1 is excluded deliberately: a 2-cell grapheme has no smaller piece
	// to break into, which WrapWords documents as its one exception.
	for _, width := range []int{2, 3, 8, 20, 68} {
		for _, in := range inputs {
			for _, line := range WrapWords(in, width) {
				if Width(line) > width {
					t.Errorf("WrapWords(%q, %d) produced %q at %d cells", in, width, line, Width(line))
				}
			}
		}
	}
}

// TestWrapWordsEmptyYieldsNoLines keeps "nothing to say" distinguishable from
// "one blank line", which is what lets DetailBlock turn an empty paragraph into
// a deliberate separator.
func TestWrapWordsEmptyYieldsNoLines(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n "} {
		if lines := WrapWords(in, 10); len(lines) != 0 {
			t.Errorf("WrapWords(%q) = %q, want no lines", in, lines)
		}
	}
}

// TestWrapWordsHardBreakTailAcceptsMoreWords pins the greedy re-entry: the short
// tail of a hard-broken word must still collect following words rather than
// being flushed as a line of its own. Without it, every word after a long path
// would start a new line.
func TestWrapWordsHardBreakTailAcceptsMoreWords(t *testing.T) {
	lines := WrapWords("aaaaaaaaaa b", 6)
	want := []string{"aaaaaa", "aaaa b"}
	if len(lines) != len(want) {
		t.Fatalf("WrapWords = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("WrapWords = %q, want %q", lines, want)
		}
	}
}

// TestWrapWordsSingleWideGraphemeIsEmittedWhole pins the documented exception
// rather than leaving it as an untested surprise: at a budget narrower than one
// grapheme the grapheme survives at its own width.
func TestWrapWordsSingleWideGraphemeIsEmittedWhole(t *testing.T) {
	lines := WrapWords("日本", 1)
	want := []string{"日", "本"}
	if len(lines) != len(want) {
		t.Fatalf("WrapWords = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("WrapWords = %q, want %q", lines, want)
		}
	}
}

// TestShortcutRowConcatenatesToExactWidth is the contract the menu's row builder
// depends on: label plus shortcut is exactly the budget, so the row can be
// prefixed by a fixed-width gutter and still land on the box edge.
func TestShortcutRowConcatenatesToExactWidth(t *testing.T) {
	cases := []struct{ label, shortcut string }{
		{"Open session", "⏎"},
		{"Restart with another profile", "P"},
		{"a session whose label is far wider than the row it is drawn in", "x"},
		{"日本語ラベル", "m"},
		{"Close", ""},
	}
	for _, width := range []int{4, 10, 30, 68} {
		for _, tc := range cases {
			label, shortcut := ShortcutRow(tc.label, tc.shortcut, width)
			if got := Width(label) + Width(shortcut); got != width {
				t.Errorf("ShortcutRow(%q, %q, %d) spans %d cells", tc.label, tc.shortcut, width, got)
			}
		}
	}
}

// TestShortcutRowDropsShortcutRatherThanTruncatingIt is the stated policy: half
// a key is a different key, so a row too narrow to hold both loses the shortcut
// whole and gives the width to the label.
func TestShortcutRowDropsShortcutRatherThanTruncatingIt(t *testing.T) {
	label, shortcut := ShortcutRow("Restart", "ctrl+r", 7)
	if shortcut != "" {
		t.Errorf("shortcut = %q, want it dropped entirely at a width that cannot hold it", shortcut)
	}
	if Width(label) != 7 {
		t.Errorf("label spans %d cells, want the full 7 once the shortcut is gone", Width(label))
	}

	label, shortcut = ShortcutRow("Restart", "ctrl+r", 8)
	if shortcut != "ctrl+r" {
		t.Errorf("shortcut = %q, want it whole once it fits with a separating cell", shortcut)
	}
	if got := Width(label) + Width(shortcut); got != 8 {
		t.Errorf("row spans %d cells, want 8", got)
	}
}

// TestShortcutRowZeroWidthIsEmpty guards the degenerate viewport rather than
// leaving it to panic inside a renderer.
func TestShortcutRowZeroWidthIsEmpty(t *testing.T) {
	label, shortcut := ShortcutRow("Open session", "⏎", 0)
	if label != "" || shortcut != "" {
		t.Errorf("ShortcutRow at width 0 = (%q, %q), want both empty", label, shortcut)
	}
}

// TestRuleRowSpansExactWidth covers the labelled, the unlabelled, and the
// too-narrow-to-label forms in one place, because all three are drawn into the
// same column and any of them overflowing is a clipped modal.
func TestRuleRowSpansExactWidth(t *testing.T) {
	for _, width := range []int{1, 5, 6, 12, 68} {
		for _, label := range []string{"", "Session", "a band label far wider than this rule"} {
			if got := Width(RuleRow(label, width)); got != width {
				t.Errorf("RuleRow(%q, %d) spans %d cells", label, width, got)
			}
		}
	}
	if got := RuleRow("", 4); got != "────" {
		t.Errorf("unlabelled rule = %q, want a plain rule", got)
	}
	if got := RuleRow("State", 5); !strings.HasPrefix(got, "─") || strings.Contains(got, "State") {
		t.Errorf("RuleRow at width 5 = %q, want a plain rule below the labelling floor", got)
	}
	if got := RuleRow("State", 12); !strings.Contains(got, " State ") {
		t.Errorf("labelled rule = %q, want the label surrounded by air", got)
	}
}

// TestRuleRowZeroWidthIsEmpty is the same degenerate-viewport guard.
func TestRuleRowZeroWidthIsEmpty(t *testing.T) {
	if got := RuleRow("State", 0); got != "" {
		t.Errorf("RuleRow at width 0 = %q, want empty", got)
	}
}

// TestDetailBlockIsAlwaysExactlyHeight is why the block exists: the explanation
// area must not change height as the selection moves, whatever the selected
// entry has to say.
func TestDetailBlockIsAlwaysExactlyHeight(t *testing.T) {
	inputs := [][]string{
		nil,
		{""},
		{"Short."},
		{"Unavailable: the session is already focused", "Focus the Kitty window running this session"},
		{strings.Repeat("word ", 80)},
	}
	for _, height := range []int{1, 2, 3, 5} {
		for _, paragraphs := range inputs {
			block := DetailBlock(paragraphs, 40, height)
			if len(block) != height {
				t.Errorf("DetailBlock(%v, 40, %d) returned %d lines", paragraphs, height, len(block))
			}
			for _, line := range block {
				if Width(line) > 40 {
					t.Errorf("DetailBlock line %q spans %d cells, want <= 40", line, Width(line))
				}
			}
		}
	}
	if block := DetailBlock([]string{"anything"}, 40, 0); block != nil {
		t.Errorf("DetailBlock at height 0 = %q, want nil", block)
	}
}

// TestDetailBlockMarksClippedContent is the review focus about clipped reasons
// seen from the primitive's side: an explanation that did not fit has to say so
// rather than end mid-sentence.
func TestDetailBlockMarksClippedContent(t *testing.T) {
	block := DetailBlock([]string{strings.Repeat("reason ", 40)}, 20, 2)
	last := block[len(block)-1]
	if !strings.HasSuffix(last, DetailClipMarker) {
		t.Errorf("last line %q does not carry the clip marker %q", last, DetailClipMarker)
	}
	if Width(last) > 20 {
		t.Errorf("clipped line spans %d cells, want <= 20", Width(last))
	}

	block = DetailBlock([]string{"fits"}, 20, 2)
	for _, line := range block {
		if strings.Contains(line, DetailClipMarker) {
			t.Errorf("unclipped block carries a clip marker: %q", block)
		}
	}
}

// TestDetailBlockKeepsEmptyParagraphAsSeparator lets a caller space two
// sentences apart without knowing how either one wrapped.
func TestDetailBlockKeepsEmptyParagraphAsSeparator(t *testing.T) {
	block := DetailBlock([]string{"first", "", "second"}, 20, 3)
	want := []string{"first", "", "second"}
	for i := range want {
		if block[i] != want[i] {
			t.Fatalf("DetailBlock = %q, want %q", block, want)
		}
	}
}

func TestFitLeadAndTailKeepsTheTailVisible(t *testing.T) {
	for _, tc := range []struct {
		lead, tail string
		budget     int
		want       string
	}{
		{"Short title", "Done.", 40, "Short title → Done."},
		{"A title long enough to need cutting down", "Committed the fix.", 40, "A title long en… → Committed the fix."},
		{"Title", "a tail far too long to fit whole in the cell", 30, "Title → a tail far too long t…"},
		{"Title", "ending", 20, "Title"},
		{"Title", "", 40, "Title"},
	} {
		got := FitLeadAndTail(tc.lead, tc.tail, tc.budget)
		if got != tc.want || Width(got) > tc.budget {
			t.Errorf("FitLeadAndTail(%q, %q, %d) = %q (%d cells); want %q", tc.lead, tc.tail, tc.budget, got, Width(got), tc.want)
		}
	}
}
