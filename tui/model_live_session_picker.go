package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

// openLiveSessionPicker raises the shared ambiguity picker. The caller supplies
// the mode Esc lands on, so a detour can never strand the operator in a view
// they did not open.
func (m *Model) openLiveSessionPicker(purpose LivePickerPurpose, returnMode Mode, title string, candidates []actions.WorktreeLiveSession) {
	m.livePickerCandidates = candidates
	m.livePickerPurpose = purpose
	m.livePickerReturnMode = returnMode
	m.livePickerTitle = title
	m.livePickerCursor = 0
	m.livePickerOffset = 0
	m.actionErr = nil
	m.mode = ModeLiveSessionPicker
}

func (m *Model) openWorktreeSessionPicker(candidates []actions.Candidate) {
	var resolved []actions.WorktreeLiveSession
	if m.worktreeActionTarget != nil {
		byID := make(map[string]actions.WorktreeLiveSession, len(m.worktreeActionTarget.LiveSessions))
		for _, candidate := range m.worktreeActionTarget.LiveSessions {
			byID[candidate.ID] = candidate
		}
		for _, candidate := range candidates {
			if live, ok := byID[candidate.ID]; ok {
				resolved = append(resolved, live)
			} else {
				resolved = append(resolved, actions.WorktreeLiveSession{ID: candidate.ID, Project: candidate.DisplayName, Directory: candidate.CanonicalPath})
			}
		}
	}
	m.openLiveSessionPicker(LivePickerWorktreeReopen, ModeWorktrees, worktreeConfirmationTitle(m.worktreeActionTarget), resolved)
}

func (m *Model) openWorktreeProfilePicker() {
	m.configPickerAccounts = append(m.configPickerAccounts[:0], m.cfg.Accounts...)
	m.configPickerPurpose = launch.ConfigPickerWorktreeReopen
	m.configPickerReturnMode = ModeWorktrees
	m.configPickerProjectIdx = -1
	m.configPickerCurrent = ""
	m.configPickerCursor = 0
	m.targetSession = sessionForWorktreePicker(m.worktreeActionTarget)
	if m.worktreeActionTarget != nil {
		m.configPickerCurrent = m.worktreeActionTarget.Profile
		m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.configPickerCurrent)
	}
	m.actionErr = nil
	m.mode = ModeConfigPicker
}

func sessionForWorktreePicker(target *actions.WorktreeView) session.RunningSession {
	if target == nil {
		return session.RunningSession{}
	}
	return session.RunningSession{Base: target.ProjectLabel, Project: target.ProjectLabel, Profile: target.Profile, Harness: target.Harness, Directory: target.Path}
}

func (m Model) handleLiveSessionPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		chosen := m.livePickerPurpose
		m.mode = m.livePickerReturnMode
		m.livePickerCandidates = nil
		m.livePickerTitle = ""
		if chosen == LivePickerWorktreeReopen {
			m.worktreeActionTarget = nil
		}
		return m, nil
	case keyJ, keyDown:
		m.livePickerCursor = clamp(m.livePickerCursor+1, 0, len(m.livePickerCandidates)-1)
	case keyK, keyUp:
		m.livePickerCursor = clamp(m.livePickerCursor-1, 0, len(m.livePickerCandidates)-1)
	case keyEnter:
		if m.livePickerCursor < 0 || m.livePickerCursor >= len(m.livePickerCandidates) {
			return m, nil
		}
		chosen := m.livePickerCandidates[m.livePickerCursor]
		purpose := m.livePickerPurpose
		m.mode = m.livePickerReturnMode
		m.livePickerCandidates = nil
		m.livePickerTitle = ""
		if purpose == LivePickerASLFocus {
			return m, commands.Focus(m.client, chosen.TabID)
		}
		return m.reopenWorktreeWithParameters(map[string]string{"session": chosen.ID})
	}
	scrollLivePickerToCursor(&m)
	return m, nil
}

func scrollLivePickerToCursor(m *Model) {
	total := len(m.livePickerCandidates)
	visible := livePickerVisibleRows(*m)
	if total == 0 || visible <= 0 {
		m.livePickerOffset = 0
		return
	}
	m.livePickerCursor = clamp(m.livePickerCursor, 0, total-1)
	if m.livePickerCursor < m.livePickerOffset {
		m.livePickerOffset = m.livePickerCursor
	}
	if m.livePickerCursor >= m.livePickerOffset+visible {
		m.livePickerOffset = m.livePickerCursor - visible + 1
	}
}

func renderLiveSessionPicker(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Choose live session: " + m.livePickerTitle))
	sb.WriteString("\n\n")
	if len(m.livePickerCandidates) == 0 {
		sb.WriteString(styleDim.Render("No live-session candidates remain."))
		sb.WriteByte('\n')
		return sb.String()
	}
	contentWidth := m.width - 10
	if contentWidth < 1 {
		contentWidth = 1
	}
	window := layout.ListWindowFor(len(m.livePickerCandidates), m.livePickerOffset, livePickerVisibleRows(m))
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲ more sessions"))
		sb.WriteByte('\n')
	}
	for index := window.Start; index < window.End; index++ {
		candidate := m.livePickerCandidates[index]
		line := formatLiveSessionCandidate(candidate, contentWidth)
		if index == m.livePickerCursor {
			line = renderSelectedRow(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼ more sessions"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func livePickerVisibleRows(m Model) int {
	rows := rootBodyHeight(m) - 4
	if rows < 1 {
		return 1
	}
	return rows
}

func formatLiveSessionCandidate(candidate actions.WorktreeLiveSession, width int) string {
	project := strings.TrimSpace(candidate.Project)
	if project == "" {
		project = "(unknown project)"
	}
	identity := strings.Trim(strings.Join([]string{candidate.Profile, candidate.Harness}, "/"), "/")
	if identity == "" {
		identity = "unknown profile"
	}
	location := fmt.Sprintf("tab %d", candidate.TabID)
	if candidate.WindowID != 0 {
		location += fmt.Sprintf(" / window %d", candidate.WindowID)
	}
	line := fmt.Sprintf("%-18s  %-20s  %-22s  candidate %s", project, identity, location, candidate.ID)
	if candidate.HarnessSessionID != "" {
		line += "  native " + candidate.HarnessSessionID
	}
	return layout.StyledTruncate(line, width, "…")
}

func renderLiveSessionPickerBox(m Model) string {
	content := strings.TrimRight(renderLiveSessionPicker(m), "\n")
	boxWidth := layout.PopupWidth(content) + styleOverlayBox.GetHorizontalFrameSize()
	if boxWidth < 64 {
		boxWidth = 64
	}
	if available := m.width - 4; available < boxWidth {
		boxWidth = available
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(content)
}
