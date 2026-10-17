package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

func feedbackText(m Model) string {
	lines := []string{"Action feedback", ""}
	selected := selectedFeedback(m)
	for _, t := range m.toasts {
		marker, target := " ", "Action notice"
		if t.NoticeID == selected.NoticeID {
			marker = ">"
		}
		if t.Target.HasSession {
			target = sessionLabel(t.Target)
		}
		lines = append(lines, fmt.Sprintf("%s %s · %s", marker, t.Occurred.Format("Jan 2 15:04:05"), target), "What happened", t.Text)
		if t.NoticeID == selected.NoticeID {
			next := "enter: check health"
			if t.Target.HasSession {
				next = "enter: inspect this session"
			}
			if t.id == undoCloseToastID {
				next = "u: recover last closed session"
			}
			if t.Pending || t.Unconfirmed {
				lines = append(lines, "What remains uncertain", "The operation has no confirmed outcome. Check the session before repeating it.")
			}
			lines = append(lines, "Next step", next+" · c: open configuration")
		}
		lines = append(lines, "")
	}
	if len(m.toasts) == 0 {
		lines = append(lines, "No retained action notices.")
	} else {
		lines = append(lines, "Check the session before repeating an unconfirmed action. Dismissing a notice does not cancel an operation.")
	}
	return strings.Join(lines, "\n")
}

func selectedFeedback(m Model) Toast {
	for _, t := range m.toasts {
		if t.NoticeID == m.feedbackSelected {
			return t
		}
	}
	if len(m.toasts) > 0 {
		return m.toasts[0]
	}
	return Toast{}
}

func (m *Model) moveFeedback(delta int) {
	if len(m.toasts) == 0 {
		return
	}
	selected, index := selectedFeedback(*m), 0
	for i, t := range m.toasts {
		if t.NoticeID == selected.NoticeID {
			index = i
			break
		}
	}
	index = (index + delta + len(m.toasts)) % len(m.toasts)
	m.feedbackSelected = m.toasts[index].NoticeID
	for i, line := range feedbackLines(*m) {
		if strings.HasPrefix(line, "> ") {
			m.feedbackOffset = i
			break
		}
	}
}

func (m Model) inspectFeedback(t Toast) (tea.Model, tea.Cmd) {
	if !t.Target.HasSession {
		m.openHealth(ModeFeedback)
		return m, nil
	}
	// Inspect by the captured identity, never by a reused row number. This
	// navigates to read-only output and does not attach or repeat the operation.
	matches := 0
	for _, row := range m.sessions {
		if feedbackTargetMatches(row, t.Target.Session) {
			matches++
		}
	}
	if matches != 1 {
		return m, m.pushToast(Toast{Severity: toastWarning, Text: "The target session is no longer uniquely listed. Check Past Sessions or health before retrying."})
	}
	m.mode = ModeList
	m.listView = dashboard.ViewUrgency
	cmd := m.applyFilterChange(dashboard.Filter{})
	for i := 0; i < m.items.Len(); i++ {
		if row, ok := m.items.SessionAt(i); ok && feedbackTargetMatches(row, t.Target.Session) {
			m.cursor = i
			if !m.inspectorActive() {
				updated, inspect := m.toggleInspector()
				return updated, tea.Batch(cmd, inspect)
			}
			updated, inspect := m.showInspector()
			return updated, tea.Batch(cmd, inspect)
		}
	}
	m.mode = ModeFeedback
	return m, tea.Batch(cmd, m.pushToast(Toast{Severity: toastWarning, Text: "The target session is no longer listed. Check Past Sessions or health before retrying."}))
}

func feedbackTargetMatches(row, captured session.RunningSession) bool {
	if row.RuntimeSessionID != "" || captured.RuntimeSessionID != "" {
		return row.RuntimeSessionID != "" && row.RuntimeSessionID == captured.RuntimeSessionID
	}
	return sessionactions.SameSession(row, captured)
}

func feedbackLines(m Model) []string {
	var lines []string
	for _, paragraph := range strings.Split(feedbackText(m), "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, layout.WrapWords(paragraph, healthContentWidth(m))...)
	}
	return lines
}

func renderFeedbackBox(m Model) string {
	lines := feedbackLines(m)
	height := max(1, healthVisibleHeight(m))
	offset := clamp(m.feedbackOffset, 0, max(0, len(lines)-height))
	return styleOverlayBox.Width(healthBoxWidth(m)).Render(strings.Join(lines[offset:min(len(lines), offset+height)], "\n"))
}

func (m Model) handleFeedbackKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	page := max(1, healthVisibleHeight(m))
	limit := max(0, len(feedbackLines(m))-page)
	switch msg.String() {
	case "tab", "]":
		m.moveFeedback(1)
	case "shift+tab", "[":
		m.moveFeedback(-1)
	case keyEnter:
		return m.inspectFeedback(selectedFeedback(m))
	case "c":
		return m, commands.EditHere(m.cfg, config.DefaultConfigPath(), func(err error) tea.Msg { return commands.ActionDoneMsg{Err: err} })
	case keyUndoClose:
		return m, m.undoLastCmd()
	case keyEsc, keyQ:
		m.mode = m.feedbackReturnMode
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.feedbackOffset++
	case keyK, keyUp:
		m.feedbackOffset--
	case keyPgDown, keyCtrlF:
		m.feedbackOffset += page
	case keyPgUp, keyCtrlB:
		m.feedbackOffset -= page
	case keyG, keyHome:
		m.feedbackOffset = 0
	case keyGG, keyEnd:
		m.feedbackOffset = limit
	case "y":
		return m, commands.CopyText(selectedFeedback(m).Text)
	case "Y":
		return m, commands.CopyText(feedbackText(m))
	case "h":
		m.openHealth(ModeFeedback)
	case keyD, "D":
		selected := selectedFeedback(m)
		kept := make([]Toast, 0, len(m.toasts))
		for _, t := range m.toasts {
			// Keep active operations and the time-limited close recovery offer.
			if (msg.String() == keyD && t.NoticeID != selected.NoticeID) || (t.Pending && !t.Persistent) || t.id == undoCloseToastID {
				kept = append(kept, t)
			}
		}
		m.toasts, m.feedbackOffset, m.actionErr = kept, 0, nil
	}
	m.feedbackOffset = clamp(m.feedbackOffset, 0, limit)
	return m, nil
}
