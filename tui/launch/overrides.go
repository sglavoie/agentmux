package launch

import (
	"errors"
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
	for _, e := range f.Entries {
		idx, ok := titleIdx[e.ProjectTitle]
		if !ok {
			slog.Debug("overrides: pruning stale entry", "title", e.ProjectTitle)
			pruned = true
			continue
		}
		profileOverrides[idx] = e.ProfileName
	}
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
		pinned[idx] = true
	}
	if pruned {
		// Recheck under the writer lock. Another dashboard may have saved a
		// preference since this read, and pruning its earlier snapshot would
		// discard that newer choice.
		if err := overrides.Update(func(current *overrides.File) error {
			entries := current.Entries[:0]
			for _, e := range current.Entries {
				if _, ok := titleIdx[e.ProjectTitle]; ok {
					entries = append(entries, e)
				}
			}
			current.Entries = entries
			pins := current.PinnedProjects[:0]
			seen := make(map[string]bool, len(current.PinnedProjects))
			for _, pin := range current.PinnedProjects {
				key := session.SessionKey(pin.ProfileName, pin.ProjectTitle)
				idx, ok := projectIdx[key]
				if !ok || seen[key] {
					continue
				}
				seen[key] = true
				pins = append(pins, overrides.PinnedProject{
					ProfileName: sessions[idx].EffectiveProfile(), ProjectTitle: sessions[idx].Title,
				})
			}
			current.PinnedProjects = pins
			current.Version = overrides.CurrentVersion
			return nil
		}); err != nil {
			slog.Debug("overrides: save after prune error", "err", err)
		}
	}
	return profileOverrides, pinned
}

type StateChange struct {
	Index   int
	Pin     *bool
	Profile *string
}

// SaveChanges applies only the choices made in this dashboard, preserving
// preferences saved by another dashboard after this one opened.
func (in PickerInput) SaveChanges(changes []StateChange) error {
	return overrides.Update(func(f *overrides.File) error {
		for _, change := range changes {
			if change.Index < 0 || change.Index >= len(in.Sessions) || (change.Pin == nil) == (change.Profile == nil) {
				return errors.New("invalid launcher state change")
			}
			s := in.Sessions[change.Index]
			if change.Pin != nil {
				pin := overrides.PinnedProject{ProfileName: s.EffectiveProfile(), ProjectTitle: s.Title}
				key := session.SessionKey(pin.ProfileName, pin.ProjectTitle)
				fresh := f.PinnedProjects[:0]
				for _, old := range f.PinnedProjects {
					if session.SessionKey(old.ProfileName, old.ProjectTitle) == key {
						continue
					}
					fresh = append(fresh, old)
				}
				f.PinnedProjects = fresh
				if *change.Pin {
					f.PinnedProjects = append(f.PinnedProjects, pin)
				}
				continue
			}
			fresh := f.Entries[:0]
			for _, old := range f.Entries {
				if old.ProjectTitle != s.Title {
					fresh = append(fresh, old)
				}
			}
			f.Entries = fresh
			if *change.Profile != "" {
				f.Entries = append(f.Entries, overrides.Entry{ProjectTitle: s.Title, ProfileName: *change.Profile})
			}
		}
		return nil
	})
}
