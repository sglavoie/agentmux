package config

import (
	"strings"
	"unicode"
)

// ProjectTitleKey is how an operator types a project title: without the
// emoji, bullets, and other symbols titles often lead with, and in lower case.
// "🐱amx", "•amx", and "AMX" all read as "amx". Commands that take a typed
// title compare by it only after an exact title has failed to match, so it
// can never shadow a project actually titled that way.
func ProjectTitleKey(title string) string {
	trimmed := strings.TrimLeftFunc(strings.TrimSpace(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.ToLower(trimmed)
}
