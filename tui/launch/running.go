package launch

import "github.com/sglavoie/agentmux/internal/session"

// BuildRunning returns logical session keys and their current instance counts,
// which the launcher renders as the "(running)" / "(N running)" row suffix.
func BuildRunning(sessions []session.RunningSession) map[string]int {
	m := make(map[string]int, len(sessions))
	for _, s := range sessions {
		m[RunningSessionKey(s)]++
	}
	return m
}

// RunningSessionKey is the logical identity a live session is counted under.
// It prefers the session's own key when Kitty reported one, and otherwise
// derives it from the effective profile and project.
func RunningSessionKey(sess session.RunningSession) string {
	if sess.Key != "" {
		return sess.Key
	}
	return session.SessionKey(sess.EffectiveProfile(), sess.EffectiveProject())
}
