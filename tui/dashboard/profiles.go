package dashboard

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/session"
)

// SessionProfiles returns unique effective profiles from the full running-session
// set in case-insensitive alphabetical order. It deliberately reads the
// unfiltered set so text, harness, and state filters do not remove profiles
// from the cycle.
func SessionProfiles(sessions []session.RunningSession) []string {
	profiles := make([]string, 0)
	seen := make(map[string]bool)
	for _, running := range sessions {
		profile := running.EffectiveProfile()
		key := strings.ToLower(profile)
		if profile == "" || seen[key] {
			continue
		}
		seen[key] = true
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i]) < strings.ToLower(profiles[j])
	})
	return profiles
}

// NextProfileFilter advances through the profiles represented by running
// sessions and then wraps to the empty "all profiles" state. A current profile
// that disappeared during refresh restarts the cycle at the first running
// profile.
func NextProfileFilter(sessions []session.RunningSession, current string) string {
	profiles := SessionProfiles(sessions)
	if len(profiles) == 0 {
		return ""
	}
	if current == "" {
		return profiles[0]
	}
	for i, profile := range profiles {
		if !strings.EqualFold(profile, current) {
			continue
		}
		if i+1 < len(profiles) {
			return profiles[i+1]
		}
		return ""
	}
	return profiles[0]
}
