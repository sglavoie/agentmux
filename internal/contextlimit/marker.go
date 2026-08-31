// Package contextlimit provides shared text-pattern detection for Claude
// Code's "Context limit reached" recovery message. Used by both the
// indicator (reactive, on hook events) and the daemon (periodic sweep).
package contextlimit

import "strings"

// These bound the search, not the copy: how far up a screen a wrapped banner
// may reach, and how many status lines may sit below it. They are structure and
// stay in Go — a pattern file that could widen the window would change which
// part of a screen is read as the banner, which is not a matter of UI copy.
const (
	maxScreenMarkerLines   = 9
	maxTrailingStatusLines = 1
)

// HasMarker reports whether text contains Claude's context-limit banner. The
// copy it looks for is data (patterns_contextlimit.toml); the conditional
// below is not.
func HasMarker(text string) bool {
	return hasMarker(installedClauses(), text)
}

// hasMarker keeps the shape the copy was tuned against, which no all/any group
// can express: "context limit reached" without a /compact hint is an immediate
// NO, not one failed disjunct of an OR. Claude prints that headline in
// situations the user can carry on from, so falling through to the window-full
// test there would let an unrelated screen mentioning "/compact or /clear" turn
// it into a banner.
func hasMarker(c clauses, text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(stripTerminalEscapes(text)), " "))
	if normalized == "" {
		return false
	}

	if containsAny(normalized, c.limitReachedBanner) {
		return containsAny(normalized, c.compactCommand)
	}

	if !containsAny(normalized, c.windowFullBanner) {
		return false
	}

	return containsAny(normalized, c.compactOrClear) ||
		(containsAny(normalized, c.compactCommand) &&
			containsAny(normalized, c.clearCommand) &&
			containsAny(normalized, c.continueHint))
}

func ScreenShowsMarker(text string) bool {
	return screenShowsMarker(installedClauses(), text)
}

func screenShowsMarker(c clauses, text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	tail := make([]string, 0, maxScreenMarkerLines+maxTrailingStatusLines)
	for i := len(lines) - 1; i >= 0 && len(tail) < maxScreenMarkerLines+maxTrailingStatusLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if lineIsUIChrome(c, line) {
			continue
		}
		tail = append([]string{line}, tail...)
	}
	if hasMarker(c, strings.Join(lastLines(tail, 3), "\n")) {
		return true
	}

	// The wrap-window search is structure: it decides which slice of the tail
	// is offered to hasMarker, and widening it would change what counts as one
	// banner. It stays in Go for the same reason the bounds above do.
	for markerLines := 4; markerLines <= maxScreenMarkerLines; markerLines++ {
		for trailingLines := maxTrailingStatusLines; trailingLines >= 0; trailingLines-- {
			if len(tail) < markerLines+trailingLines {
				continue
			}
			end := len(tail) - trailingLines
			if hasWrappedMarkerWindow(c, tail[end-markerLines:end]) {
				return true
			}
		}
	}

	return false
}

func hasWrappedMarkerWindow(c clauses, lines []string) bool {
	return hasMarker(c, strings.Join(lines, "\n")) && !hasMarker(c, lines[0])
}

func lastLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// isUIChromeLine is the single-line form of lineIsUIChrome against the
// installed patterns. Screen scans resolve their clauses once and call
// lineIsUIChrome directly.
func isUIChromeLine(line string) bool {
	return lineIsUIChrome(installedClauses(), line)
}

// lineIsUIChrome reports whether a single screen line looks like Claude's
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
//   - The first non-whitespace rune is one of `chrome_prompt_rune`
//     (input-box edges or the current prompt indicator).
//   - The line contains `chrome_mode_hint` (current Claude mode hint).
//   - The line starts with `chrome_shortcut_prefix` and contains
//     `chrome_shortcut_hint` (legacy Claude footer hint).
//   - The line contains `chrome_footer_label` (legacy footer; conservative
//     belt-and-braces match).
//
// The named clauses are UI copy and live in patterns_contextlimit.toml, which
// documents what each one is and how it is consumed. The four tests and their
// order are control flow and stay here; so does the box-drawing density test
// below, which is a shape rather than a phrase.
//
// The copy is matched CASE-SENSITIVELY, against the trimmed line as captured:
// the chrome test runs before anything is folded, and folding here would newly
// strip a chat line reading "Model: …". The loader requires the shipped
// phrases to be lowercase, which is how Claude renders all of them.
func lineIsUIChrome(c clauses, line string) bool {
	trimmed := strings.TrimSpace(stripTerminalEscapes(line))
	if trimmed == "" {
		return false
	}

	if isFirstRune(trimmed, c.chromePromptRune) {
		return true
	}

	if containsAny(trimmed, c.chromeModeHint) {
		return true
	}
	if hasAnyPrefix(trimmed, c.chromeShortcutPrefix) && containsAny(trimmed, c.chromeShortcutHint) {
		return true
	}
	if containsAny(trimmed, c.chromeFooterLabel) {
		return true
	}

	// 80% box-drawing runes, counted against non-whitespace runes only. The
	// ratio is a tuned threshold rather than harness copy: a pattern file that
	// could lower it would start stripping chat lines that merely contain a
	// pipe, which is the failure the `incidental pipe rune` fixture pins.
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

// containsAny reports whether text contains any phrase. Phrase lists are
// searched here rather than through patternset.Patterns.Match because this
// package resolves its clauses once per screen and because the chrome lists are
// matched against unfolded text, which Match's raw/lower contract does not
// describe.
func containsAny(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// isFirstRune reports whether text begins with one of the given runes. The
// clause is consumed as a rune set, not a substring search: an entry longer
// than one rune can never match, which patterns_contextlimit.toml says at the
// definition site.
func isFirstRune(text string, runes []string) bool {
	first := firstRune(text)
	if first == 0 {
		return false
	}
	for _, candidate := range runes {
		if candidate == string(first) {
			return true
		}
	}
	return false
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// isBoxDrawingRune is the 14-rune set the density test counts. It stays in Go:
// it is a character class, not harness copy, and it is deliberately NOT the
// same set as chrome_prompt_rune next door (which includes ❯, not a
// box-drawing rune at all). Two further box-frame sets exist in
// internal/screenstate (claude.go's composer/choice edges, codex.go's Codex
// frame set); all three answer different questions and merging them would be a
// behaviour change rather than an extraction.
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
