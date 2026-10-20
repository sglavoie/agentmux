package layout

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// List states. Every view says "still loading", "nothing matches", and
// "nothing here yet" in the same shape, so an operator learns it once:
//
//	loading worktrees…
//	no worktrees match — esc clears the filter
//	no managed worktrees — w in Past Sessions forks a conversation into one
//
// The text after the dash is always the way out: the key that clears what is
// narrowing the list, or the next step that would put something in it. Inside
// a list body the line is a dim placeholder in parentheses (Placeholder), so
// it never reads as a row; where it stands alone — a hero title, a status bar,
// a palette's only row — it is a sentence (Sentence) with no parentheses.

// ClearFilterHint is the way out of a filtered list whose filter esc clears.
const ClearFilterHint = "esc clears the filter"

// LoadingText says a list is still being read: "loading <things>…", or
// "loading…" when the header already names what is loading.
func LoadingText(things string) string {
	if things = strings.TrimSpace(things); things == "" {
		return "loading…"
	}
	return "loading " + things + "…"
}

// NoMatchText says a filter left nothing: "no <things> match — <hint>". The
// hint names the key that clears what is narrowing the list; an empty hint
// leaves the sentence bare, for a caller that shows the way out elsewhere.
func NoMatchText(things, hint string) string {
	return WithHint("no "+things+" match", hint)
}

// EmptyText says there is nothing to list at all: "no <things> — <next>",
// where next is the step that would put something in it.
func EmptyText(things, next string) string {
	return WithHint("no "+things, next)
}

// LoadFailedText says a list is empty because it could not be read, which is
// not the same as there being nothing to list: "could not load <things> —
// <retry>", where retry names how to try again.
func LoadFailedText(things, retry string) string {
	return WithHint("could not load "+things, retry)
}

// WithHint appends the way out to a state that needs its own wording:
// "<text> — <hint>", or text alone when there is no hint.
func WithHint(text, hint string) string {
	if hint = strings.TrimSpace(hint); hint == "" {
		return text
	}
	return text + " — " + hint
}

// SearchEditHint is the way out of a list narrowed by an inline "/" search:
// backspace while the search line has the keyboard, "/" to take it back after
// esc or enter left the query in place.
func SearchEditHint(searching bool) string {
	if searching {
		return "backspace edits the search"
	}
	return "/ edits the search"
}

// Placeholder wraps a state for a list body: "(loading worktrees…)".
func Placeholder(text string) string {
	return "(" + text + ")"
}

// Sentence capitalizes a state that stands on its own: "Loading sessions…".
func Sentence(text string) string {
	r, size := utf8.DecodeRuneInString(text)
	if r == utf8.RuneError {
		return text
	}
	return string(unicode.ToUpper(r)) + text[size:]
}
