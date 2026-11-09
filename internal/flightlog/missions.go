package flightlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// Missions are explicit user outcomes, independent of observed agent activity.
type Mission struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Project    string     `json:"project"`
	Status     string     `json:"status"`
	References []string   `json:"references"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ShippedAt  *time.Time `json:"shipped_at,omitempty"`
}

func missions(events []Event) []Mission {
	result := []Mission{}
	indexes := map[string]int{}
	for _, e := range events {
		if e.Kind != "mission" || e.Mission == nil {
			continue
		}
		m := *e.Mission
		if i, ok := indexes[m.ID]; ok {
			result[i] = m
		} else {
			indexes[m.ID] = len(result)
			result = append(result, m)
		}
	}
	return result
}

// SaveMission serializes read/validate/write with recorder and reset operations.
// Empty ID creates a mission; otherwise title, project and references replace
// the existing values. The first shipping date permanently earns its patch.
func (s Store) SaveMission(ctx context.Context, m Mission, now time.Time) (Mission, error) {
	m.Title = strings.TrimSpace(m.Title)
	m.Project = strings.TrimSpace(m.Project)
	validText := func(s string) bool { return !strings.ContainsFunc(s, unicode.IsControl) }
	if m.Title == "" || len(m.Title) > 200 || !validText(m.Title) || m.Project == "" || len(m.Project) > 200 || !validText(m.Project) {
		return m, fmt.Errorf("mission title and project must contain 1–200 bytes without control characters")
	}
	if !slices.Contains([]string{"briefed", "in_progress", "review", "shipped"}, m.Status) {
		return m, fmt.Errorf("invalid mission status")
	}
	if len(m.References) > 50 {
		return m, fmt.Errorf("at most 50 references allowed")
	}
	refs := []string{}
	for _, ref := range m.References {
		ref = strings.TrimSpace(ref)
		if ref == "" || len(ref) > 1000 || !validText(ref) {
			return m, fmt.Errorf("references must contain 1–1000 bytes without control characters")
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	m.References = refs
	err := s.withLock(ctx, func() error {
		if _, err := s.initialize(now); err != nil {
			return err
		}
		snap, err := s.load(ctx)
		if err != nil {
			return err
		}
		if snap.CorruptLines > 0 {
			return fmt.Errorf("mission update refused: unreadable history; data preserved")
		}
		if !snap.Settings.Enabled {
			return fmt.Errorf("enable Flight Log tracking before changing missions")
		}
		if now.Before(snap.TrackingSince) {
			return fmt.Errorf("mission update predates tracking period")
		}
		if m.ID == "" {
			m.ID = diagnostics.NewTraceID()
			m.CreatedAt = now
			m.ShippedAt = nil
		} else {
			found := false
			for _, old := range missions(uniqueEvents(snap.Events, now)) {
				if old.ID == m.ID {
					if !m.UpdatedAt.IsZero() && !m.UpdatedAt.Equal(old.UpdatedAt) {
						return fmt.Errorf("mission changed; refresh and retry")
					}
					m.CreatedAt = old.CreatedAt
					m.ShippedAt = old.ShippedAt
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("mission not found: %s", m.ID)
			}
		}
		m.UpdatedAt = now
		if m.Status == "shipped" && m.ShippedAt == nil {
			at := now
			m.ShippedAt = &at
		}
		snap.Events = append(snap.Events, Event{ID: diagnostics.NewTraceID(), At: now, Kind: "mission", Mission: &m})
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		if err := encoder.Encode(header{SchemaVersion: snap.SchemaVersion, TrackingSince: snap.TrackingSince, Settings: snap.Settings}); err != nil {
			return err
		}
		for _, event := range snap.Events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
		return atomicfile.Write(s.Path, data.Bytes(), 0o600)
	})
	return m, err
}

func missionReport(r *Report, events []Event, now time.Time) {
	r.Missions = []Mission{}
	completed, advanced := 0, 0
	start := periodStart("week", now)
	for _, m := range missions(events) {
		r.Projects = addUnique(r.Projects, m.Project)
		if r.Filters.Project != "" && m.Project != r.Filters.Project {
			continue
		}
		r.Missions = append(r.Missions, m)
		if !m.UpdatedAt.Before(start) {
			advanced++
		}
		if m.ShippedAt != nil && !m.ShippedAt.Before(start) {
			completed++
		}
	}
	slices.Sort(r.Projects)
	r.Recap = append([]string{fmt.Sprintf("Captain's log: %d missions first shipped this week; %d missions created or updated.", completed, advanced)}, r.Recap...)
	for _, m := range missions(events) {
		if m.ShippedAt == nil {
			continue
		}
		r.Achievements = append(r.Achievements, Achievement{ID: "mission:" + m.ID, Title: m.Title, Description: "Mission patch · " + m.Project, Progress: 1, Target: 1, UnlockedAt: m.ShippedAt})
	}
	if len(r.Missions) == 0 {
		r.Recap = append(r.Recap, "Optional challenge: brief one project mission with a concrete outcome.")
	}
}
