package launch

import (
	"slices"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
)

// SortSessions returns a sorted copy: by profile group, then by normalized
// project title (emoji/punctuation prefix stripped, case-insensitive), then by
// full profile name.
func SortSessions(sessions []config.Session) []config.Session {
	sorted := slices.Clone(sessions)
	slices.SortStableFunc(sorted, func(a, b config.Session) int {
		if c := strings.Compare(
			profileGroup(a.EffectiveProfile()),
			profileGroup(b.EffectiveProfile()),
		); c != 0 {
			return c
		}
		if c := strings.Compare(
			session.ProjectSortKey(a.Title),
			session.ProjectSortKey(b.Title),
		); c != 0 {
			return c
		}
		return strings.Compare(
			session.ProjectSortKey(a.EffectiveProfile()),
			session.ProjectSortKey(b.EffectiveProfile()),
		)
	})
	return sorted
}

// profileGroup is the profile's normalized name without a trailing harness
// suffix, so "personal-claude" and "personal-codex" list as one "personal"
// group. A profile named only for its harness ("copilot") keeps its name.
func profileGroup(profile string) string {
	key := session.ProjectSortKey(profile)
	for _, h := range []string{harness.Claude, harness.Codex, harness.Copilot} {
		for _, sep := range []string{"-", "_", " "} {
			if prefix, ok := strings.CutSuffix(key, sep+h); ok && prefix != "" {
				return prefix
			}
		}
	}
	return key
}
