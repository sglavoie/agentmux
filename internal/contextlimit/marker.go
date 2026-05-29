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

	hasLimitMessage := strings.Contains(normalized, "context limit reached") ||
		strings.Contains(normalized, "context window is full") ||
		strings.Contains(normalized, "context window full")
	if !hasLimitMessage {
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
