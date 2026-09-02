package launch

import (
	"log/slog"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
)

// LoadState loads persisted profile overrides and project pins. Profile
// overrides retain their legacy title-only identity; pins use the configured
// project's default (profile, title) identity so duplicate titles across
// profiles remain independent. Stale and duplicate entries are pruned.
func LoadState(sessions []config.Session) (map[int]string, map[int]bool) {
	profileOverrides := make(map[int]string)
	pinned := make(map[int]bool)
	f, err := overrides.Load()
	if err != nil {
		slog.Debug("overrides: load error", "err", err)
		return profileOverrides, pinned
	}
	titleIdx := make(map[string]int, len(sessions))
	projectIdx := make(map[string]int, len(sessions))
	for i, s := range sessions {
		titleIdx[s.Title] = i
		projectIdx[session.SessionKey(s.EffectiveProfile(), s.Title)] = i
	}
	var pruned bool
	var fresh []overrides.Entry
	for _, e := range f.Entries {
		idx, ok := titleIdx[e.ProjectTitle]
		if !ok {
			slog.Debug("overrides: pruning stale entry", "title", e.ProjectTitle)
			pruned = true
			continue
		}
		fresh = append(fresh, e)
		profileOverrides[idx] = e.ProfileName
	}
	freshPins := make([]overrides.PinnedProject, 0, len(f.PinnedProjects))
	seenPins := make(map[string]bool, len(f.PinnedProjects))
	for _, pin := range f.PinnedProjects {
		key := session.SessionKey(pin.ProfileName, pin.ProjectTitle)
		idx, ok := projectIdx[key]
		if !ok || seenPins[key] {
			slog.Debug("overrides: pruning stale or duplicate pin",
				"profile", pin.ProfileName, "title", pin.ProjectTitle)
			pruned = true
			continue
		}
		seenPins[key] = true
		canonical := overrides.PinnedProject{
			ProfileName:  sessions[idx].EffectiveProfile(),
			ProjectTitle: sessions[idx].Title,
		}
		if canonical != pin {
			pruned = true
		}
		freshPins = append(freshPins, canonical)
		pinned[idx] = true
	}
	if pruned {
		f.Version = overrides.CurrentVersion
		f.Entries = fresh
		f.PinnedProjects = freshPins
		if err := overrides.Save(f); err != nil {
			slog.Debug("overrides: save after prune error", "err", err)
		}
	}
	return profileOverrides, pinned
}

// SaveState writes profile overrides and pins together so changing one
// launcher preference cannot discard the other.
func (in PickerInput) SaveState(profileOverrides map[int]string, pinned map[int]bool) error {
	f := overrides.File{Version: overrides.CurrentVersion}
	for idx := range in.Sessions {
		if profile := profileOverrides[idx]; profile != "" {
			f.Entries = append(f.Entries, overrides.Entry{
				ProjectTitle: in.Sessions[idx].Title,
				ProfileName:  profile,
			})
		}
		if pinned[idx] {
			f.PinnedProjects = append(f.PinnedProjects, overrides.PinnedProject{
				ProfileName:  in.Sessions[idx].EffectiveProfile(),
				ProjectTitle: in.Sessions[idx].Title,
			})
		}
	}
	return overrides.Save(f)
}
