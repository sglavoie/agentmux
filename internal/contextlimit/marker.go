// Package contextlimit provides shared text-pattern detection for Claude
// Code's "Context limit reached" recovery message. Used by both the
// indicator (reactive, on hook events) and the daemon (periodic sweep).
package contextlimit

import "strings"

const (
	maxScreenMarkerLines   = 9
	maxTrailingStatusLines = 1
)

func HasMarker(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(stripTerminalEscapes(text)), " "))
	if normalized == "" {
		return false
	}

	if strings.Contains(normalized, "context limit reached") {
		return strings.Contains(normalized, "/compact")
	}

	hasWindowFullMessage := strings.Contains(normalized, "context window is full") ||
		strings.Contains(normalized, "context window full")
	if !hasWindowFullMessage {
		return false
	}

	return strings.Contains(normalized, "/compact or /clear") ||
		(strings.Contains(normalized, "/compact") &&
			strings.Contains(normalized, "/clear") &&
			strings.Contains(normalized, "continue"))
}

func ScreenShowsMarker(text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	tail := make([]string, 0, maxScreenMarkerLines+maxTrailingStatusLines)
	for i := len(lines) - 1; i >= 0 && len(tail) < maxScreenMarkerLines+maxTrailingStatusLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if isUIChromeLine(line) {
			continue
		}
		tail = append([]string{line}, tail...)
	}
	if HasMarker(strings.Join(lastLines(tail, 3), "\n")) {
		return true
	}

	for markerLines := 4; markerLines <= maxScreenMarkerLines; markerLines++ {
		for trailingLines := maxTrailingStatusLines; trailingLines >= 0; trailingLines-- {
			if len(tail) < markerLines+trailingLines {
				continue
			}
			end := len(tail) - trailingLines
			if hasWrappedMarkerWindow(tail[end-markerLines : end]) {
				return true
			}
		}
	}

	return false
}

func hasWrappedMarkerWindow(lines []string) bool {
	return HasMarker(strings.Join(lines, "\n")) && !HasMarker(lines[0])
}

func lastLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// isUIChromeLine reports whether a single screen line looks like Claude's
// input-box / status / hint chrome rather than conversation content. The
// helper is intentionally conservative: the cost of a false positive
// (stripping a real chat line) is worse than the cost of a false negative
// (a chrome line we fail to recognise).
//
// Recognised shapes (both the legacy box-drawing UI and the current
// `─` rule + `❯` prompt + `(shift+tab to cycle)` hint):
//
//   - The trimmed line is composed of at least 80% box-drawing runes
//     (`─ │ ═ ║ ┌ ┐ └ ┘ ╭ ╮ ╰ ╯ ┃ ━`), counted against non-whitespace
//     runes only.
//   - The first non-whitespace rune is one of `│`, `╭`, `╰`, or `❯`
//     (input-box edges or the current prompt indicator).
//   - The line contains `(shift+tab to cycle)` (current Claude mode hint).
//   - The line starts with `?` and contains `for shortcuts` (legacy
//     Claude footer hint).
//   - The line contains `model:` (legacy footer; conservative
//     belt-and-braces match).
func isUIChromeLine(line string) bool {
	trimmed := strings.TrimSpace(stripTerminalEscapes(line))
	if trimmed == "" {
		return false
	}

	first := firstRune(trimmed)
	switch first {
	case '│', '╭', '╰', '❯':
		return true
	}

	if strings.Contains(trimmed, "(shift+tab to cycle)") {
		return true
	}
	if strings.HasPrefix(trimmed, "?") && strings.Contains(trimmed, "for shortcuts") {
		return true
	}
	if strings.Contains(trimmed, "model:") {
		return true
	}

	var total, boxes int
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			continue
		}
		total++
		if isBoxDrawingRune(r) {
			boxes++
		}
	}
	return total > 0 && boxes*5 >= total*4
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func isBoxDrawingRune(r rune) bool {
	switch r {
	case '─', '│', '═', '║', '┌', '┐', '└', '┘', '╭', '╮', '╰', '╯', '┃', '━':
		return true
	}
	return false
}

func stripTerminalEscapes(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	for i := 0; i < len(text); i++ {
		if text[i] != 0x1b {
			b.WriteByte(text[i])
			continue
		}
		i++
		if i >= len(text) {
			break
		}

		switch text[i] {
		case '[':
			for i+1 < len(text) {
				i++
				if text[i] >= 0x40 && text[i] <= 0x7e {
					break
				}
			}
		case ']':
			for i+1 < len(text) {
				i++
				if text[i] == 0x07 {
					break
				}
				if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
					i++
					break
				}
			}
		default:
			if text[i] >= 0x40 && text[i] <= 0x5f {
				continue
			}
			b.WriteByte(text[i])
		}
	}

	return b.String()
}
