package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

func (m model) toggleCompact() (tea.Model, tea.Cmd) {
	if m.quitAfterPreference {
		return m, nil
	}
	m.compact = !m.compact
	m.preferenceChanged = true
	m.preferenceErr = nil
	m.reconcileCaptures()
	save := m.savePreference()
	return m, tea.Batch(save, m.pumpCaptures())
}

// Only one write can be in flight. Rapid toggles coalesce to the newest value,
// preventing a slow older write from overwriting the mode visible on screen.
func (m *model) savePreference() tea.Cmd {
	if m.preferenceSaving || m.deps.preferences == nil {
		return nil
	}
	m.preferenceSaving = true
	m.preferenceSavingValue = m.compact
	value := m.compact
	return m.deps.preferences(&value)
}

func (m model) handlePreferences(msg commands.OverviewPreferencesMsg) (tea.Model, tea.Cmd) {
	if msg.Read {
		// A late startup read must never undo a key the operator already pressed.
		if m.preferenceChanged {
			return m, nil
		}
		m.preferenceErr = msg.Err
		// Without an operator toggle this daemon lifetime, the configured
		// opening mode stands.
		if msg.Err == nil && msg.Explicit {
			m.compact = msg.Compact
			m.reconcileCaptures()
			return m, m.pumpCaptures()
		}
		return m, nil
	}
	if !m.preferenceSaving {
		return m, nil
	}
	m.preferenceSaving = false
	m.preferenceErr = msg.Err
	if m.compact != m.preferenceSavingValue {
		return m, m.savePreference()
	}
	if m.quitAfterPreference {
		return m, tea.Quit
	}
	return m, nil
}
