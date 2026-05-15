package session

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

// ProfilePrefix returns the tab title prefix for a profile.
func ProfilePrefix(profileName string) string {
	if profileName == "" {
		return ""
	}
	return strings.ToUpper(profileName[:1]) + ":"
}

// AccountPrefix is kept as a compatibility alias while the codebase moves from
// account terminology to profile terminology.
func AccountPrefix(accountName string) string {
	return ProfilePrefix(accountName)
}

// FormatTitle returns the Kitty tab title for a session.
// Format: "<ProfileInitial>:<project-title>".
func FormatTitle(sess config.Session) string {
	profile := sess.Profile
	if profile == "" {
		profile = sess.Account
	}
	return ProfilePrefix(profile) + sess.Title
}
