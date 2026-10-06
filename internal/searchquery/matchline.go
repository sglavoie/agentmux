package searchquery

import (
	"strings"
	"unicode/utf8"
)

// MatchLeadContext is how much of a line a match preview keeps before the
// match it was taken for. It is short so the match lands early in a narrow
// column, and long enough to say what the match was part of.
const MatchLeadContext = 24

// MatchLine returns the line of text holding the byte offset pos, the match a
// search result is previewed by. A line whose match lies further in than
// MatchLeadContext runes is entered a few words before the match instead, and
// clipped reports that, so the caller can mark the cut once it has truncated
// the line to its own snippet length. A pos outside text yields text whole.
//
// The transcript and plan searches share it so their previews cannot disagree
// about where a match is shown, the way they share what a match is.
func MatchLine(text string, pos int) (line string, clipped bool) {
	if pos < 0 || pos >= len(text) {
		return text, false
	}
	start := strings.LastIndexByte(text[:pos], '\n') + 1
	end := len(text)
	if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
		end = pos + i
	}
	lead := strings.Join(strings.Fields(text[start:pos]), " ")
	if lead != "" && strings.ContainsRune(" \t\r", rune(text[pos-1])) {
		lead += " "
	}
	if utf8.RuneCountInString(lead) <= MatchLeadContext {
		return text[start:end], false
	}
	return leadIn(lead) + text[pos:end], true
}

// leadIn keeps the last MatchLeadContext runes of lead, dropping a leading
// partial word when a word boundary lies inside them.
func leadIn(lead string) string {
	runes := []rune(lead)
	kept := string(runes[len(runes)-MatchLeadContext:])
	if _, after, found := strings.Cut(kept, " "); found {
		return after
	}
	return kept
}
