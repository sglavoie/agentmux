package layout

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// styleSelected and styleDim mirror the two root TUI styles these tests
// originally rendered through. Layout owns no theme, so the tests carry the
// styles they need to prove that ANSI attributes never count toward cell
// width or survive a cut.
var (
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleDim      = lipgloss.NewStyle().Faint(true)
)

// TestDisplayWidthBasic exercises the centralized cell-width helper on
// the glyphs the TUI actually renders: plain ASCII, CJK, narrow markers
// like '•', the full emoji icon set (with and without VS-16), ZWJ
// sequences, and decomposed combining marks.
func TestDisplayWidthBasic(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"digits", "12345", 5},
		{"mixed-ascii", "hi 中", 5},
		{"cjk-single", "中", 2},
		{"cjk-multi", "中文", 4},
		{"bullet", "•", 1},
		{"bullet-with-text", "• item", 6},
		{"combining-acute", "é", 1},
		{"combining-tilde", "ñ", 1},
		{"emoji-cat", "🐱", 2},
		{"emoji-brain", "🧠", 2},
		{"emoji-speak", "🗣", 2},
		{"emoji-speak-vs", "🗣️", 2},
		{"emoji-warn", "⚠", 1},
		{"emoji-warn-vs", "⚠️", 2},
		{"emoji-bolt", "⚡", 2},
		{"emoji-rainbow-flag", "\U0001f3f3️‍\U0001f308", 1},
		{"emoji-then-text", "🐱 cat", 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Width(c.in); got != c.want {
				t.Errorf("Width(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestStyledDisplayWidthStripsAnsi covers the styled-safe measurement
// path: callers pass already-rendered Lipgloss output and the helper
// must report the visible width, not the byte-count including escapes.
func TestStyledDisplayWidthStripsAnsi(t *testing.T) {
	plain := "hello"
	styled := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff0000")).Render(plain)
	if styled == plain {
		t.Fatalf("lipgloss did not produce ANSI-styled output for %q", plain)
	}
	if got, want := StyledWidth(styled), len(plain); got != want {
		t.Errorf("StyledWidth(styled) = %d, want %d (styled=%q)", got, want, styled)
	}
	if got := StyledWidth("plain"); got != 5 {
		t.Errorf("StyledWidth(plain) = %d, want 5", got)
	}
	// Embedded raw CSI sequence around an emoji — visible width must
	// match the underlying glyph.
	raw := "\x1b[31m⚡\x1b[0m"
	if got := StyledWidth(raw); got != 2 {
		t.Errorf("StyledWidth(raw ANSI ⚡) = %d, want 2", got)
	}
}

func TestDisplayStyledTruncatePreservesAnsi(t *testing.T) {
	styled := styleDim.Render("label: abcdef")
	got := StyledTruncate(styled, 8, "…")
	if !utf8.ValidString(got) {
		t.Fatalf("StyledTruncate produced invalid UTF-8: %q", got)
	}
	if plain := stripAnsi(got); plain != "label: …" {
		t.Fatalf("StyledTruncate visible text = %q, want %q (raw=%q)", plain, "label: …", got)
	}
	if w := StyledWidth(got); w != 8 {
		t.Fatalf("StyledTruncate visible width = %d, want 8 (raw=%q)", w, got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("StyledTruncate should preserve ANSI styling, got %q", got)
	}
	if reset := lastANSIResetIndex(got); reset < strings.LastIndex(got, "…") {
		t.Fatalf("StyledTruncate should preserve a reset after the tail, got %q", got)
	}

	untruncated := StyledTruncate(styled, 80, "…")
	if untruncated != styled {
		t.Fatalf("StyledTruncate under budget = %q, want original %q", untruncated, styled)
	}
}

func lastANSIResetIndex(s string) int {
	reset := strings.LastIndex(s, "\x1b[m")
	if alt := strings.LastIndex(s, "\x1b[0m"); alt > reset {
		reset = alt
	}
	return reset
}

// TestDisplayTruncateKeepsClusters locks in that truncation never splits
// a grapheme cluster. The rainbow flag is the canonical case: even
// though it occupies a single cell, it is a 4-codepoint cluster that
// must not be partially emitted.
func TestDisplayTruncateKeepsClusters(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		tail string
		want string
	}{
		{"under-budget", "abc", 5, "…", "abc"},
		{"exact-budget", "abcde", 5, "…", "abcde"},
		{"trim-ascii", "abcdef", 5, "…", "abcd…"},
		{"empty-input", "", 5, "…", ""},
		{"zero-max", "abc", 0, "…", ""},
		{"tail-bigger-than-max", "abcdef", 1, "…", "…"},
		// Rainbow flag is 1 cell wide, single cluster — should fit at
		// max=1 with no tail required.
		{"rainbow-flag-fits", "\U0001f3f3️‍\U0001f308", 1, "…", "\U0001f3f3️‍\U0001f308"},
		// Two emoji of width 2 + cat: trimming at 3 must keep the first
		// 2-cell emoji whole, drop the second, leave room for the tail.
		{"emoji-cluster-boundary", "🐱🧠cat", 4, "…", "🐱…"},
		// CJK clusters never split mid-character.
		{"cjk-trim", "中文测试", 5, "…", "中文…"},
		// Combining mark must stay attached to its base.
		{"combining-keeps-mark", "éf", 1, "…", "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(c.in, c.max, c.tail)
			if got != c.want {
				t.Errorf("Truncate(%q, %d, %q) = %q, want %q", c.in, c.max, c.tail, got, c.want)
			}
			if w := Width(got); c.max > 0 && w > c.max {
				t.Errorf("Truncate(%q, %d, %q) result %q exceeds max width: %d", c.in, c.max, c.tail, got, w)
			}
		})
	}
}

// TestDisplayTruncateLeftBehavior covers path-style left truncation: we
// keep the rightmost graphemes intact and prepend an ellipsis when
// content is dropped.
func TestDisplayTruncateLeftBehavior(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		max    int
		prefix string
		want   string
	}{
		{"under-budget", "abc", 5, "…", "abc"},
		{"trim", "/long/path/here", 6, "…", "…/here"},
		{"zero-max", "abc", 0, "…", ""},
		{"prefix-bigger-than-max", "abcdef", 1, "…", "…"},
		// CJK clusters stay atomic.
		{"cjk-trim-left", "中文测试", 5, "…", "…测试"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TruncateLeft(c.in, c.max, c.prefix)
			if got != c.want {
				t.Errorf("TruncateLeft(%q, %d, %q) = %q, want %q", c.in, c.max, c.prefix, got, c.want)
			}
		})
	}
}

// TestDisplayPadWidthHelpers covers the right/left/center padders so the
// helpers can serve as drop-in replacements for the ad-hoc width
// arithmetic embedded throughout list/picker render paths.
func TestDisplayPadWidthHelpers(t *testing.T) {
	t.Run("pad-right-ascii", func(t *testing.T) {
		got := PadRight("hi", 5)
		if got != "hi   " {
			t.Errorf("PadRight ascii = %q, want %q", got, "hi   ")
		}
		if w := Width(got); w != 5 {
			t.Errorf("PadRight resulting width = %d, want 5", w)
		}
	})
	t.Run("pad-right-emoji", func(t *testing.T) {
		// 🐱 is 2 cells; padding to 4 should add exactly 2 spaces.
		got := PadRight("🐱", 4)
		if got != "🐱  " {
			t.Errorf("PadRight emoji = %q, want %q", got, "🐱  ")
		}
	})
	t.Run("pad-right-already-wide", func(t *testing.T) {
		got := PadRight("hello", 3)
		if got != "hello" {
			t.Errorf("PadRight returned %q for already-wide input, want unchanged", got)
		}
	})
	t.Run("pad-left-ascii", func(t *testing.T) {
		got := PadLeft("hi", 5)
		if got != "   hi" {
			t.Errorf("PadLeft ascii = %q, want %q", got, "   hi")
		}
	})
	t.Run("pad-left-cjk", func(t *testing.T) {
		// "中" is 2 cells; padding to 4 should add exactly 2 spaces.
		got := PadLeft("中", 4)
		if got != "  中" {
			t.Errorf("PadLeft cjk = %q, want %q", got, "  中")
		}
	})
}

// TestDisplayCenterWidth pads strings inside a fixed column. Centering
// must measure styled output by its visible width, not its byte length.
func TestDisplayCenterWidth(t *testing.T) {
	t.Run("centers-ascii", func(t *testing.T) {
		got := Center("hi", 6)
		if got != "  hi" {
			t.Errorf("Center ascii = %q, want %q", got, "  hi")
		}
	})
	t.Run("centers-styled", func(t *testing.T) {
		styled := lipgloss.NewStyle().Bold(true).Render("hi")
		got := Center(styled, 6)
		if !strings.HasPrefix(got, "  ") {
			t.Errorf("Center styled = %q, expected 2 leading spaces", got)
		}
		if !strings.HasSuffix(got, styled) {
			t.Errorf("Center styled = %q, expected to end with styled content", got)
		}
	})
	t.Run("no-pad-when-saturated", func(t *testing.T) {
		got := Center("hello", 3)
		if got != "hello" {
			t.Errorf("Center saturated = %q, want unchanged", got)
		}
	})
}

// TestTitleCellIconPadding locks in the leading-emoji policy: an
// emoji-like grapheme — including selected VS-16 / ZWJ variants — occupies
// exactly 2 cells so titles starting with an icon line up regardless of which
// glyph is used.
func TestTitleCellIconPadding(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"cat-already-wide", "🐱", "🐱"},
		{"brain-already-wide", "🧠", "🧠"},
		{"bolt-already-wide", "⚡", "⚡"},
		{"warn-needs-pad", "⚠", "⚠ "},
		{"warn-vs-already-wide", "⚠️", "⚠️"},
		{"speak-already-wide", "🗣", "🗣"},
		{"speak-vs-already-wide", "🗣️", "🗣️"},
		{"rainbow-needs-pad", "\U0001f3f3️‍\U0001f308", "\U0001f3f3️‍\U0001f308 "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PadIconCell(c.in)
			if got != c.want {
				t.Errorf("PadIconCell(%q) = %q, want %q", c.in, got, c.want)
			}
			if w := Width(got); w != iconCell() {
				t.Errorf("PadIconCell(%q) cell width = %d, want %d", c.in, w, iconCell())
			}
		})
	}
}

// TestTitleCellIsLeadingIcon verifies the classifier used to decide
// whether a title should reserve the fixed 2-cell icon column. Bullets,
// CJK ideographs, and plain ASCII must not be promoted to icons; emoji
// must.
func TestTitleCellIsLeadingIcon(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"ascii", "agentmux", false},
		{"bullet", "• item", false},
		{"cjk", "中文项目", false},
		{"combining", "éproject", false},
		{"emoji-cat", "🐱 cat-tool", true},
		{"emoji-brain", "🧠 brain", true},
		{"emoji-speak", "🗣 voice", true},
		{"emoji-speak-vs", "🗣️ voice", true},
		{"emoji-warn", "⚠ careful", true},
		{"emoji-warn-vs", "⚠️ careful", true},
		{"emoji-bolt", "⚡power", true},
		{"emoji-rainbow", "\U0001f3f3️‍\U0001f308 pride", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isTitleIcon(c.in); got != c.want {
				t.Errorf("isTitleIcon(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestTitleCellRenderPadsIconColumn verifies that rendering a title
// through the helper makes the leading emoji occupy exactly 2 cells and
// leaves the remainder intact. Titles without a leading emoji must not
// gain any phantom padding.
func TestTitleCellRenderPadsIconColumn(t *testing.T) {
	t.Run("warn-vs-already-wide", func(t *testing.T) {
		const title = "⚠️ api"
		got := RenderTitleIcon(title)
		want := title
		if got != want {
			t.Errorf("RenderTitleIcon(%q) = %q, want %q", title, got, want)
		}
		if w := Width(got); w != Width(want) {
			t.Errorf("rendered width = %d, want %d", w, Width(want))
		}
	})
	t.Run("bolt-unchanged", func(t *testing.T) {
		const title = "⚡cli"
		got := RenderTitleIcon(title)
		if got != title {
			t.Errorf("RenderTitleIcon(%q) = %q, want unchanged", title, got)
		}
	})
	t.Run("speak-unchanged", func(t *testing.T) {
		const title = "🗣commscoach"
		got := RenderTitleIcon(title)
		if got != title {
			t.Errorf("RenderTitleIcon(%q) = %q, want unchanged", title, got)
		}
	})
	t.Run("rainbow-padded", func(t *testing.T) {
		title := "\U0001f3f3️‍\U0001f308 flag"
		got := RenderTitleIcon(title)
		want := "\U0001f3f3️‍\U0001f308  flag"
		if got != want {
			t.Errorf("RenderTitleIcon(rainbow) = %q, want %q", got, want)
		}
	})
	t.Run("ascii-unchanged", func(t *testing.T) {
		const title = "agentmux"
		got := RenderTitleIcon(title)
		if got != title {
			t.Errorf("RenderTitleIcon(%q) = %q, want unchanged", title, got)
		}
	})
	t.Run("bullet-unchanged", func(t *testing.T) {
		const title = "• status"
		got := RenderTitleIcon(title)
		if got != title {
			t.Errorf("RenderTitleIcon(%q) = %q, want unchanged", title, got)
		}
	})
}
