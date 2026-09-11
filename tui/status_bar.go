package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
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
	if msg, ok := topologyStatus(m, modelNow(m)); ok {
		return msg
	}
	if msg, ok := modeErrorStatus(m); ok {
		return msg
	}
	if daemonHealthDegraded(m) || hookHealthDegraded(m) {
		return statusMessage{Text: "Health degraded", Severity: statusWarning}
	}
	if m.mode == ModeSessionSearch && m.sessionRefreshInFlight {
		if count := len(m.sessionResults); count > 0 {
			return statusMessage{
				Text:     fmt.Sprintf("Scanning transcripts… showing %d cached result%s; updates automatically", count, plural(count)),
				Severity: statusInfo,
			}
		}
		return statusMessage{Text: "Scanning transcripts… results will update automatically", Severity: statusInfo}
	}
	return statusMessage{Text: fallbackStatusText(m), Severity: statusIdle}
}

// topologyStatus is the live window tree's own slot in the status bar, ranked
// directly below action errors so a refresh problem never displaces the report
// of something the operator just asked for.
//
// A refresh failure is only fatal while the dashboard is cold — no successful
// refresh has ever landed, so there is nothing to keep showing and the red
// `Kitty error` is the whole truth. Once a tree has been rendered, an outage is
// a degradation rather than a failure: the rows stay, and the only question is
// how old they have become. Below the threshold nothing is said at all, because
// a dashboard that shouts about a two-second gap between refreshes teaches the
// operator to ignore it.
func topologyStatus(m Model, now time.Time) (statusMessage, bool) {
	age, warm := topologyAge(m, now)
	if !warm {
		if m.err != nil {
			return statusMessage{Text: "Kitty error: " + m.err.Error(), Severity: statusError}, true
		}
		return statusMessage{}, false
	}
	if age < protocol.KittySnapshotStaleAfter {
		return statusMessage{}, false
	}
	return statusMessage{
		Text:     fmt.Sprintf("Kitty refresh delayed; showing cached sessions (%ds old)", int(age/time.Second)),
		Severity: statusWarning,
	}, true
}

// topologyAge reports how old the window tree currently on screen is: the age
// it already carried when it was applied plus everything elapsed since. Adding
// the elapsed term is what lets the warning appear during an outage, when no
// message is arriving to recompute anything.
//
// The threshold is protocol.KittySnapshotStaleAfter, the daemon's own — but the
// verdict is not. A daemon Stale flag describes one snapshot at the moment it
// answered and says nothing about a refresh that never reached a daemon at all,
// so the age the operator is warned about has to be derived here. The flag
// remains the sole authority over whether topology may drive Kitty writes.
//
// A backwards clock is clamped to zero. The false return is the cold state.
func topologyAge(m Model, now time.Time) (time.Duration, bool) {
	if m.topologyObservedAt.IsZero() {
		return 0, false
	}
	age := m.topology.Age + now.Sub(m.topologyObservedAt)
	if age < 0 {
		return 0, true
	}
	return age, true
}

func modeErrorStatus(m Model) (statusMessage, bool) {
	switch m.mode {
	case ModeSessionSearch, ModeDeleteChoice:
		if m.sessionSearchErr != nil {
			return statusMessage{Text: "Search error: " + m.sessionSearchErr.Error(), Severity: statusError}, true
		}
		if m.sessionRefreshErr != nil {
			return statusMessage{
				Text:     sentenceCase(sessionRefreshErrorLabel(m)) + ": " + m.sessionRefreshErr.Error(),
				Severity: statusError,
			}, true
		}
	case ModeChipPicker:
		if chipPickerSourceMode(m) == ModePlanSearch {
			if m.planSearchErr != nil {
				return statusMessage{Text: "Search error: " + m.planSearchErr.Error(), Severity: statusError}, true
			}
			if m.planRefreshErr != nil {
				return statusMessage{Text: "Refresh error: " + m.planRefreshErr.Error(), Severity: statusError}, true
			}
		} else {
			if m.sessionSearchErr != nil {
				return statusMessage{Text: "Search error: " + m.sessionSearchErr.Error(), Severity: statusError}, true
			}
			if m.sessionRefreshErr != nil {
				return statusMessage{
					Text:     sentenceCase(sessionRefreshErrorLabel(m)) + ": " + m.sessionRefreshErr.Error(),
					Severity: statusError,
				}, true
			}
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
	case ModePlanView:
		if m.planViewerLoadErr != nil {
			return statusMessage{Text: "Read error: " + m.planViewerLoadErr.Error(), Severity: statusError}, true
		}
	case ModeWorktrees:
		if m.worktreeErr != nil {
			return statusMessage{Text: "Refresh error: " + m.worktreeErr.Error(), Severity: statusError}, true
		}
		if len(m.worktreeWarnings) > 0 {
			return statusMessage{Text: fmt.Sprintf("Worktrees: %d warning%s", len(m.worktreeWarnings), plural(len(m.worktreeWarnings))), Severity: statusWarning}, true
		}
	case ModeASLRuns:
		if m.aslErr != nil {
			return statusMessage{Text: sentenceCase(m.aslErr.Error()), Severity: statusError}, true
		}
		if m.aslNotice != "" {
			return statusMessage{Text: sentenceCase(m.aslNotice), Severity: statusWarning}, true
		}
		if aslView(m) == asl.ViewStale {
			return statusMessage{Text: "Runs: read-only, showing the last snapshot", Severity: statusWarning}, true
		}
	case ModeASLResponse:
		if m.aslResponse.Notice != "" {
			return statusMessage{Text: sentenceCase(m.aslResponse.Notice), Severity: statusWarning}, true
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

// daemonHealthDegraded and hookHealthDegraded are the status bar's reading of
// two named issues. They ask the issue model rather than re-testing the raw
// fields, so the "Health degraded" line, the header hint strip, and the details
// overlay cannot disagree about whether a check is degraded.
//
// Topology is deliberately NOT folded into them even though it is now an issue
// of its own: topologyStatus already owns that slot above, with its own cold and
// warm wordings, and a second claim on the same condition one rank lower would
// be the duplication this consolidation exists to remove.
//
// Configuration is likewise left out. Its notice has a banner and is dismissible,
// and letting a dismissible warning drive the status line would either survive
// the dismissal or let the dismissal reach the line that reports real failures.
func daemonHealthDegraded(m Model) bool {
	return healthIssueDegraded(m, dashboard.HealthIssueDaemon)
}

func hookHealthDegraded(m Model) bool {
	return healthIssueDegraded(m, dashboard.HealthIssueHooks)
}

// healthIssueDegraded reports whether the named issue has been observed to be in
// trouble. HealthUnknown is not degraded: a check that has never run is not
// evidence of a problem any more than it is evidence of health, and reporting it
// as either is what the session's "unobserved checks reported as healthy"
// criterion cuts both ways against.
func healthIssueDegraded(m Model, id string) bool {
	issue, ok := dashboard.FindHealthIssue(healthIssues(m), id)
	if !ok {
		return false
	}
	return issue.State == dashboard.HealthWarning || issue.State == dashboard.HealthFailed
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
		if m.activeConfigPickerPurpose() == launch.ConfigPickerWorktreeReopen {
			return "Choose worktree replacement profile"
		}
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
	case ModeCommandPalette:
		n := len(m.commandResults)
		return fmt.Sprintf("Command Palette: %d result%s", n, plural(n))
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
	case ModeHealth:
		return "Health details"
	case ModeProjectSwitcher:
		// The applied scope, not the highlighted one. The cursor moves while the
		// operator reads the list and the filter behind the overlay has not
		// changed yet; naming the highlight here would say the dashboard is
		// showing something it is not.
		if applied := m.activeFilter.Project; applied != "" {
			return "Switch project: " + applied
		}
		return "Switch project: all projects"
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
	case ModePlanView:
		return planViewStatusText(m)
	case ModeWorktrees:
		if m.worktreeActionInFlight {
			return "Worktrees: lifecycle action in progress"
		}
		if m.worktreeLoading {
			if len(m.worktreeEntries) > 0 {
				return fmt.Sprintf("Worktrees: refreshing %d cached", len(m.worktreeEntries))
			}
			return "Worktrees: loading"
		}
		if len(m.worktreeFiltered) == len(m.worktreeEntries) {
			return fmt.Sprintf("Worktrees: %d managed", len(m.worktreeEntries))
		}
		return fmt.Sprintf("Worktrees: %d/%d visible", len(m.worktreeFiltered), len(m.worktreeEntries))
	case ModeASLRuns:
		switch aslView(m) {
		case asl.ViewLoading:
			return "Runs: loading"
		case asl.ViewUnavailable:
			return "Runs: agent-session-loop unavailable"
		}
		return fmt.Sprintf("Runs: %d need action / %d active", asl.NeedsActionCount(m.aslRows), len(m.aslRows))
	case ModeASLResponse:
		if m.aslResponse.InFlight {
			return "Runs: submitting"
		}
		if total := len(m.aslResponse.QuestionIDs); total > 1 {
			return fmt.Sprintf("Runs: answering question %d of %d", m.aslResponse.QuestionIdx+1, total)
		}
		if m.aslResponse.Kind == asl.ResponsePaused {
			return "Runs: reply and resume"
		}
		return "Runs: reply"
	case ModeLiveSessionPicker:
		return fmt.Sprintf("Choose live session: %d candidate%s", len(m.livePickerCandidates), plural(len(m.livePickerCandidates)))
	default:
		return listStatusText(m)
	}
}

// listStatusText is the dashboard's own line: how many sessions there are, how
// many the filter left, and how those break down across the three urgency
// buckets. The breakdown is what makes the line worth reading in the Projects
// view, where a collapsed family hides its members' icons — the tally says how
// many rows are folded away needing attention without unfolding anything.
//
// The counts are taken from the VISIBLE SESSIONS (m.filtered) rather than from
// the projected items, and that is the whole of the count semantics: a family
// summary is a line, not a session, so counting items would count a two-session
// family three times in the expanded view and once in the collapsed one, and the
// same dashboard would report a different number of sessions depending on which
// families the operator had folded. Sessions are what the operator has; lines
// are how they are currently drawn.
//
// Every bucket decision is dashboard.StateGroup's, so the tally cannot drift
// from the group headings the rows render under, nor from the `!attention`,
// `!active`, and `!idle` filters that select each bucket.
func listStatusText(m Model) string {
	if topologyLoading(m) {
		return "Loading sessions…"
	}
	total := len(m.sessions)
	visible := len(m.filtered)
	if total == 0 {
		return "No running sessions"
	}
	lead := fmt.Sprintf("Sessions: %d", total)
	if visible != total {
		lead = fmt.Sprintf("Sessions: %d/%d visible", visible, total)
	}
	counts := visibleStateCounts(m)
	return fmt.Sprintf("%s · %d attention · %d active · %d idle", lead, counts[0], counts[1], counts[2])
}

// visibleStateCounts tallies the filtered sessions into the three urgency
// buckets, indexed by group. It reads m.filtered directly: the item projection
// is a rendering of these rows, and a summary line standing for several of them
// must never be added to or substituted for its members.
func visibleStateCounts(m Model) [3]int {
	var counts [3]int
	for _, s := range m.filtered {
		g := dashboard.StateGroup(s, m.lastActivity, m.idleThreshold)
		if g < 0 || g >= len(counts) {
			continue
		}
		counts[g]++
	}
	return counts
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
	if layout.Width(text) > width {
		text = layout.Truncate(text, width, "...")
	}
	return layout.PadRight(text, width)
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
