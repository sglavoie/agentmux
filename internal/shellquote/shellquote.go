// Package shellquote quotes words for a POSIX shell: hook commands written
// into harness settings, launch command lines, and the commands a supervising
// agent is told to type.
//
// It is allowlist-based on purpose. A word made only of letters, digits, and
// a few punctuation marks no POSIX shell treats specially is left as it is, so
// ordinary paths and flags stay readable; anything else is wrapped in single
// quotes, inside which a POSIX shell interprets nothing. A denylist has to
// name every metacharacter and quietly fails open on the one it forgot — the
// copy this replaced left a leading `~` to tilde expansion.
package shellquote

import (
	"strings"
	"unicode"
)

// safePunctuation is the punctuation left unquoted. None of it is special to
// a POSIX shell in any position.
const safePunctuation = "@%_+=:,./-"

// Quote returns s as one POSIX shell word. The empty string becomes a pair of
// single quotes, so an empty argument survives word splitting instead of
// vanishing. A single quote inside s is closed, escaped, and reopened:
//
//	it's  ->  'it'\''s'
func Quote(s string) string {
	if isSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join quotes each word and joins them with single spaces. No words gives "".
func Join(words []string) string {
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = Quote(word)
	}
	return strings.Join(quoted, " ")
}

// isSafe reports whether s can stand unquoted. Letters and digits of any
// script are safe: no byte of a multi-byte UTF-8 sequence is a shell
// metacharacter, and leaving them bare keeps hook commands already written
// into operators' settings files byte-identical.
func isSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(safePunctuation, r) {
			continue
		}
		return false
	}
	return true
}
