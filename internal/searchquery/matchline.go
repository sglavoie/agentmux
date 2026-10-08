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

// PreviewAt picks the offset a result is previewed by, given first, the
// earliest occurrence of any term in lower (as Tally.Add reports it). A
// document matches when it holds every term somewhere, so the line showing
// the first of them is often a line about only one: searching `kitty socket`
// previewed a line that said "Kitty" and nothing about sockets. The first line
// holding every term is preferred, entered at its earliest match, and complete
// reports that one was found; failing one, first is returned unchanged, since
// the terms may well sit in different lines or different messages.
func (t Terms) PreviewAt(lower string, first int) (pos int, complete bool) {
	if first < 0 || len(t) < 2 {
		return first, first >= 0
	}
	for offset := first; offset < len(lower); {
		i := strings.Index(lower[offset:], t[0])
		if i < 0 {
			break
		}
		start := strings.LastIndexByte(lower[:offset+i], '\n') + 1
		end := len(lower)
		if j := strings.IndexByte(lower[offset+i:], '\n'); j >= 0 {
			end = offset + i + j
		}
		if line := lower[start:end]; t.ContainsAll(line) {
			earliest := len(line)
			for _, term := range t {
				if k := strings.Index(line, term); k >= 0 && k < earliest {
					earliest = k
				}
			}
			return start + earliest, true
		}
		offset = end + 1
	}
	return first, false
}
