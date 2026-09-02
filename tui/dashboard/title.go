package dashboard

import "github.com/sglavoie/agentmux/internal/session"

// SessionDisplayTitle is the name a running session shows under in every
// dashboard-derived surface: the row title column, the filter's substring
// match, the detail and preview headers, and the action confirmations that
// name a session back to the user. It prefers the session's effective
// project title and falls back to its Kitty base name.
func SessionDisplayTitle(sess session.RunningSession) string {
	if title := sess.EffectiveProject(); title != "" {
		return title
	}
	return sess.Base
}
