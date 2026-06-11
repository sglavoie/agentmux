package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"
)

type statusSeverity int

const (
	statusIdle statusSeverity = iota
	statusInfo
	statusSuccess
	statusWarning
	statusError
)

type statusMessage struct {
	Text     string
	Severity statusSeverity
}

func renderStatusBar(m Model) string {
	msg := currentStatusMessage(m, time.Now())
	line := fitStatusBarText(msg.Text, m.width)
	return statusBarStyle(msg.Severity).Render(line)
}

func currentStatusMessage(m Model, now time.Time) statusMessage {
	if msg, ok := latestActiveToastStatus(m, now); ok {
		return msg
	}
	return fallbackStatusMessage(m)
}

func latestActiveToastStatus(m Model, now time.Time) (statusMessage, bool) {
	for _, t := range m.toasts {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if !t.Expires.IsZero() && !now.Before(t.Expires) {
			continue
		}
		return statusMessage{
			Text:     t.Text,
			Severity: toastStatusSeverity(t.Severity),
		}, true
	}
	return statusMessage{}, false
}

func toastStatusSeverity(sev toastSeverity) statusSeverity {
	switch sev {
	case toastSuccess:
		return statusSuccess
	case toastWarning:
		return statusWarning
	case toastError:
		return statusError
	default:
		return statusInfo
	}
}

func fallbackStatusMessage(m Model) statusMessage {
	if m.actionErr != nil {
		return statusMessage{Text: "Action error: " + m.actionErr.Error(), Severity: statusError}
	}
	if m.err != nil {
		return statusMessage{Text: "Kitty error: " + m.err.Error(), Severity: statusError}
	}
	if msg, ok := modeErrorStatus(m); ok {
		return msg
	}
	if m.activeLoopTitleSyncErr != nil {
		return statusMessage{Text: "Loop title sync: " + m.activeLoopTitleSyncErr.Error(), Severity: statusWarning}
	}
	if daemonHealthDegraded(m) || hookHealthDegraded(m) {
		return statusMessage{Text: "Health degraded", Severity: statusWarning}
	}
	return statusMessage{Text: fallbackStatusText(m), Severity: statusIdle}
}

func modeErrorStatus(m Model) (statusMessage, bool) {
	switch m.mode {
	case ModeSessionSearch, ModeDeleteChoice, ModeChipPicker:
		if m.sessionSearchErr != nil {
			return statusMessage{Text: "Search error: " + m.sessionSearchErr.Error(), Severity: statusError}, true
		}
		if m.sessionRefreshErr != nil {
			return statusMessage{
				Text:     sentenceCase(sessionRefreshErrorLabel(m)) + ": " + m.sessionRefreshErr.Error(),
				Severity: statusError,
			}, true
		}
	case ModeSessionView, ModeMessageDetail:
		if m.viewerLoadErr != nil {
			return statusMessage{Text: "Read error: " + m.viewerLoadErr.Error(), Severity: statusError}, true
		}
	case ModePlanSearch, ModeLoopRunPlanSearch:
		if m.planSearchErr != nil {
			return statusMessage{Text: "Search error: " + m.planSearchErr.Error(), Severity: statusError}, true
		}
		if m.planRefreshErr != nil {
			return statusMessage{Text: "Refresh error: " + m.planRefreshErr.Error(), Severity: statusError}, true
		}
	case ModeLoopRunPlanPath:
		if m.loopRunPlanPathErr != nil {
			return statusMessage{Text: "Path error: " + m.loopRunPlanPathErr.Error(), Severity: statusError}, true
		}
	case ModePlanView:
		if m.planViewerLoadErr != nil {
			return statusMessage{Text: "Read error: " + m.planViewerLoadErr.Error(), Severity: statusError}, true
		}
	}
	return statusMessage{}, false
}

func sentenceCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func daemonHealthDegraded(m Model) bool {
	return !m.daemonHealthCheckedAt.IsZero() && !m.daemonAlive
}

func hookHealthDegraded(m Model) bool {
	if m.hookHealthErr != nil {
		return true
	}
	return !m.hookHealthCheckedAt.IsZero() && !m.hookHealth.Healthy()
}

func fallbackStatusText(m Model) string {
	switch m.mode {
	case ModeSearch:
		if strings.TrimSpace(m.filterText) != "" {
			return "Filter sessions: /" + m.filterText
		}
		return "Filter sessions"
	case ModeConfirm:
		if strings.TrimSpace(m.confirmAction) != "" {
			return "Confirm " + m.confirmAction
		}
		return "Confirm action"
	case ModePicker:
		return pickerStatusText(m)
	case ModeConfigPicker:
		if m.configPickerReturnMode == ModePicker {
			return "Choose launch profile"
		}
		return "Choose profile"
	case ModeHelp:
		return "Help"
	case ModeDetail:
		if m.detailWindowID > 0 {
			return fmt.Sprintf("Session details: window %d", m.detailWindowID)
		}
		return "Session details"
	case ModeActions:
		return "Session actions"
	case ModeSessionSearch:
		if m.sessionLoading {
			return "Past Sessions: loading"
		}
		n := len(m.sessionResults)
		return fmt.Sprintf("Past Sessions: %d result%s", n, plural(n))
	case ModeSessionView:
		return sessionViewStatusText(m)
	case ModeDeleteChoice:
		return "Delete session"
	case ModeChipPicker:
		return "Choose " + chipPickerStatusLabel(m.chipPickerKind)
	case ModeMessageDetail:
		if m.messageDetailIdx >= 0 && m.messageDetailIdx < len(m.viewerMessages) {
			return fmt.Sprintf("Message detail: %d/%d", m.messageDetailIdx+1, len(m.viewerMessages))
		}
		return "Message detail"
	case ModePlanSearch:
		if m.planLoading {
			return "Plans: loading"
		}
		n := len(m.planResults)
		return fmt.Sprintf("Plans: %d result%s", n, plural(n))
	case ModeLoopRunPlanSearch:
		if m.planLoading {
			return "Run Plan: loading"
		}
		n := len(m.planResults)
		return fmt.Sprintf("Run Plan: %d result%s", n, plural(n))
	case ModeLoopRunPlanPath:
		if strings.TrimSpace(m.loopRunPlanPath) != "" {
			return "Run Plan Path: " + strings.TrimSpace(m.loopRunPlanPath)
		}
		return "Run Plan Path"
	case ModePlanView:
		return planViewStatusText(m)
	case ModeLoopTailPicker:
		n := len(m.loopTailCandidates)
		return fmt.Sprintf("Loop tail: %d candidate%s", n, plural(n))
	default:
		return listStatusText(m)
	}
}

func listStatusText(m Model) string {
	total := len(m.sessions)
	visible := len(m.filtered)
	switch {
	case total == 0:
		return "No running sessions"
	case visible == total:
		return fmt.Sprintf("Sessions: %d running", total)
	default:
		return fmt.Sprintf("Sessions: %d/%d visible", visible, total)
	}
}

func pickerStatusText(m Model) string {
	selected := selectedPickerCount(m.pickerSelected)
	total := len(m.pickerSessions)
	visible := len(m.pickerFilteredIdx)
	base := fmt.Sprintf("Launch Sessions: %d selected", selected)
	switch {
	case total == 0:
		return base
	case visible == total:
		return fmt.Sprintf("%s - %d session%s", base, total, plural(total))
	default:
		return fmt.Sprintf("%s - %d/%d visible", base, visible, total)
	}
}

func selectedPickerCount(selected map[int]bool) int {
	count := 0
	for _, ok := range selected {
		if ok {
			count++
		}
	}
	return count
}

func sessionViewStatusText(m Model) string {
	if m.viewerSession != nil {
		project := m.viewerSession.Project
		if project == "" {
			project = "(unknown)"
		}
		n := len(m.viewerMessages)
		return fmt.Sprintf("Transcript: %s - %d message%s", project, n, plural(n))
	}
	return "Transcript viewer"
}

func planViewStatusText(m Model) string {
	if m.viewerPlan != nil {
		name := m.viewerPlan.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Sprintf("Plan: %s", name)
	}
	return "Plan viewer"
}

func chipPickerStatusLabel(kind chipPickerKind) string {
	switch kind {
	case chipPickerHarness:
		return "harness"
	case chipPickerProfile:
		return "profile"
	case chipPickerProject:
		return "project"
	default:
		return "option"
	}
}

func fitStatusBarText(text string, width int) string {
	text = oneLineStatusText(text)
	if width <= 0 {
		return text
	}
	if runewidth.StringWidth(text) > width {
		text = runewidth.Truncate(text, width, "...")
	}
	if pad := width - runewidth.StringWidth(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return text
}

func oneLineStatusText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "Agent Mux"
	}
	return " " + text
}

func statusBarStyle(sev statusSeverity) lipgloss.Style {
	switch sev {
	case statusInfo:
		return styleStatusInfo
	case statusSuccess:
		return styleStatusSuccess
	case statusWarning:
		return styleStatusWarning
	case statusError:
		return styleStatusError
	default:
		return styleStatusIdle
	}
}
