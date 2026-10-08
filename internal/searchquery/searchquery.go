// Package searchquery is what a typed search query means for the transcript
// and plan searches and for the viewers that step between their matches. The
// searches count with it and the transcript and plan viewers step between
// lines holding any of its terms, so a result and the matches inside it cannot
// disagree about the query. It also picks the line a result is previewed by
// (MatchLine), entering a long line just before a deep match, so both searches
// show a match the same way.
//
// A query is a set of terms: whitespace separates words, and a double-quoted
// span is one term however many words it holds. A document matches when it
// contains every term (AND), and its match count is the sum of every term's
// occurrences. A single word therefore behaves exactly as the old whole-query
// substring did, and the old phrase behavior is one pair of quotes away.
//
// Matching is case-insensitive by contract: terms are lowercased here, and
// every haystack a caller passes must already be lowercased — the search
// sidecars are stored that way, which is what lets this package do no folding
// of its own on the hot path.
package searchquery

import (
	"strings"
	"unicode"
)

// Terms is a parsed query. The zero value is the empty query.
type Terms []string

// Parse splits a query into lowercased terms. An unterminated quote runs to
// the end of the query, and empty quotes contribute nothing, so every input
// parses.
func Parse(query string) Terms {
	var terms Terms
	rest := strings.ToLower(query)
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			return terms
		}
		if rest[0] == '"' {
			phrase, after, _ := strings.Cut(rest[1:], `"`)
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				terms = append(terms, phrase)
			}
			rest = after
			continue
		}
		end := strings.IndexFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || r == '"' })
		if end < 0 {
			end = len(rest)
		}
		terms = append(terms, rest[:end])
		rest = rest[end:]
	}
}

// Empty reports whether the query holds no terms.
func (t Terms) Empty() bool { return len(t) == 0 }

// ContainsAll reports whether lower holds every term.
func (t Terms) ContainsAll(lower string) bool {
	if t.Empty() {
		return false
	}
	for _, term := range t {
		if !strings.Contains(lower, term) {
			return false
		}
	}
	return true
}

// ContainsAny reports whether lower holds at least one term. It is the
// per-line question a viewer asks: inside a document that matched as a whole,
// every line that shows any term is a place worth stepping to.
func (t Terms) ContainsAny(lower string) bool {
	for _, term := range t {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// Tally accumulates per-term counts across the pieces of one document — the
// messages of a transcript, say — so the AND is decided over the document as a
// whole rather than within each piece.
type Tally struct {
	terms  Terms
	counts []int
}

// NewTally starts an empty tally for t.
func (t Terms) NewTally() *Tally {
	return &Tally{terms: t, counts: make([]int, len(t))}
}

// Add counts every term's non-overlapping occurrences in lower and returns
// the byte offset of the earliest occurrence of any term, or -1 when none
// occurs.
func (t *Tally) Add(lower string) int {
	first := -1
	for i, term := range t.terms {
		offset := 0
		for {
			pos := strings.Index(lower[offset:], term)
			if pos < 0 {
				break
			}
			absolute := offset + pos
			if first < 0 || absolute < first {
				first = absolute
			}
			t.counts[i]++
			offset = absolute + len(term)
		}
	}
	return first
}

// Total is the summed occurrences when every term occurred at least once,
// and 0 otherwise: a document missing a term is not a match however often the
// others appear.
func (t *Tally) Total() int {
	if len(t.counts) == 0 {
		return 0
	}
	total := 0
	for _, count := range t.counts {
		if count == 0 {
			return 0
		}
		total += count
	}
	return total
}
