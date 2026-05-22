package session

import (
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
)

// ProfilePrefix returns the tab title prefix for a profile.
// The initial is taken as the first rune (not the first byte) so multibyte
// profile names like "éscriba" or "中文" yield a valid UTF-8 prefix rather
// than a truncated invalid byte that corrupts the tab title.
func ProfilePrefix(profileName string) string {
	if profileName == "" {
		return ""
	}
	r, _ := utf8.DecodeRuneInString(profileName)
	if r == utf8.RuneError {
		return ""
	}
	return strings.ToUpper(string(r)) + ":"
}

// AccountPrefix is kept as a compatibility alias while the codebase moves from
// account terminology to profile terminology.
func AccountPrefix(accountName string) string {
	return ProfilePrefix(accountName)
}

// FormatTitle returns the managed Kitty tab base title for a session.
// Palette icons and the star marker are layered on top of this base title.
func FormatTitle(sess config.Session) string {
	return sess.Title
}
