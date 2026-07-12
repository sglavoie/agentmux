package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// A workspace is split only when both panes can remain useful.  The same
// detail item list is used in every layout so changing terminal size never
// changes ordering or the action attached to Enter.
const (
	workspaceSplitMinWidth  = 100
	workspaceSplitMinHeight = 18
)

type workspaceFocus uint8

const (
	workspaceFocusProjects workspaceFocus = iota
	workspaceFocusDetail
)

type workspaceDetailKind uint8

const (
	workspaceDetailSection workspaceDetailKind = iota
	workspaceDetailLive
	workspaceDetailConversation
	workspaceDetailPlan
	workspaceDetailActivity
	workspaceDetailEmpty
)

// workspaceDetailItem is the flat interaction model behind both the split
// pane and the narrow drill-down. Sections are deliberately entries too: they
// preserve a single stable order, but are skipped by navigation and Enter.
type workspaceDetailItem struct {
	kind         workspaceDetailKind
	label        string
	live         session.RunningSession
	worktreePath string
	conversation workspace.Conversation
	plan         workspace.LinkedPlan
	activity     workspace.ActivityItem
}

func workspaceUsesSplit(m Model) bool {
	return m.width >= workspaceSplitMinWidth && m.height >= workspaceSplitMinHeight
}

func (m Model) selectedWorkspace() (workspace.WorkspaceSnapshot, bool) {
	if m.workspaceCursor < 0 || m.workspaceCursor >= len(m.workspaceSnapshot.Workspaces) {
		return workspace.WorkspaceSnapshot{}, false
	}
	return m.workspaceSnapshot.Workspaces[m.workspaceCursor], true
}

func workspaceLiveState(live workspace.LiveSession) string {
	if live.Daemon.Observed && live.Daemon.IconState != "" {
		return live.Daemon.IconState
	}
	return live.Session.State
}

func workspaceNeedsAttention(live workspace.LiveSession) bool {
	switch workspaceLiveState(live) {
	case "key", "error", "warning", "question":
		return true
	default:
		return false
	}
}

func workspaceItems(item workspace.WorkspaceSnapshot) []workspaceDetailItem {
	items := make([]workspaceDetailItem, 0, 1+len(item.LiveSessions)+len(item.Conversations)+len(item.Plans)+len(item.Activity))
	attention := make([]workspace.LiveSession, 0)
	live := make([]workspace.LiveSession, 0)
	for _, running := range item.LiveSessions {
		if workspaceNeedsAttention(running) {
			attention = append(attention, running)
		} else {
			live = append(live, running)
		}
	}
	if len(attention) > 0 {
		items = append(items, workspaceDetailItem{kind: workspaceDetailSection, label: "Attention"})
		for _, running := range attention {
			items = append(items, workspaceDetailItem{kind: workspaceDetailLive, live: running.Session, worktreePath: running.WorktreePath})
		}
	}
	if len(live) > 0 {
		items = append(items, workspaceDetailItem{kind: workspaceDetailSection, label: "Live sessions"})
		for _, running := range live {
			items = append(items, workspaceDetailItem{kind: workspaceDetailLive, live: running.Session, worktreePath: running.WorktreePath})
		}
	}
	if len(item.Conversations) > 0 {
		items = append(items, workspaceDetailItem{kind: workspaceDetailSection, label: "Recent conversations"})
		for _, conversation := range item.Conversations {
			items = append(items, workspaceDetailItem{kind: workspaceDetailConversation, conversation: conversation})
		}
	}
	if len(item.Plans) > 0 {
		items = append(items, workspaceDetailItem{kind: workspaceDetailSection, label: "Plans"})
		for _, plan := range item.Plans {
			items = append(items, workspaceDetailItem{kind: workspaceDetailPlan, plan: plan})
		}
	}
	activityLabel := "Activity"
	if item.UnreadCount > 0 {
		activityLabel += fmt.Sprintf(" (%d unread)", item.UnreadCount)
	}
	items = append(items, workspaceDetailItem{kind: workspaceDetailSection, label: activityLabel})
	for _, record := range item.Activity {
		items = append(items, workspaceDetailItem{kind: workspaceDetailActivity, activity: record})
	}
	if len(item.Activity) == 0 {
		items = append(items, workspaceDetailItem{kind: workspaceDetailEmpty, label: "No meaningful activity yet"})
	}
	if len(item.LiveSessions) == 0 && len(item.Conversations) == 0 && len(item.Plans) == 0 && len(item.Activity) == 0 {
		items = append([]workspaceDetailItem{
			{kind: workspaceDetailEmpty, label: "No live work, conversations, or plans yet"},
		}, items...)
	}
	return items
}

func workspaceSelectable(item workspaceDetailItem) bool {
	switch item.kind {
	case workspaceDetailLive, workspaceDetailConversation, workspaceDetailPlan, workspaceDetailActivity:
		return true
	default:
		return false
	}
}

func workspaceDetailID(item workspaceDetailItem) string {
	switch item.kind {
	case workspaceDetailLive:
		return fmt.Sprintf("live:%d", item.live.WindowID)
	case workspaceDetailConversation:
		return "conversation:" + transcript.IndexKey(item.conversation.Harness, item.conversation.ID)
	case workspaceDetailPlan:
		return "plan:" + item.plan.Directory + ":" + item.plan.ID
	case workspaceDetailActivity:
		return fmt.Sprintf("activity:%d", item.activity.ID)
	default:
		return fmt.Sprintf("%d:%s", item.kind, item.label)
	}
}

func workspaceFirstSelectable(items []workspaceDetailItem) int {
	for i, item := range items {
		if workspaceSelectable(item) {
			return i
		}
	}
	return 0
}

func workspaceMoveCursor(items []workspaceDetailItem, cursor, direction int) int {
	if len(items) == 0 {
		return 0
	}
	if cursor < 0 || cursor >= len(items) || !workspaceSelectable(items[cursor]) {
		return workspaceFirstSelectable(items)
	}
	for i := cursor + direction; i >= 0 && i < len(items); i += direction {
		if workspaceSelectable(items[i]) {
			return i
		}
	}
	return cursor
}

func (m *Model) clampWorkspaceSelection() {
	m.workspaceCursor = clamp(m.workspaceCursor, 0, len(m.workspaceSnapshot.Workspaces)-1)
	item, ok := m.selectedWorkspace()
	if !ok {
		m.workspaceDetailCursor = 0
		m.workspaceDetailOffset = 0
		return
	}
	items := workspaceItems(item)
	if m.workspaceDetailCursor < 0 || m.workspaceDetailCursor >= len(items) || !workspaceSelectable(items[m.workspaceDetailCursor]) {
		m.workspaceDetailCursor = workspaceFirstSelectable(items)
	}
	m.workspaceProjectOffset = clamp(m.workspaceProjectOffset, 0, maxWorkspaceOffset(len(m.workspaceSnapshot.Workspaces), workspacePaneHeight(*m)))
	m.workspaceDetailOffset = clamp(m.workspaceDetailOffset, 0, maxWorkspaceOffset(len(items), workspacePaneHeight(*m)))
	m.scrollWorkspaceToSelection(items)
}

func maxWorkspaceOffset(total, visible int) int {
	if visible < 1 {
		visible = 1
	}
	if total-visible < 0 {
		return 0
	}
	return total - visible
}

func workspacePaneHeight(m Model) int {
	// Header, workspace title, and the root's footer/status consume the rest.
	if m.height <= 0 {
		return 12
	}
	h := rootBodyHeight(m) - 3
	if h < 2 {
		return 2
	}
	return h
}

func (m *Model) scrollWorkspaceToSelection(items []workspaceDetailItem) {
	visible := workspacePaneHeight(*m)
	if m.workspaceCursor < m.workspaceProjectOffset {
		m.workspaceProjectOffset = m.workspaceCursor
	}
	if m.workspaceCursor >= m.workspaceProjectOffset+visible {
		m.workspaceProjectOffset = m.workspaceCursor - visible + 1
	}
	if m.workspaceDetailCursor < m.workspaceDetailOffset {
		m.workspaceDetailOffset = m.workspaceDetailCursor
	}
	if m.workspaceDetailCursor >= m.workspaceDetailOffset+visible {
		m.workspaceDetailOffset = m.workspaceDetailCursor - visible + 1
	}
	m.workspaceProjectOffset = clamp(m.workspaceProjectOffset, 0, maxWorkspaceOffset(len(m.workspaceSnapshot.Workspaces), visible))
	m.workspaceDetailOffset = clamp(m.workspaceDetailOffset, 0, maxWorkspaceOffset(len(items), visible))
}

func (m Model) selectedWorkspaceDetail() (workspaceDetailItem, bool) {
	item, ok := m.selectedWorkspace()
	if !ok {
		return workspaceDetailItem{}, false
	}
	items := workspaceItems(item)
	if m.workspaceDetailCursor < 0 || m.workspaceDetailCursor >= len(items) {
		return workspaceDetailItem{}, false
	}
	return items[m.workspaceDetailCursor], true
}

func (m Model) handleWorkspaceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.clampWorkspaceSelection()
	key := msg.String()
	if key == keyCtrlC || key == keyQ {
		return m, tea.Quit
	}
	if key == keyWorkspaceLive {
		m.workspaceDrillDown = false
		m.workspaceFocus = workspaceFocusProjects
		m.mode = ModeList
		return m, nil
	}
	if key == keyQuestion {
		m.helpOffset = 0
		m.helpReturnMode = ModeWorkspace
		m.mode = ModeHelp
		return m, nil
	}
	// Loading and genuinely empty workspace lists have no project or detail
	// selection. Keep their navigation keys inert instead of moving focus into
	// an invisible empty pane.
	if len(m.workspaceSnapshot.Workspaces) == 0 {
		return m, nil
	}

	if m.workspaceFocus == workspaceFocusProjects && !m.workspaceDrillDown {
		switch key {
		case keyJ, keyDown:
			m.workspaceCursor = clamp(m.workspaceCursor+1, 0, len(m.workspaceSnapshot.Workspaces)-1)
			m.workspaceDetailCursor = 0
			m.clampWorkspaceSelection()
		case keyK, keyUp:
			m.workspaceCursor = clamp(m.workspaceCursor-1, 0, len(m.workspaceSnapshot.Workspaces)-1)
			m.workspaceDetailCursor = 0
			m.clampWorkspaceSelection()
		case keyG:
			m.workspaceCursor = 0
			m.workspaceDetailCursor = 0
			m.clampWorkspaceSelection()
		case keyGG:
			m.workspaceCursor = len(m.workspaceSnapshot.Workspaces) - 1
			m.workspaceDetailCursor = 0
			m.clampWorkspaceSelection()
		case keyEnter, keyTab, keyShiftTab:
			m.workspaceFocus = workspaceFocusDetail
			if !workspaceUsesSplit(m) {
				m.workspaceDrillDown = true
			}
			m.clampWorkspaceSelection()
			return m, m.markSelectedWorkspaceRead()
		case keySessionSearch:
			if selected, ok := m.selectedWorkspace(); ok {
				return m.enterSessionSearchForProject(selected.DisplayName)
			}
		}
		return m, nil
	}

	if key == keyEsc {
		if m.workspaceDrillDown {
			m.workspaceDrillDown = false
		}
		m.workspaceFocus = workspaceFocusProjects
		return m, nil
	}
	if key == keyTab || key == keyShiftTab {
		m.workspaceFocus = workspaceFocusProjects
		m.workspaceDrillDown = false
		return m, nil
	}
	selected, ok := m.selectedWorkspace()
	if !ok {
		return m, nil
	}
	items := workspaceItems(selected)
	switch key {
	case keyJ, keyDown:
		m.workspaceDetailCursor = workspaceMoveCursor(items, m.workspaceDetailCursor, 1)
		m.scrollWorkspaceToSelection(items)
		return m, nil
	case keyK, keyUp:
		m.workspaceDetailCursor = workspaceMoveCursor(items, m.workspaceDetailCursor, -1)
		m.scrollWorkspaceToSelection(items)
		return m, nil
	case keyG:
		m.workspaceDetailCursor = workspaceFirstSelectable(items)
		m.scrollWorkspaceToSelection(items)
		return m, nil
	case keyGG:
		for i := len(items) - 1; i >= 0; i-- {
			if workspaceSelectable(items[i]) {
				m.workspaceDetailCursor = i
				break
			}
		}
		m.scrollWorkspaceToSelection(items)
		return m, nil
	}

	item, ok := m.selectedWorkspaceDetail()
	if !ok {
		return m, nil
	}
	return m.workspacePrimaryAction(item, key)
}

func (m Model) workspacePrimaryAction(item workspaceDetailItem, key string) (tea.Model, tea.Cmd) {
	switch item.kind {
	case workspaceDetailLive:
		return m.workspaceLiveAction(item.live, key)
	case workspaceDetailConversation:
		if key != keyEnter {
			return m, nil
		}
		meta, ok := m.workspaceConversationMeta[transcript.IndexKey(item.conversation.Harness, item.conversation.ID)]
		if !ok {
			cmd := m.workspaceToast("conversation details are still loading")
			return m, cmd
		}
		readers := m.transcriptReaders
		if readers == nil {
			readers = buildTranscriptReaders()
		}
		return m, m.conversationActionCmd(readers, meta, actions.Continue)
	case workspaceDetailPlan:
		if key != keyEnter {
			return m, nil
		}
		return m.enterPlanView(plans.PlanMeta{
			Name:    item.plan.Name,
			DirPath: item.plan.Directory,
			Files:   append([]string(nil), item.plan.Files...),
			Size:    item.plan.Size,
			ModTime: item.plan.ModifiedAt,
			Snippet: item.plan.Snippet,
		})
	case workspaceDetailActivity:
		if key != keyEnter {
			return m, nil
		}
		selected, _ := m.selectedWorkspace()
		for _, live := range selected.LiveSessions {
			if item.activity.WindowID > 0 && live.Session.WindowID == item.activity.WindowID {
				return m, focusCmd(m.client, live.Session.TabID)
			}
		}
		if item.activity.Harness != "" && item.activity.SessionID != "" {
			meta, ok := m.workspaceConversationMeta[transcript.IndexKey(item.activity.Harness, item.activity.SessionID)]
			if ok {
				readers := m.transcriptReaders
				if readers == nil {
					readers = buildTranscriptReaders()
				}
				return m, m.conversationActionCmd(readers, meta, actions.Continue)
			}
		}
		return m, m.workspaceToast("activity has no available live or indexed target")
	}
	return m, nil
}

func (m Model) workspaceLiveAction(running session.RunningSession, key string) (tea.Model, tea.Cmd) {
	switch key {
	case keyEnter, keySpace:
		return m, focusCmd(m.client, running.TabID)
	case keyE:
		return m, peekCmd(m.client, running.WindowID)
	case keyClearStatus:
		if stateGroup(running, m.lastActivity, m.idleThreshold) == 0 {
			return m, m.clearStatusCmd(running)
		}
	case keyLock:
		return m, toggleLockCmd(m.client, running)
	case keyM:
		return m, toggleMuteCmd(m.client, running)
	case keyY:
		return m, copyTitleCmd(runningSessionDisplayTitle(running))
	case keyO:
		if configured, ok := findConfigSessionForRunning(m.cfg, running); ok {
			return m, openDirActionCmd(m, configured)
		}
	case keyOpenEditor:
		if configured, ok := findConfigSessionForRunning(m.cfg, running); ok {
			return m, openInEditorActionCmd(m, configured)
		}
	case keyD:
		return m.enterDetailMode(running.WindowID, false)
	case keyOpenTimeline:
		return m.enterDetailMode(running.WindowID, true)
	case keyA:
		m.targetSession = running
		m.actionsOffset = 0
		m.actionsReturnMode = ModeWorkspace
		m.mode = ModeActions
		return m, nil
	case keyR:
		if running.Locked {
			return m, m.lockedDenyToast()
		}
		m.targetSession = running
		m.confirmAction = "restart"
		m.confirmConsequence = restartConsequence
		m.confirmReturnMode = ModeWorkspace
		m.mode = ModeConfirm
		return m, nil
	case keyClose:
		if running.Locked {
			return m, m.lockedDenyToast()
		}
		m.targetSession = running
		if m.cfg.Defaults.ConfirmDestructive {
			m.confirmAction = "close"
			m.confirmConsequence = closeConsequence
			m.confirmReturnMode = ModeWorkspace
			m.mode = ModeConfirm
			return m, nil
		}
		return m, m.closeWithToast(running)
	case keyC:
		if running.Locked {
			return m, m.lockedDenyToast()
		}
		m.targetSession = running
		m.configPickerAccounts = m.cfg.Accounts
		m.configPickerCurrent = running.EffectiveProfile()
		m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, running.EffectiveProfile())
		m.configPickerPurpose = ConfigPickerRestartProfile
		m.configPickerReturnMode = ModeWorkspace
		m.configPickerProjectIdx = -1
		m.mode = ModeConfigPicker
		return m, nil
	case keyDashboardLaunch:
		m.targetSession = running
		m.configPickerAccounts = m.cfg.Accounts
		m.configPickerCurrent = running.EffectiveProfile()
		m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, running.EffectiveProfile())
		m.configPickerPurpose = ConfigPickerDashboardLaunch
		m.configPickerReturnMode = ModeWorkspace
		m.configPickerProjectIdx = -1
		m.mode = ModeConfigPicker
		return m, nil
	}
	return m, nil
}

func (m *Model) workspaceToast(text string) tea.Cmd {
	return m.pushToast(Toast{Severity: toastInfo, Text: text, Expires: time.Now().Add(2 * time.Second)})
}

func renderWorkspaceHome(m Model) string {
	var sb strings.Builder
	sb.WriteString(renderHeader(m))
	if m.workspaceLoadErr != nil {
		sb.WriteString(styleError.Render("Workspace load: "+m.workspaceLoadErr.Error()) + "\n")
	}
	if m.workspaceLoading && len(m.workspaceSnapshot.Workspaces) == 0 {
		sb.WriteString(styleDim.Render("Loading project workspaces…") + "\n")
		return sb.String()
	}
	if len(m.workspaceSnapshot.Workspaces) == 0 {
		if m.workspaceLoadErr != nil {
			sb.WriteString(styleDim.Render("Workspace data is unavailable; retrying in the background.") + "\n")
			return sb.String()
		}
		sb.WriteString(styleHeader.Render("Workspaces") + "\n")
		sb.WriteString(styleDim.Render("No configured projects. Add [[projects]] entries to start a workspace.") + "\n")
		return sb.String()
	}
	m.clampWorkspaceSelection()
	if workspaceUsesSplit(m) && !m.workspaceDrillDown {
		sb.WriteString(renderWorkspaceSplit(m))
	} else if m.workspaceDrillDown || m.workspaceFocus == workspaceFocusDetail {
		sb.WriteString(renderWorkspaceDrillDown(m))
	} else {
		sb.WriteString(renderWorkspaceProjects(m, m.width, true))
	}
	return sb.String()
}

func renderWorkspaceSplit(m Model) string {
	leftWidth := m.width / 3
	if leftWidth < 28 {
		leftWidth = 28
	}
	if leftWidth > 38 {
		leftWidth = 38
	}
	rightWidth := m.width - leftWidth - 3
	left := strings.Split(strings.TrimRight(renderWorkspaceProjects(m, leftWidth, false), "\n"), "\n")
	right := strings.Split(strings.TrimRight(renderWorkspaceDetails(m, rightWidth, false), "\n"), "\n")
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	var lines []string
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		l = displayStyledTruncate(l, leftWidth, "…")
		pad := leftWidth - displayStyledWidth(l)
		if pad < 0 {
			pad = 0
		}
		line := l + strings.Repeat(" ", pad) + styleDim.Render(" │ ") + displayStyledTruncate(r, rightWidth, "…")
		lines = append(lines, displayStyledTruncate(line, m.width, "…"))
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderWorkspaceProjects(m Model, width int, full bool) string {
	header := "Workspaces"
	if full {
		header += "  ·  Enter for details  ·  v for live sessions"
	}
	lines := []string{styleHeader.Render(displayTruncate(header, width, "…"))}
	visible := workspacePaneHeight(m)
	start := m.workspaceProjectOffset
	end := start + visible
	if end > len(m.workspaceSnapshot.Workspaces) {
		end = len(m.workspaceSnapshot.Workspaces)
	}
	for i := start; i < end; i++ {
		item := m.workspaceSnapshot.Workspaces[i]
		prefix := "  "
		if i == m.workspaceCursor {
			prefix = "› "
		}
		label := workspaceProjectLabel(item)
		line := prefix + label
		if i == m.workspaceCursor && m.workspaceFocus == workspaceFocusProjects {
			line = styleSelected.Render(displayTruncate(line, width, "…"))
		} else {
			line = displayTruncate(line, width, "…")
		}
		lines = append(lines, line)
	}
	if end < len(m.workspaceSnapshot.Workspaces) {
		lines = append(lines, styleDim.Render("  ▼ more workspaces"))
	}
	return strings.Join(lines, "\n") + "\n"
}

func workspaceProjectLabel(item workspace.WorkspaceSnapshot) string {
	state := "idle"
	switch item.Urgency {
	case workspace.UrgencyAttention:
		state = "attention"
	case workspace.UrgencyActive:
		state = "active"
	case workspace.UrgencyRecent:
		state = "recent"
	}
	counts := make([]string, 0, 3)
	if n := len(item.LiveSessions); n > 0 {
		counts = append(counts, fmt.Sprintf("%d live", n))
	}
	if n := len(item.Conversations); n > 0 {
		counts = append(counts, fmt.Sprintf("%d recent", n))
	}
	if n := len(item.Plans); n > 0 {
		counts = append(counts, fmt.Sprintf("%d plans", n))
	}
	if item.UnreadCount > 0 {
		counts = append(counts, fmt.Sprintf("%d unread", item.UnreadCount))
	}
	if len(counts) == 0 {
		return item.DisplayName + "  ·  " + state
	}
	return item.DisplayName + "  ·  " + state + "  ·  " + strings.Join(counts, ", ")
}

func renderWorkspaceDrillDown(m Model) string {
	item, _ := m.selectedWorkspace()
	lines := []string{styleHeader.Render(displayTruncate("‹ Workspaces  ·  "+item.DisplayName, m.width, "…"))}
	lines = append(lines, strings.Split(strings.TrimRight(renderWorkspaceDetails(m, m.width, true), "\n"), "\n")...)
	return strings.Join(lines, "\n") + "\n"
}

func renderWorkspaceDetails(m Model, width int, full bool) string {
	item, ok := m.selectedWorkspace()
	if !ok {
		return styleDim.Render("Select a workspace") + "\n"
	}
	title := item.DisplayName
	if full {
		title = "Workspace details  ·  " + title
	}
	lines := []string{styleHeader.Render(displayTruncate(title, width, "…"))}
	items := workspaceItems(item)
	visible := workspacePaneHeight(m)
	start := m.workspaceDetailOffset
	end := start + visible
	if end > len(items) {
		end = len(items)
	}
	if start > 0 {
		lines = append(lines, styleDim.Render("  ▲ more"))
	}
	for i := start; i < end; i++ {
		line := workspaceDetailLine(items[i])
		if i == m.workspaceDetailCursor && workspaceSelectable(items[i]) && (m.workspaceFocus == workspaceFocusDetail || m.workspaceDrillDown) {
			line = styleSelected.Render(displayTruncate("› "+line, width, "…"))
		} else if items[i].kind == workspaceDetailSection {
			line = styleHeader.Render(displayTruncate(line, width, "…"))
		} else {
			line = displayTruncate("  "+line, width, "…")
		}
		lines = append(lines, line)
	}
	if end < len(items) {
		lines = append(lines, styleDim.Render("  ▼ more"))
	}
	return strings.Join(lines, "\n") + "\n"
}

func workspaceDetailLine(item workspaceDetailItem) string {
	switch item.kind {
	case workspaceDetailSection:
		return item.label
	case workspaceDetailLive:
		state := item.live.State
		if state == "" {
			state = "idle"
		}
		name := item.live.Base
		if name == "" {
			name = runningSessionDisplayTitle(item.live)
		}
		line := fmt.Sprintf("%s  ·  %s  ·  %s", name, item.live.EffectiveProfile(), state)
		if item.worktreePath != "" {
			line += "  ·  " + filepath.Base(item.worktreePath)
		}
		return line
	case workspaceDetailConversation:
		when := workspaceRelativeTime(item.conversation.LastAt)
		label := item.conversation.Project
		if label == "" {
			label = item.conversation.ID
		}
		return fmt.Sprintf("%s  ·  %s  ·  %s", label, item.conversation.Profile, when)
	case workspaceDetailPlan:
		return item.plan.Name
	case workspaceDetailActivity:
		marker := ""
		if item.activity.Unread {
			marker = "● "
		}
		return fmt.Sprintf("%s%s  ·  %s", marker, item.activity.Summary, workspaceRelativeTime(item.activity.OccurredAt))
	default:
		return item.label
	}
}

func workspaceRelativeTime(at time.Time) string {
	if at.IsZero() {
		return "unknown time"
	}
	d := time.Since(at)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
