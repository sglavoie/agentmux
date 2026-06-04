package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
)

const loopTailPickerMaxRows = 10

func (m Model) handleLoopTailPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ:
		m.closeLoopTailPicker()
		return m, nil
	case keyJ, keyDown:
		m.loopTailCursor = clamp(m.loopTailCursor+1, 0, len(m.loopTailCandidates)-1)
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyK, keyUp:
		m.loopTailCursor = clamp(m.loopTailCursor-1, 0, len(m.loopTailCandidates)-1)
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyG:
		m.loopTailCursor = 0
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyGG:
		m.loopTailCursor = len(m.loopTailCandidates) - 1
		if m.loopTailCursor < 0 {
			m.loopTailCursor = 0
		}
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyPgDown, keyCtrlD, keyCtrlF:
		step := loopTailPickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.loopTailCursor = clamp(m.loopTailCursor+step, 0, len(m.loopTailCandidates)-1)
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyPgUp, keyCtrlU, keyCtrlB:
		step := loopTailPickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.loopTailCursor = clamp(m.loopTailCursor-step, 0, len(m.loopTailCandidates)-1)
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	case keyEnter:
		if len(m.loopTailCandidates) == 0 {
			m.loopTailCursor = 0
			m.loopTailOffset = 0
			return m, nil
		}
		if m.loopTailCursor < 0 || m.loopTailCursor >= len(m.loopTailCandidates) {
			scrollLoopTailPickerToCursor(&m)
			return m, nil
		}
		candidate := m.loopTailCandidates[m.loopTailCursor]
		m.closeLoopTailPicker()
		m.actionErr = nil
		return m, loopTailLaunchCmd(candidate)
	}
	return m, nil
}

func (m *Model) closeLoopTailPicker() {
	m.mode = ModeList
	m.loopTailCandidates = nil
	m.loopTailCursor = 0
	m.loopTailOffset = 0
}

func loopTailPickerVisibleHeight(m Model) int {
	if m.height == 0 {
		return len(m.loopTailCandidates)
	}
	h := m.height - 8
	if h < 1 {
		return 1
	}
	if h > loopTailPickerMaxRows {
		h = loopTailPickerMaxRows
	}
	return h
}

func scrollLoopTailPickerToCursor(m *Model) {
	total := len(m.loopTailCandidates)
	height := loopTailPickerVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.loopTailCursor = 0
		m.loopTailOffset = 0
		return
	}
	if m.loopTailCursor < 0 {
		m.loopTailCursor = 0
	}
	if m.loopTailCursor >= total {
		m.loopTailCursor = total - 1
	}
	if m.loopTailCursor < m.loopTailOffset {
		m.loopTailOffset = m.loopTailCursor
	}
	for m.loopTailOffset < m.loopTailCursor {
		budget := height
		if m.loopTailOffset > 0 {
			budget--
		}
		if m.loopTailCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.loopTailCursor-m.loopTailOffset+1 <= budget {
			break
		}
		m.loopTailOffset++
	}
	if m.loopTailOffset < 0 {
		m.loopTailOffset = 0
	}
}

func renderLoopTailPicker(m Model) string {
	var sb strings.Builder
	width := loopTailPickerInnerWidth(m)
	total := len(m.loopTailCandidates)

	sb.WriteString(styleHeader.Render("Choose loop to tail"))
	if total > 0 {
		sb.WriteString(styleDim.Render(fmt.Sprintf("  %d active", total)))
	}
	sb.WriteByte('\n')

	if total == 0 {
		sb.WriteString(styleDim.Render(padRight("  (no active loop candidates)", width)))
		sb.WriteByte('\n')
		return sb.String()
	}

	cursor, offset := normalizedLoopTailPickerWindow(m)
	height := loopTailPickerVisibleHeight(m)
	startIdx, endIdx, showTop, showBottom := loopTailPickerRange(total, offset, height)

	if showTop {
		sb.WriteString(styleDim.Render(padRight("  ^", width)))
		sb.WriteByte('\n')
	}

	now := time.Now()
	for i := startIdx; i < endIdx; i++ {
		row := loopTailCandidateRow(m.loopTailCandidates[i], width, now)
		if i == cursor {
			sb.WriteString(styleSelected.Render(row))
		} else {
			sb.WriteString(row)
		}
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render(padRight("  v", width)))
		sb.WriteByte('\n')
	}

	return sb.String()
}

func normalizedLoopTailPickerWindow(m Model) (cursor, offset int) {
	total := len(m.loopTailCandidates)
	if total == 0 {
		return 0, 0
	}
	cursor = m.loopTailCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	offset = m.loopTailOffset
	if offset < 0 {
		offset = 0
	}
	if offset > cursor {
		offset = cursor
	}
	return cursor, offset
}

func loopTailPickerRange(total, offset, height int) (startIdx, endIdx int, showTop, showBottom bool) {
	startIdx = offset
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx > total {
		startIdx = total
	}
	endIdx = total
	if height <= 0 {
		return startIdx, endIdx, false, false
	}
	if height == 1 {
		endIdx = startIdx + 1
		if endIdx > total {
			endIdx = total
		}
		return startIdx, endIdx, false, false
	}

	budget := height
	if startIdx > 0 {
		showTop = true
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	endIdx = startIdx + budget
	if endIdx > total {
		endIdx = total
	}
	if endIdx < total {
		showBottom = true
		if budget > 1 {
			budget--
		} else {
			showBottom = false
		}
		endIdx = startIdx + budget
		if endIdx > total {
			endIdx = total
		}
	}
	return startIdx, endIdx, showTop, showBottom
}

func loopTailCandidateRow(candidate agentloop.Candidate, width int, now time.Time) string {
	plan := loopTailPlanLabel(candidate)
	cwd := candidate.CWD
	if cwd == "" {
		cwd = candidate.OverlayCWD()
	}
	if cwd == "" {
		cwd = "(unknown cwd)"
	}

	parts := []string{plan, fmt.Sprintf("pid %d", candidate.PID)}
	if active := loopTailActiveLabel(candidate); active != "" {
		parts = append(parts, active)
	}
	parts = append(parts, loopTailAgeLabel(candidate, now), "cwd "+cwd)
	return padRight("  "+strings.Join(parts, "  "), width)
}

func loopTailPlanLabel(candidate agentloop.Candidate) string {
	if candidate.PlanDir == "" {
		return "(unknown plan)"
	}
	base := filepath.Base(candidate.PlanDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return candidate.PlanDir
	}
	return base
}

func loopTailActiveLabel(candidate agentloop.Candidate) string {
	switch {
	case candidate.ActiveSession > 0 && candidate.ActiveTurn > 0:
		return fmt.Sprintf("s%d/t%d", candidate.ActiveSession, candidate.ActiveTurn)
	case candidate.ActiveSession > 0:
		return fmt.Sprintf("s%d", candidate.ActiveSession)
	case candidate.ActiveTurn > 0:
		return fmt.Sprintf("t%d", candidate.ActiveTurn)
	default:
		return ""
	}
}

func loopTailAgeLabel(candidate agentloop.Candidate, now time.Time) string {
	if !candidate.RunStartedAt.IsZero() {
		return "started " + formatRelativeAge(candidate.RunStartedAt, now)
	}
	if !candidate.LockCreatedAt.IsZero() {
		return "lock " + formatRelativeAge(candidate.LockCreatedAt, now)
	}
	return "started unknown"
}

func renderLoopTailPickerBox(m Model) string {
	innerWidth := loopTailPickerInnerWidth(m)
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderLoopTailPicker(m), "\n"))
}

func loopTailPickerInnerWidth(m Model) int {
	innerWidth := 92
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return innerWidth
}
