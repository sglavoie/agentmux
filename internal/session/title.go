package session

import (
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

// AccountPrefix returns the tab title prefix for an account.
// e.g., "work" → "W:", "personal" → "P:"
func AccountPrefix(accountName string) string {
	if accountName == "" {
		return ""
	}
	return strings.ToUpper(accountName[:1]) + ":"
}

// FormatTitle returns the Kitty tab title for a session.
// Format: "<AccountInitial>:<session-title>"  e.g., "W:api"
func FormatTitle(sess config.Session) string {
	return AccountPrefix(sess.Account) + sess.Title
}
