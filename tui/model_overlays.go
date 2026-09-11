// This file is the root's own overlays — the ones that belong to no single
// domain leaf. It holds the config picker, the per-row actions overlay, the
// session detail pane, the help sheet, the transcript delete choice, and the
// live-session picker: entering each, handling its keys, and leaving it.

package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/history"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

// activeRootMode resolves the root view that currently owns the model's async
// state. Help, confirmations, and the profile/handoff picker replace m.mode for
// the duration of the detour but return to the view they were raised from, so a
// load completing behind one of them still belongs to that view.
func (m Model) activeRootMode() Mode {
	switch m.mode {
	case ModeHelp:
		return m.helpReturnMode
	case ModeConfirm:
		return m.confirmReturnMode
	case ModeConfigPicker:
		return m.configPickerReturnMode
	case ModeLiveSessionPicker:
		return m.livePickerReturnMode
	case ModeHealth:
		return m.healthReturnMode
	case ModeASLResponse:
		return ModeASLRuns
	default:
		return m.mode
	}
}

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		if m.activeConfigPickerPurpose() == launch.ConfigPickerHandoff {
			m.handoffConversation = nil
			m.handoffEdit = false
		}
		m.mode = m.configPickerReturnMode
		if m.activeConfigPickerPurpose() == launch.ConfigPickerWorktreeReopen {
			m.worktreeActionTarget = nil
		}
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
		// The centered overlay changes this row from ANSI-free selection text
		// back to its semantic colors. Force a complete repaint so terminal
		// diffing cannot retain a cell from the prior reverse-video span.
		return m, tea.ClearScreen
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
		return m, tea.ClearScreen
	case keyEnter:
		if m.configPickerCursor >= 0 && m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			purpose := m.activeConfigPickerPurpose()
			m.mode = m.configPickerReturnMode
			if purpose == launch.ConfigPickerHandoff {
				if m.handoffConversation == nil {
					return m, nil
				}
				meta := *m.handoffConversation
				edit := m.handoffEdit
				m.handoffConversation = nil
				m.handoffEdit = false
				return m, m.conversationHandoffCmd(meta, acc.Name, edit)
			}
			if purpose == launch.ConfigPickerWorktreeReopen {
				return m.reopenWorktreeWithParameters(map[string]string{"profile": acc.Name})
			}
			if purpose == launch.ConfigPickerLauncherOverride && m.configPickerProjectIdx >= 0 && m.configPickerProjectIdx < len(m.pickerSessions) {
				defaultProfile := m.pickerSessions[m.configPickerProjectIdx].EffectiveProfile()
				if strings.EqualFold(acc.Name, defaultProfile) {
					delete(m.pickerProfileOverride, m.configPickerProjectIdx)
				} else {
					if m.pickerProfileOverride == nil {
						m.pickerProfileOverride = make(map[int]string)
					}
					m.pickerProfileOverride[m.configPickerProjectIdx] = acc.Name
				}
				m.savePickerOverrides()
				if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			if purpose == launch.ConfigPickerRestartProfile && strings.EqualFold(acc.Name, m.configPickerCurrent) {
				return m, nil
			}
			if purpose == launch.ConfigPickerDashboardLaunch {
				return m, m.dashboardLaunchWithAccountCmd(acc)
			}
			return m, m.restartWithAccountCmd(acc)
		}
	}
	return m, nil
}

// handleActionsKey dispatches the per-row actions overlay. Esc/q close the
// overlay without side effects; any other key that matches an available
// action's keyMatch runs it. Unavailable or unknown keys are ignored.
func (m Model) handleActionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	total, visible := actionsScrollBounds(m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	switch s {
	case keyEsc, keyQ:
		m.exitActionsMode()
		m.actionsOffset = 0
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.actionsOffset < maxOffset {
			m.actionsOffset++
		}
		return m, nil
	case keyK, keyUp:
		if m.actionsOffset > 0 {
			m.actionsOffset--
		}
		return m, nil
	case keyPgDown, keyCtrlF:
		m.actionsOffset += visible
		if m.actionsOffset > maxOffset {
			m.actionsOffset = maxOffset
		}
		return m, nil
	case keyPgUp, keyCtrlB:
		m.actionsOffset -= visible
		if m.actionsOffset < 0 {
			m.actionsOffset = 0
		}
		return m, nil
	case keyCtrlD:
		step := visible / 2
		if step < 1 {
			step = 1
		}
		m.actionsOffset += step
		if m.actionsOffset > maxOffset {
			m.actionsOffset = maxOffset
		}
		return m, nil
	case keyCtrlU:
		step := visible / 2
		if step < 1 {
			step = 1
		}
		m.actionsOffset -= step
		if m.actionsOffset < 0 {
			m.actionsOffset = 0
		}
		return m, nil
	case keyG:
		m.actionsOffset = 0
		return m, nil
	case keyGG:
		m.actionsOffset = maxOffset
		return m, nil
	}
	for _, a := range actionsFor(m) {
		if s != a.KeyMatch {
			continue
		}
		if !a.Available(m) {
			return m, nil
		}
		next, cmd := a.Run(m)
		next.actionsOffset = 0
		return next, cmd
	}
	return m, nil
}

// enterDetailMode switches to ModeDetail for the given window, marks the
// pane as loading, and returns a command that fetches the event ring from
// the daemon. Callers must guard against an empty session list.
func (m Model) enterDetailMode(windowID int, startInTimeline bool) (tea.Model, tea.Cmd) {
	m.mode = ModeDetail
	m.detailWindowID = windowID
	m.detailEvents = nil
	m.detailScroll = 0
	m.detailLoading = true
	m.detailErr = nil
	m.detailViewTimeline = startInTimeline
	return m, sessionDetailCmd(windowID)
}

// exitDetailMode returns to ModeList and clears every detail-pane field so
// the next entry starts from a clean state. No-op when the model is not in
// ModeDetail.
func (m *Model) exitDetailMode() {
	m.mode = ModeList
	m.detailWindowID = 0
	m.detailEvents = nil
	m.detailScroll = 0
	m.detailLoading = false
	m.detailErr = nil
	m.detailViewTimeline = false
	m.detailTimelineScroll = 0
	m.detailTimelineFold = nil
	m.detailShowSuppressed = false
	m.detailShowTimestamps = false
}

func (m Model) handleDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := detailScrollBounds(m, detailVisibleHeight(m))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}

	switch msg.String() {
	case keyEsc, keyQ, keyD:
		m.exitDetailMode()
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyToggleTimeline:
		m.detailViewTimeline = !m.detailViewTimeline
		m.detailTimelineScroll = 0
		return m, nil
	case keyToggleTimestamps:
		if m.detailViewTimeline {
			m.detailShowTimestamps = !m.detailShowTimestamps
		}
		return m, nil

	case keyPhasePrev:
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailTimelineScroll = historyDetailInput(m).TimelinePrevPhaseScroll()
		return m, nil

	case keyPhaseNext:
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailTimelineScroll = historyDetailInput(m).TimelineNextPhaseScroll()
		return m, nil

	case keyEnter:
		if !m.detailViewTimeline {
			return m, nil
		}
		fold, ok := historyDetailInput(m).TimelineToggleFold()
		if !ok {
			return m, nil
		}
		m.detailTimelineFold = fold
		m.detailTimelineScroll = historyDetailInput(m).ClampTimelineScroll()
		return m, nil

	case "s":
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailShowSuppressed = !m.detailShowSuppressed
		m.detailTimelineScroll = historyDetailInput(m).ClampTimelineScroll()
		return m, nil

	case "z":
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailTimelineFold = historyDetailInput(m).TimelineFoldAll(false)
		m.detailTimelineScroll = historyDetailInput(m).ClampTimelineScroll()
		return m, nil

	case "Z":
		if !m.detailViewTimeline {
			return m, nil
		}
		m.detailTimelineFold = historyDetailInput(m).TimelineFoldAll(true)
		return m, nil
	}

	if m.detailViewTimeline {
		tlIn := historyDetailInput(m)
		tlMax := tlIn.TimelineMaxScroll()
		tlVisible := tlIn.TimelineVisibleHeight()
		switch msg.String() {
		case keyJ, keyDown:
			if m.detailTimelineScroll < tlMax {
				m.detailTimelineScroll++
			}
		case keyK, keyUp:
			if m.detailTimelineScroll > 0 {
				m.detailTimelineScroll--
			}
		case keyPgDown:
			m.detailTimelineScroll += tlVisible
			if m.detailTimelineScroll > tlMax {
				m.detailTimelineScroll = tlMax
			}
		case keyPgUp:
			m.detailTimelineScroll -= tlVisible
			if m.detailTimelineScroll < 0 {
				m.detailTimelineScroll = 0
			}
		case keyG:
			m.detailTimelineScroll = 0
		case keyGG:
			m.detailTimelineScroll = tlMax
		}
		return m, nil
	}

	switch msg.String() {
	case keyJ, keyDown:
		if m.detailScroll < maxOffset {
			m.detailScroll++
		}
	case keyK, keyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
	case keyPgDown:
		m.detailScroll += visible
		if m.detailScroll > maxOffset {
			m.detailScroll = maxOffset
		}
	case keyPgUp:
		m.detailScroll -= visible
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case keyG:
		m.detailScroll = 0
	case keyGG:
		m.detailScroll = maxOffset
	}
	return m, nil
}

// sessionDetailCmd queries the daemon for a window's hook event ring
// buffer. Errors from a missing or unreachable daemon are captured on the
// returned msg so the detail pane can surface them rather than blocking
// indefinitely.
func sessionDetailCmd(windowID int) tea.Cmd {
	return func() tea.Msg {
		resp, err := ipc.NewClient().Send(protocol.Request{
			Type:     protocol.TypeSessionDetail,
			WindowID: windowID,
		})
		if err != nil {
			return commands.DetailEventsMsg{WindowID: windowID, Err: err}
		}
		return commands.DetailEventsMsg{WindowID: windowID, Events: resp.Events}
	}
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := helpScrollBounds(m, helpVisibleHeight(m))
	switch msg.String() {
	case keyEsc, keyQuestion, keyQ:
		m.mode = m.helpReturnMode
		m.helpOffset = 0
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.helpOffset+visible < total {
			m.helpOffset++
		}
	case keyK, keyUp:
		if m.helpOffset > 0 {
			m.helpOffset--
		}
	case keyG:
		m.helpOffset = 0
	case keyGG:
		m.helpOffset = total - visible
		if m.helpOffset < 0 {
			m.helpOffset = 0
		}
	}
	return m, nil
}

// transcriptDeleteDoneMsg returns the result of a delete-prompt mutation to
// the Bubble Tea update loop. The potentially blocking advisory-lock wait
// happens inside the tea.Cmd that produces this message, never in Update.
type transcriptDeleteDoneMsg struct {
	target   transcript.SessionMeta
	alsoFile bool
	err      error
}

// enterDeleteChoice stashes the target row and switches to the three-way
// prompt. The caller is responsible for bounds-checking sessionCursor.
func (m Model) enterDeleteChoice(meta transcript.SessionMeta) (tea.Model, tea.Cmd) {
	target := meta
	m.deleteTarget = &target
	m.mode = ModeDeleteChoice
	return m, nil
}

// exitDeleteChoice returns to ModeSessionSearch without touching the index.
// Chip filters and the text query are preserved (same invariant as S7's
// enter/exit pair).
func (m *Model) exitDeleteChoice() {
	m.deleteTarget = nil
	m.mode = ModeSessionSearch
}

// handleDeleteChoiceKey is the keypress dispatcher for ModeDeleteChoice.
// Three branches: `i` hides the entry from the index (file untouched),
// `f` removes the entry and the on-disk transcript, `esc` cancels.
// `ctrl+c`/`q` always quit (mirrors handleConfirmKey).
func (m Model) handleDeleteChoiceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyQ:
		return m, tea.Quit
	case keyEsc:
		m.exitDeleteChoice()
		return m, nil
	case "i":
		return m.applyDeleteChoice(false)
	case keyF:
		return m.applyDeleteChoice(true)
	}
	return m, nil
}

// applyDeleteChoice dispatches the deletion as a tea.Cmd so waiting for the
// cache advisory lock cannot block input or rendering. alsoFile
// gates whether the on-disk transcript is removed alongside the entry:
//   - alsoFile=false uses SetHidden (entry preserved with HiddenFromIndex=true)
//     so Refresh does not resurrect the row from disk on the next rescan.
//   - alsoFile=true uses Delete which removes the entry and `os.Remove`s the
//     underlying JSONL file.
func (m Model) applyDeleteChoice(alsoFile bool) (tea.Model, tea.Cmd) {
	if m.deleteTarget == nil || m.transcriptIndex == nil {
		m.exitDeleteChoice()
		return m, nil
	}
	target := *m.deleteTarget
	key := transcript.IndexKey(target.Harness, target.ID)
	idx := m.transcriptIndex

	m.exitDeleteChoice()
	m.viewerSession = nil
	m.viewerMessages = nil
	m.viewerMatchLines = nil

	// Invalidate searches and warm-refresh results dispatched before the
	// mutation. Completion bumps the sequence again so searches performed
	// while the mutation was waiting on the lock cannot restore stale rows.
	m.sessionSearchSeq++
	m.sessionLoading = true
	m.sessionSearchErr = nil

	return m, func() tea.Msg {
		var err error
		if alsoFile {
			err = idx.Delete(key, true)
		} else {
			err = idx.SetHidden(key, true)
		}
		return transcriptDeleteDoneMsg{target: target, alsoFile: alsoFile, err: err}
	}
}

// handleTranscriptDeleteDone publishes the mutation outcome and launches a
// fresh search against the current query. A SourceDeletedError is a partial
// success: the irreversible file deletion happened even though the cache
// metadata could not be updated.
func (m Model) handleTranscriptDeleteDone(msg transcriptDeleteDoneMsg) (tea.Model, tea.Cmd) {
	var toast Toast
	if msg.err == nil {
		if msg.alsoFile {
			toast = Toast{Severity: toastSuccess, Text: fmt.Sprintf("Deleted transcript file for %s", history.DeleteTargetLabel(msg.target))}
		} else {
			toast = Toast{Severity: toastSuccess, Text: fmt.Sprintf("Hid %s from index", history.DeleteTargetLabel(msg.target))}
		}
	} else {
		var partial *transcript.SourceDeletedError
		if errors.As(msg.err, &partial) {
			toast = Toast{
				Severity: toastWarning,
				Text:     fmt.Sprintf("Deleted transcript file for %s, but index update failed: %v", history.DeleteTargetLabel(msg.target), partial.Err),
			}
		} else {
			toast = Toast{Severity: toastError, Text: "Delete failed: " + msg.err.Error()}
		}
	}

	// Clamp the cursor before the async sessionSearchDoneMsg handler runs: it
	// also clamps, but a stale offset between now and that message can scroll
	// past the new tail. Mirror the pattern in case sessionSearchDoneMsg.
	if m.sessionCursor >= len(m.sessionResults)-1 && m.sessionCursor > 0 {
		m.sessionCursor--
	}
	scrollSessionSearchToCursor(&m)

	var cmds []tea.Cmd
	if c := m.pushToast(toast); c != nil {
		cmds = append(cmds, c)
	}

	// Re-dispatch the search so the list refreshes without the deleted row.
	// Bump seq again so work started while the mutation was blocked cannot win.
	m.sessionSearchSeq++
	m.sessionLoading = true
	m.sessionSearchErr = nil
	if m.transcriptIndex != nil {
		cmds = append(cmds, m.runSessionSearchCmd(m.sessionSearchSeq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery()))
	} else {
		m.sessionLoading = false
	}
	return m, tea.Batch(cmds...)
}

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
		m.configPickerCursor = launch.AccountIndex(m.cfg.Accounts, m.configPickerCurrent)
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
