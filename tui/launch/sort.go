package launch

import (
	"slices"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// SortSessions returns a sorted copy: by profile, then by normalized project
// title (emoji/punctuation prefix stripped, case-insensitive).
func SortSessions(sessions []config.Session) []config.Session {
	sorted := slices.Clone(sessions)
	slices.SortStableFunc(sorted, func(a, b config.Session) int {
		if c := strings.Compare(
			session.ProjectSortKey(a.EffectiveProfile()),
			session.ProjectSortKey(b.EffectiveProfile()),
		); c != 0 {
			return c
		}
		return strings.Compare(
			session.ProjectSortKey(a.Title),
			session.ProjectSortKey(b.Title),
		)
	})
	return sorted
}
