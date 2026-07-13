package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
	if m.dataServicesRecoveryActive() || m.dataServicesRecoveryTerminal() {
		if msg, ok := dataServicesRecoveryStatus(m); ok {
			return msg
		}
	}
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
	if msg, ok := dataServicesRecoveryStatus(m); ok {
		return msg
	}
	if daemonHealthDegraded(m) || hookHealthDegraded(m) {
		return statusMessage{Text: "Health degraded", Severity: statusWarning}
	}
	return statusMessage{Text: fallbackStatusText(m), Severity: statusIdle}
}

func dataServicesRecoveryStatus(m Model) (statusMessage, bool) {
	switch m.dataServicesRecoveryState {
	case dataServicesRecoveryRequested, dataServicesRecoveryInFlight:
		return statusMessage{
			Text:     "Database corruption detected; preserving and rebuilding workspace data…",
			Severity: statusWarning,
		}, true
	case dataServicesRecoverySucceeded:
		if m.dataServicesRecoveryResult.Replaced {
			if path := strings.TrimSpace(m.dataServicesRecoveryResult.QuarantineDir); path != "" {
				return statusMessage{Text: "Database recovered; corrupt files preserved at " + path, Severity: statusSuccess}, true
			}
			return statusMessage{Text: "Database recovered; workspace data rebuilt", Severity: statusSuccess}, true
		}
		return statusMessage{Text: "Database was repaired by another process; workspace data reloaded", Severity: statusSuccess}, true
	case dataServicesRecoveryFailed, dataServicesRecoveryAlreadyAttempted:
		text := "Database recovery stopped"
		if m.dataServicesRecoveryErr != nil {
			text += ": " + m.dataServicesRecoveryErr.Error()
		}
		return statusMessage{Text: text, Severity: statusError}, true
	default:
		return statusMessage{}, false
	}
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
	case ModePlanSearch:
		if m.planSearchErr != nil {
			return statusMessage{Text: "Search error: " + m.planSearchErr.Error(), Severity: statusError}, true
		}
		if m.planRefreshErr != nil {
			return statusMessage{Text: "Refresh error: " + m.planRefreshErr.Error(), Severity: statusError}, true
		}
	case ModeUnifiedSearch:
		if m.unifiedSearchErr != nil {
			return statusMessage{Text: "Search error: " + m.unifiedSearchErr.Error(), Severity: statusError}, true
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
	case ModePalette:
		if strings.TrimSpace(m.paletteQuery) == "" {
			return "Command palette"
		}
		return "Command palette: " + m.paletteQuery
	case ModeWorkspace:
		if m.workspaceLoading && len(m.workspaceSnapshot.Workspaces) == 0 {
			return "Workspaces: loading"
		}
		if m.workspaceLoadErr != nil {
			if len(m.workspaceSnapshot.Workspaces) > 0 {
				return fmt.Sprintf("Workspaces: %d project%s · refresh failed", len(m.workspaceSnapshot.Workspaces), plural(len(m.workspaceSnapshot.Workspaces)))
			}
			return "Workspaces unavailable"
		}
		if m.workspaceProjectionInFlight && len(m.workspaceSnapshot.Workspaces) > 0 {
			return fmt.Sprintf("Workspaces: %d project%s · refreshing", len(m.workspaceSnapshot.Workspaces), plural(len(m.workspaceSnapshot.Workspaces)))
		}
		return fmt.Sprintf("Workspaces: %d project%s", len(m.workspaceSnapshot.Workspaces), plural(len(m.workspaceSnapshot.Workspaces)))
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
	case ModeUnifiedSearch:
		if m.unifiedLoading {
			return "Unified Search: searching"
		}
		return fmt.Sprintf("Unified Search: %d result%s", len(m.unifiedResults), plural(len(m.unifiedResults)))
	case ModePlanView:
		return planViewStatusText(m)
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
	if displayWidth(text) > width {
		text = displayTruncate(text, width, "...")
	}
	return displayPadRight(text, width)
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
