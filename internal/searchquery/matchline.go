package searchquery

import (
	"strings"
	"unicode"
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

// PreviewFit ranks how well a preview position shows a query, so a caller
// choosing between pieces of one document (the messages of a transcript) can
// keep the best one it has seen.
type PreviewFit int

const (
	// FitFirst is the earliest occurrence of any term; no line holds them all.
	FitFirst PreviewFit = iota
	// FitLine is a line holding every term, though one may sit inside a word.
	FitLine
	// FitWords is a line where every term starts a word. Nothing fits better.
	FitWords
)

// PreviewAt picks the offset a result is previewed by, given first, the
// earliest occurrence of any term in lower (as Tally.Add reports it). A
// document matches when it holds every term somewhere, so the line showing
// the first of them is often a line about only one: searching `kitty socket`
// previewed a line that said "Kitty" and nothing about sockets. And a line
// holding every term may hold them inside other words: searching `daemon test`
// previewed the file name daemon_state_acceptance_doc_test.go rather than the
// sentence about the daemon test. So the first line where every term starts a
// word is preferred (FitWords), then the first line holding every term
// (FitLine), each entered at its earliest match; failing both, first is
// returned unchanged (FitFirst), since the terms may well sit in different
// lines or different messages.
func (t Terms) PreviewAt(lower string, first int) (pos int, fit PreviewFit) {
	if first < 0 || t.Empty() {
		return first, FitFirst
	}
	pos, fit = first, FitFirst
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
		offset = end + 1
		line := lower[start:end]
		if !t.ContainsAll(line) {
			continue
		}
		if at, ok := t.wordStarts(line); ok {
			return start + at, FitWords
		}
		if fit == FitFirst {
			pos, fit = start+t.earliest(line), FitLine
		}
	}
	return pos, fit
}

// earliest is the offset of the first occurrence of any term in line.
func (t Terms) earliest(line string) int {
	earliest := len(line)
	for _, term := range t {
		if k := strings.Index(line, term); k >= 0 && k < earliest {
			earliest = k
		}
	}
	return earliest
}

// wordStarts reports whether every term occurs in line at the start of a
// word, and the earliest such occurrence.
func (t Terms) wordStarts(line string) (int, bool) {
	earliest := len(line)
	for _, term := range t {
		k := wordStart(line, term)
		if k < 0 {
			return 0, false
		}
		earliest = min(earliest, k)
	}
	return earliest, true
}

// wordStart returns the offset of the first occurrence of term in line that
// is not preceded by a letter, digit, or underscore, or -1. The underscore
// counts as part of a word so that identifiers like daemon_test do not read as
// the word "test".
func wordStart(line, term string) int {
	for offset := 0; offset < len(line); {
		i := strings.Index(line[offset:], term)
		if i < 0 {
			return -1
		}
		at := offset + i
		before, _ := utf8.DecodeLastRuneInString(line[:at])
		if at == 0 || !isWordRune(before) {
			return at
		}
		offset = at + 1
	}
	return -1
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
