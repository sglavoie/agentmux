// Package contextlimit provides shared text-pattern detection for Claude
// Code's "Context limit reached" recovery message. Used by both the
// indicator (reactive, on hook events) and the daemon (periodic sweep).
package contextlimit

import "strings"

func HasMarker(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
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
	tail := make([]string, 0, 6)
	for i := len(lines) - 1; i >= 0 && len(tail) < 6; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		tail = append([]string{line}, tail...)
	}
	if HasMarker(strings.Join(lastLines(tail, 3), "\n")) {
		return true
	}
	if len(tail) >= 4 &&
		hasWrappedMarkerWindow(tail[len(tail)-4:len(tail)-1]) {
		return true
	}
	if len(tail) >= 4 &&
		hasWrappedMarkerWindow(tail[len(tail)-4:]) {
		return true
	}
	if len(tail) >= 5 &&
		hasWrappedMarkerWindow(tail[len(tail)-5:len(tail)-1]) {
		return true
	}
	if len(tail) >= 5 &&
		hasWrappedMarkerWindow(tail[len(tail)-5:]) {
		return true
	}
	if len(tail) >= 6 &&
		hasWrappedMarkerWindow(tail[len(tail)-6:len(tail)-1]) {
		return true
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
