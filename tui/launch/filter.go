package launch

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// ApplyFilter returns indices into sessions matching the DSL query. Reuses the
// dashboard filter DSL (@account, !state, substring) so the same shortcut
// behaves the same in both contexts. An empty query returns the full index
// range. Config sessions are matched as synthetic running sessions — !state
// filters won't match anything since config rows have no live state.
func ApplyFilter(sessions []config.Session, query string) []int {
	out := make([]int, 0, len(sessions))
	if strings.TrimSpace(query) == "" {
		for i := range sessions {
			out = append(out, i)
		}
		return out
	}
	f := dashboard.Parse(query)
	for i, cs := range sessions {
		synthetic := session.RunningSession{
			Base:      configSessionDisplayTitle(cs),
			Account:   cs.EffectiveProfile(),
			Profile:   cs.EffectiveProfile(),
			Project:   cs.Title,
			Directory: cs.Directory,
		}
		if f.Match(synthetic, nil, 0) {
			out = append(out, i)
		}
	}
	return out
}

// FilteredIndices applies the launcher DSL and cycled default-profile filter,
// then performs a stable partition so pinned projects float above unpinned
// projects. Because Sessions is already in profile/title order, both partitions
// retain that existing order.
func (in PickerInput) FilteredIndices(query string) []int {
	indices := ApplyFilter(in.Sessions, query)
	indices = FilterIndicesByProfile(in.Sessions, indices, in.ProfileFilter)
	return pinIndices(indices, in.Pinned)
}

// FilterIndicesByProfile drops indices whose configured default profile does
// not match the cycled profile filter. An empty filter is a pass-through.
func FilterIndicesByProfile(sessions []config.Session, indices []int, profile string) []int {
	if profile == "" {
		return indices
	}
	out := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= len(sessions) {
			continue
		}
		if strings.EqualFold(sessions[idx].EffectiveProfile(), profile) {
			out = append(out, idx)
		}
	}
	return out
}

// ProjectProfiles returns unique configured default profiles in the order they
// first appear in sessions. The launcher sorts that slice by profile before
// calling this helper, so unused configured profiles are omitted and the cycle
// follows the visible launcher group order.
func ProjectProfiles(sessions []config.Session) []string {
	profiles := make([]string, 0)
	seen := make(map[string]bool)
	for _, sess := range sessions {
		profile := sess.EffectiveProfile()
		key := strings.ToLower(profile)
		if profile == "" || seen[key] {
			continue
		}
		seen[key] = true
		profiles = append(profiles, profile)
	}
	return profiles
}

// NextProfileFilter advances the cycled default-profile filter one step,
// wrapping through the unfiltered "" state after the last profile.
func NextProfileFilter(sessions []config.Session, current string) string {
	profiles := ProjectProfiles(sessions)
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

func pinIndices(indices []int, pinned map[int]bool) []int {
	if len(indices) == 0 || len(pinned) == 0 {
		return indices
	}
	out := make([]int, 0, len(indices))
	for _, idx := range indices {
		if pinned[idx] {
			out = append(out, idx)
		}
	}
	for _, idx := range indices {
		if !pinned[idx] {
			out = append(out, idx)
		}
	}
	return out
}
