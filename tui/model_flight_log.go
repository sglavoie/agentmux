package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/flightview"
)

func (m Model) enterFlightLog() (Model, tea.Cmd) {
	m.mode = ModeFlightLog
	m.flightFilters = flightlog.Filters{Period: "week"}
	m.flightTab = 0
	m.flightDetail = false
	m.flightOffset = 0
	m.flightEventKind = "all"
	return m, m.beginFlightRefresh()
}
func (m *Model) beginFlightRefresh() tea.Cmd {
	m.flightSequence++
	m.flightLoading = true
	m.flightError = ""
	var names []string
	if m.cfg != nil {
		for _, a := range m.cfg.Accounts {
			if name := a.EffectiveHarness(); !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	read := m.flightRead
	if read == nil {
		read = commands.ReadFlightLog
	}
	return read(m.flightFilters, names, m.flightSequence)
}
func (m Model) handleFlightLog(msg commands.FlightLogMsg) (tea.Model, tea.Cmd) {
	if msg.Sequence != m.flightSequence {
		return m, nil
	}
	m.flightLoading = false
	if msg.Err != nil {
		m.flightError = msg.Err.Error()
		return m, nil
	}
	key := ""
	if m.flightReport != nil && m.flightCursor < len(m.flightReport.Sessions) {
		key = m.flightReport.Sessions[m.flightCursor].Key
	}
	m.flightReport = &msg.Report
	m.flightCursor = 0
	for i, s := range msg.Report.Sessions {
		if s.Key == key {
			m.flightCursor = i
			break
		}
	}
	return m, nil
}
func (m Model) flightInput() flightview.Input {
	return flightview.Input{Report: m.flightReport, Loading: m.flightLoading, Error: m.flightError, Tab: m.flightTab, Cursor: m.flightCursor, Offset: m.flightOffset, Detail: m.flightDetail, EventKind: m.flightEventKind}
}
func (m Model) handleFlightLogKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		if m.flightDetail {
			m.flightDetail = false
			m.flightOffset = 0
		} else {
			m.mode = ModeList
			m.flightSequence++
			m.flightLoading = false
		}
		return m, nil
	case "1", "2", "3":
		m.flightTab = int(msg.String()[0] - '1')
		m.flightDetail = false
		m.flightOffset = 0
	case "r":
		return m, m.beginFlightRefresh()
	case "p":
		values := []string{"today", "week", "all"}
		m.flightFilters.Period = values[(slices.Index(values, m.flightFilters.Period)+1)%len(values)]
		m.flightOffset = 0
		return m, m.beginFlightRefresh()
	case "f", "h", "a":
		if m.flightReport == nil {
			return m, nil
		}
		var target *string
		var choices []string
		switch msg.String() {
		case "f":
			target = &m.flightFilters.Project
			choices = m.flightReport.Projects
		case "h":
			target = &m.flightFilters.Harness
			choices = m.flightReport.Harnesses
		case "a":
			target = &m.flightFilters.Profile
			choices = m.flightReport.Profiles
		}
		choices = append([]string{""}, choices...)
		*target = choices[(slices.Index(choices, *target)+1)%len(choices)]
		m.flightOffset = 0
		return m, m.beginFlightRefresh()
	case "e":
		values := []string{"all", "question", "key", "stop", "error"}
		m.flightEventKind = values[(slices.Index(values, m.flightEventKind)+1)%len(values)]
		m.flightOffset = 0
	case "enter":
		if m.flightReport != nil && len(m.flightReport.Sessions) > 0 {
			m.flightTab = 1
			m.flightDetail = true
			m.flightOffset = 0
		}
	case "s":
		if m.flightReport == nil || m.flightCursor >= len(m.flightReport.Sessions) {
			return m, nil
		}
		identity := strings.SplitN(m.flightReport.Sessions[m.flightCursor].ConversationID, ":", 2)
		if len(identity) != 2 {
			return m, nil
		}
		updated, cmd := m.enterAllSessionSearch()
		next := updated.(Model)
		next.sessionWantedKey = transcript.IndexKey(identity[0], identity[1])
		return next, cmd
	case "j", "down":
		if m.flightTab == 1 && !m.flightDetail && m.flightReport != nil {
			m.flightCursor = min(max(0, len(m.flightReport.Sessions)-1), m.flightCursor+1)
			m.flightOffset = max(0, m.flightCursor*2-max(1, m.height-12))
		} else {
			m.flightOffset++
		}
	case "k", "up":
		if m.flightTab == 1 && !m.flightDetail {
			m.flightCursor = max(0, m.flightCursor-1)
			m.flightOffset = min(m.flightOffset, m.flightCursor*2)
		} else {
			m.flightOffset = max(0, m.flightOffset-1)
		}
	case "pgdown":
		if m.flightTab == 1 && !m.flightDetail && m.flightReport != nil {
			m.flightCursor = min(max(0, len(m.flightReport.Sessions)-1), m.flightCursor+max(1, (m.height-10)/2))
		} else {
			m.flightOffset += max(1, m.height-10)
		}
	case "pgup":
		if m.flightTab == 1 && !m.flightDetail {
			m.flightCursor = max(0, m.flightCursor-max(1, (m.height-10)/2))
		} else {
			m.flightOffset = max(0, m.flightOffset-max(1, m.height-10))
		}
	case "g":
		m.flightOffset = 0
		if m.flightTab == 1 && !m.flightDetail {
			m.flightCursor = 0
		}
	case "G":
		m.flightOffset = max(0, len(flightview.Lines(m.flightInput(), m.width))-max(1, m.height-6))
		if m.flightTab == 1 && !m.flightDetail && m.flightReport != nil {
			m.flightCursor = max(0, len(m.flightReport.Sessions)-1)
		}
	}
	return m, nil
}
