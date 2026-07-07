// Package tui exposes the centralized TUI display-width policy.
//
// Every visible-cell calculation in the TUI should route through the
// helpers in this file. Direct production calls to
// runewidth.StringWidth, runewidth.Truncate, or lipgloss.Width outside
// the helper are discouraged so width, truncation, padding, and centering
// stay consistent across surfaces.
package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// titleIconCellWidth is the fixed cell width reserved for a leading
// pictographic grapheme in titles and icon columns. Two cells matches
// Kitty's rendering of emoji icons in our terminal layout, regardless of
// whether the glyph carries a VS-16 (FE0F) variation selector.
const titleIconCellWidth = 2

// displayWidth returns the visual cell width of s. The string is iterated
// grapheme cluster by cluster via uniseg so ZWJ sequences, combining
// marks, and variation selectors fold into the preceding base grapheme
// instead of adding extra cells. Per-cluster width mostly comes from
// go-runewidth, with targeted overrides for glyphs that Kitty renders wider
// than the library reports.
//
// The input must be ANSI-free. Use displayStyledWidth for measurement on
// already-styled output containing CSI escape sequences.
func displayWidth(s string) int {
	if s == "" {
		return 0
	}
	total := 0
	state := -1
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		total += displayGraphemeWidth(cluster)
	}
	return total
}

// displayStyledWidth strips ANSI escape sequences from s and returns the
// displayWidth of the visible content.
func displayStyledWidth(s string) int {
	return displayWidth(ansi.Strip(s))
}

// displayStyledTruncate returns ANSI-styled s truncated to at most max display
// cells, appending tail when truncation actually happens. It keeps grapheme
// clusters and ANSI escape sequences whole, and continues copying escape
// sequences after the visible cut so trailing resets still close active styles.
func displayStyledTruncate(s string, max int, tail string) string {
	if max <= 0 {
		return ""
	}
	if displayStyledWidth(s) <= max {
		return s
	}
	tailW := displayStyledWidth(tail)
	if tailW >= max {
		return displayStyledTruncateToBudget(tail, max, "")
	}
	return displayStyledTruncateToBudget(s, max-tailW, tail)
}

func displayStyledTruncateToBudget(s string, budget int, tail string) string {
	var b strings.Builder
	used := 0
	truncated := false
	state := -1
	rest := s
	for len(rest) > 0 {
		if n := displayANSISequenceLen(rest); n > 0 {
			b.WriteString(rest[:n])
			rest = rest[n:]
			state = -1
			continue
		}

		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if truncated {
			continue
		}
		w := displayGraphemeWidth(cluster)
		if used+w > budget {
			b.WriteString(tail)
			truncated = true
			continue
		}
		b.WriteString(cluster)
		used += w
	}
	return b.String()
}

func displayANSISequenceLen(s string) int {
	if len(s) == 0 || s[0] != '\x1b' {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		return displayStringTerminatedSequenceLen(s)
	case 'P', '_', '^', 'X':
		return displayStringTerminatedSequenceLen(s)
	default:
		return 2
	}
}

func displayStringTerminatedSequenceLen(s string) int {
	for i := 2; i < len(s); i++ {
		if s[i] == '\a' {
			return i + 1
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
	}
	return len(s)
}

// displayTruncate returns s truncated to at most max display cells,
// appending tail when truncation actually happens. Grapheme clusters are
// kept whole. When max <= 0 the result is empty. When tail itself is
// wider than max, a runewidth-truncated copy of tail is returned so the
// caller still gets a sensible visible marker.
func displayTruncate(s string, max int, tail string) string {
	if max <= 0 {
		return ""
	}
	if displayWidth(s) <= max {
		return s
	}
	tailW := displayWidth(tail)
	if tailW >= max {
		return runewidth.Truncate(tail, max, "")
	}
	budget := max - tailW
	var b strings.Builder
	used := 0
	state := -1
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		w := displayGraphemeWidth(cluster)
		if used+w > budget {
			break
		}
		b.WriteString(cluster)
		used += w
	}
	b.WriteString(tail)
	return b.String()
}

// displayTruncateLeft returns s truncated to at most max display cells by
// dropping leading graphemes, prepending prefix when truncation happens.
// Useful for path columns where the rightmost segment (filename) matters
// more than the root.
func displayTruncateLeft(s string, max int, prefix string) string {
	if max <= 0 {
		return ""
	}
	if displayWidth(s) <= max {
		return s
	}
	prefixW := displayWidth(prefix)
	if prefixW >= max {
		return runewidth.Truncate(prefix, max, "")
	}
	budget := max - prefixW
	clusters := graphemeClusters(s)
	for len(clusters) > 0 && totalGraphemeWidth(clusters) > budget {
		clusters = clusters[1:]
	}
	return prefix + strings.Join(clusters, "")
}

// displayPadRight returns s padded with trailing spaces to exactly width
// cells. s is returned unchanged when its displayWidth is already at or
// above width.
func displayPadRight(s string, width int) string {
	w := displayWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// displayPadLeft mirrors displayPadRight, prepending spaces instead.
func displayPadLeft(s string, width int) string {
	w := displayWidth(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

// displayCenter pads s with leading spaces so the visible content sits
// centered inside width cells. The measurement strips ANSI sequences so
// Lipgloss-styled output is centered by its rendered width, not its byte
// length. Trailing space is omitted because callers typically continue
// rendering inside a width-bounded composition.
func displayCenter(s string, width int) string {
	w := displayStyledWidth(s)
	if w >= width {
		return s
	}
	pad := (width - w) / 2
	return strings.Repeat(" ", pad) + s
}

// displayWrapHard splits s into display-width-bounded rows without splitting
// grapheme clusters. It does not perform word wrapping; callers can run this
// after a word-wrapper to repair rows whose terminal width policy differs from
// the wrapper's internal width table.
func displayWrapHard(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	if width <= 0 || displayWidth(s) <= width {
		return []string{s}
	}
	var lines []string
	var b strings.Builder
	used := 0
	state := -1
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		w := displayGraphemeWidth(cluster)
		if b.Len() > 0 && used+w > width {
			lines = append(lines, b.String())
			b.Reset()
			used = 0
		}
		b.WriteString(cluster)
		used += w
		if used >= width {
			lines = append(lines, b.String())
			b.Reset()
			used = 0
		}
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}

// displayStyledWrapHard splits ANSI-styled s into display-width-bounded rows
// without splitting grapheme clusters or ANSI escape sequences. It is the
// style-aware counterpart to displayWrapHard: escape sequences are copied
// whole and cost zero display width, so each returned row measures at most
// width visible cells. The line's leading style (the run of escape sequences
// before the first visible grapheme) is re-emitted at the start of every
// continuation row and a reset is appended when a row wraps, so a hard split
// through a `<SGR>text<reset>` line keeps its color on every row.
//
// An empty string yields a single empty row so blank separator lines survive
// wrapping. When width <= 0 or s already fits, s is returned as a single row.
func displayStyledWrapHard(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	if width <= 0 || displayStyledWidth(s) <= width {
		return []string{s}
	}
	const reset = "\x1b[0m"
	prefix := leadingANSISequences(s)

	var lines []string
	var b strings.Builder
	used := 0
	state := -1
	rest := s
	for len(rest) > 0 {
		if n := displayANSISequenceLen(rest); n > 0 {
			b.WriteString(rest[:n])
			rest = rest[n:]
			state = -1
			continue
		}

		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		w := displayGraphemeWidth(cluster)
		if used > 0 && used+w > width {
			b.WriteString(reset)
			lines = append(lines, b.String())
			b.Reset()
			b.WriteString(prefix)
			used = 0
		}
		b.WriteString(cluster)
		used += w
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}

// leadingANSISequences returns the run of ANSI escape sequences at the start
// of s, before the first visible grapheme cluster. displayStyledWrapHard
// re-emits this so each wrapped continuation row re-opens the line's style.
func leadingANSISequences(s string) string {
	var b strings.Builder
	rest := s
	for len(rest) > 0 {
		n := displayANSISequenceLen(rest)
		if n == 0 {
			break
		}
		b.WriteString(rest[:n])
		rest = rest[n:]
	}
	return b.String()
}

// titleIconCell returns the fixed cell width reserved for a leading
// title-icon column.
func titleIconCell() int { return titleIconCellWidth }

// displayPadIconCell pads icon so it occupies exactly titleIconCellWidth
// (2) cells. Icons whose displayWidth already meets or exceeds 2 are
// returned unchanged.
func displayPadIconCell(icon string) string {
	w := displayWidth(icon)
	if w >= titleIconCellWidth {
		return icon
	}
	return icon + strings.Repeat(" ", titleIconCellWidth-w)
}

// displayIsTitleIcon reports whether title begins with a pictographic
// grapheme that should be rendered as a fixed-width title-icon column.
// Plain ASCII text, digits, narrow markers like '•', and CJK characters
// return false; emoji glyphs (including VS-16 and ZWJ variants) return
// true.
func displayIsTitleIcon(title string) bool {
	if title == "" {
		return false
	}
	cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(title, -1)
	return isPictographicCluster(cluster)
}

// displayRenderTitleIcon pads a leading pictographic grapheme to the
// fixed title-icon column and returns the result. When title does not
// begin with a pictographic grapheme it is returned unchanged so callers
// can layer this on every title without inspecting the content first.
func displayRenderTitleIcon(title string) string {
	if title == "" {
		return title
	}
	cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(title, -1)
	if !isPictographicCluster(cluster) {
		return title
	}
	return displayPadIconCell(cluster) + rest
}

// graphemeClusters splits s into a slice of grapheme cluster strings.
func graphemeClusters(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	state := -1
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		out = append(out, cluster)
	}
	return out
}

func totalGraphemeWidth(clusters []string) int {
	total := 0
	for _, c := range clusters {
		total += displayGraphemeWidth(c)
	}
	return total
}

func displayGraphemeWidth(cluster string) int {
	switch cluster {
	case "\U0001f5e3", "\U0001f5e3\ufe0f":
		// Kitty renders speaking-head project icons as a full emoji cell pair,
		// while go-runewidth reports this grapheme as one cell.
		return titleIconCellWidth
	default:
		return runewidth.StringWidth(cluster)
	}
}

// isPictographicCluster reports whether cluster is an emoji-like
// grapheme cluster. The detection accepts any cluster that:
//   - contains the FE0F variation selector (e.g. ⚠️, 🗣️)
//   - contains a ZWJ joiner (e.g. 🏳️‍🌈)
//   - starts with a known pictographic base rune (e.g. ⚠, 🗣, ⚡, 🐱)
//
// Plain wide characters such as CJK ideographs do not match — only
// pictographic ranges count as title-icon glyphs so leading CJK letters
// remain treated as title body.
func isPictographicCluster(cluster string) bool {
	if cluster == "" {
		return false
	}
	for _, r := range cluster {
		if r == 0xFE0F || r == 0x200D {
			return true
		}
		if isPictographicRune(r) {
			return true
		}
	}
	return false
}

// isPictographicRune reports whether r is in a range we treat as
// pictographic for leading title-icon detection. The set covers the
// emoji blocks we actually use as icons; it is intentionally narrower
// than the full Unicode Extended_Pictographic property so we don't have
// to ship a property table for a UI heuristic.
func isPictographicRune(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x1F000 && r <= 0x1F2FF:
		return true
	}
	return false
}
