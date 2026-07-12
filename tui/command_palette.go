package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type paletteEntryKind uint8

const (
	paletteEntryAction paletteEntryKind = iota
	paletteEntryWorkspace
	paletteEntryLive
	paletteEntryConversation
	paletteEntryPlan
	paletteEntrySearch
)

type paletteEntry struct {
	kind         paletteEntryKind
	key          string
	label        string
	description  string
	search       []string
	score        int
	actionID     actions.ID
	definition   actions.Definition
	request      actions.Request
	availability actions.Availability
	navigate     func(Model) (tea.Model, tea.Cmd)
}

func (k paletteEntryKind) label() string {
	switch k {
	case paletteEntryAction:
		return "action"
	case paletteEntryWorkspace:
		return "workspace"
	case paletteEntryLive:
		return "live"
	case paletteEntryConversation:
		return "conversation"
	case paletteEntryPlan:
		return "plan"
	case paletteEntrySearch:
		return "search"
	default:
		return "target"
	}
}

// paletteCanOpen keeps ':' available everywhere that is not actively
// accepting free-form text. Navigation modes and non-text modals are safe:
// paletteReturnMode restores them byte-for-byte because their state remains in
// the root model while the palette is visible.
func (m Model) paletteCanOpen() bool {
	switch m.mode {
	case ModePalette, ModeSearch:
		return false
	case ModePicker:
		return !m.pickerSearchMode
	case ModeSessionSearch:
		return !m.sessionSearchMode
	case ModePlanSearch:
		return !m.planSearchMode
	case ModeUnifiedSearch:
		return !m.unifiedInput
	case ModeChipPicker:
		return !m.chipPickerSearchMode
	default:
		return true
	}
}

func (m Model) enterPalette() Model {
	m.clearJumpChord()
	m.paletteReturnMode = m.mode
	m.paletteQuery = ""
	m.paletteCursor = 0
	m.paletteOffset = 0
	m.paletteMessage = ""
	m.mode = ModePalette
	return m
}

func (m *Model) exitPalette() {
	returnMode := m.paletteReturnMode
	if returnMode == ModePalette {
		returnMode = ModeList
	}
	m.mode = returnMode
	m.paletteQuery = ""
	m.paletteCursor = 0
	m.paletteOffset = 0
	m.paletteReturnMode = ModeList
	m.paletteMessage = ""
}

func (m Model) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := paletteEntries(m)
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.exitPalette()
		return m, nil
	case keyBackspace:
		if r := []rune(m.paletteQuery); len(r) > 0 {
			m.paletteQuery = string(r[:len(r)-1])
			m.resetPaletteSelection()
		}
		return m, nil
	case keyJ, keyDown:
		m.paletteCursor = clamp(m.paletteCursor+1, 0, len(entries)-1)
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyK, keyUp:
		m.paletteCursor = clamp(m.paletteCursor-1, 0, len(entries)-1)
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyG:
		m.paletteCursor = 0
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyGG:
		m.paletteCursor = len(entries) - 1
		if m.paletteCursor < 0 {
			m.paletteCursor = 0
		}
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyPgDown, keyCtrlF:
		m.paletteCursor = clamp(m.paletteCursor+paletteVisibleRows(m), 0, len(entries)-1)
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyPgUp, keyCtrlB:
		m.paletteCursor = clamp(m.paletteCursor-paletteVisibleRows(m), 0, len(entries)-1)
		m.scrollPaletteToCursor(len(entries))
		return m, nil
	case keyEnter:
		if m.paletteCursor < 0 || m.paletteCursor >= len(entries) {
			return m, nil
		}
		return m.runPaletteEntry(entries[m.paletteCursor])
	default:
		if text := msg.Key().Text; text != "" {
			m.paletteQuery += text
			m.resetPaletteSelection()
		}
		return m, nil
	}
}

func (m *Model) resetPaletteSelection() {
	m.paletteCursor = 0
	m.paletteOffset = 0
	m.paletteMessage = ""
}

func (m *Model) scrollPaletteToCursor(total int) {
	visible := paletteVisibleRows(*m)
	if m.paletteCursor < m.paletteOffset {
		m.paletteOffset = m.paletteCursor
	}
	if m.paletteCursor >= m.paletteOffset+visible {
		m.paletteOffset = m.paletteCursor - visible + 1
	}
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	m.paletteOffset = clamp(m.paletteOffset, 0, maxOffset)
}

func paletteEntries(m Model) []paletteEntry {
	request := paletteContextRequest(m)
	entries := make([]paletteEntry, 0, len(sharedActionRegistry.Definitions())+16)
	for _, definition := range sharedActionRegistry.Definitions() {
		availability := sharedActionRegistry.Availability(paletteDependencies(m), definition.ID, request)
		entries = append(entries, paletteEntry{
			kind:         paletteEntryAction,
			key:          "action:" + string(definition.ID),
			label:        definition.Label,
			description:  definition.Description,
			search:       []string{definition.Label, string(definition.ID), definition.Description},
			actionID:     definition.ID,
			definition:   definition,
			request:      request,
			availability: availability,
		})
	}

	entries = append(entries, paletteNavigationEntries(m)...)
	query := strings.TrimSpace(m.paletteQuery)
	if query == "" {
		return entries
	}
	filtered := entries[:0]
	for _, entry := range entries {
		score, ok := paletteBestScore(query, append(entry.search, entry.kind.label()))
		if !ok {
			continue
		}
		entry.score = score
		filtered = append(filtered, entry)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].score != filtered[j].score {
			return filtered[i].score > filtered[j].score
		}
		if filtered[i].kind != filtered[j].kind {
			return filtered[i].kind < filtered[j].kind
		}
		if c := strings.Compare(strings.ToLower(filtered[i].label), strings.ToLower(filtered[j].label)); c != 0 {
			return c < 0
		}
		return filtered[i].key < filtered[j].key
	})
	return filtered
}

// paletteBestScore implements deterministic case-insensitive subsequence
// matching. An exact match wins, followed by an exact prefix, a contiguous
// substring, and finally a sparse subsequence. Shorter spans and earlier
// starts break ties before the stable entry ordering does.
func paletteBestScore(query string, fields []string) (int, bool) {
	best := 0
	matched := false
	for _, field := range fields {
		if score, ok := paletteSubsequenceScore(query, field); ok && (!matched || score > best) {
			best, matched = score, true
		}
	}
	return best, matched
}

func paletteSubsequenceScore(query, candidate string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	candidate = strings.ToLower(candidate)
	if query == "" {
		return 1, true
	}
	if candidate == query {
		return 4_000_000 - len(candidate), true
	}
	if strings.HasPrefix(candidate, query) {
		return 3_000_000 - len(candidate), true
	}
	if start := strings.Index(candidate, query); start >= 0 {
		return 2_000_000 - start*100 - len(candidate), true
	}

	queryRunes := []rune(query)
	candidateRunes := []rune(candidate)
	qi, first, last := 0, -1, -1
	for i, r := range candidateRunes {
		if qi >= len(queryRunes) || r != queryRunes[qi] {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		qi++
	}
	if qi != len(queryRunes) {
		return 0, false
	}
	span := last - first + 1
	gaps := span - len(queryRunes)
	return 1_000_000 - gaps*100 - first*10 - span, true
}

func paletteContextRequest(m Model) actions.Request {
	var request actions.Request
	originMode := m.paletteReturnMode
	mode := originMode
	switch originMode {
	case ModeActions, ModeConfirm, ModeConfigPicker:
		if m.targetSession.TabID != 0 || m.targetSession.WindowID != 0 {
			paletteApplyRunningContext(m, m.targetSession, &request)
		}
		return request
	case ModeDetail:
		if running, ok := paletteRunningSessionForWindow(m, m.detailWindowID); ok {
			paletteApplyRunningContext(m, running, &request)
		}
		return request
	case ModeHelp:
		mode = m.helpReturnMode
	}

	switch mode {
	case ModeWorkspace:
		if selected, ok := m.selectedWorkspace(); ok {
			workspaceTarget := actions.WorkspaceTarget{ID: selected.ID, LoadedThrough: selected.ActivityLoadedThrough}
			request.Workspace = &workspaceTarget
			if project, ok := paletteWorkspaceProject(m, selected.DisplayName, selected.DefaultLaunch); ok {
				projectCopy := project
				request.Project = &projectCopy
			}
			if m.workspaceFocus == workspaceFocusDetail || m.workspaceDrillDown {
				if item, ok := m.selectedWorkspaceDetail(); ok {
					paletteApplyWorkspaceItemContext(m, item, &request)
				}
			}
		}
	case ModeList:
		if m.cursor >= 0 && m.cursor < len(m.filtered) {
			paletteApplyRunningContext(m, m.filtered[m.cursor], &request)
		}
	case ModePicker:
		if m.pickerCursor >= 0 && m.pickerCursor < len(m.pickerFilteredIdx) {
			idx := m.pickerFilteredIdx[m.pickerCursor]
			if idx >= 0 && idx < len(m.pickerSessions) {
				project := m.pickerSessionWithResolvedProfile(idx)
				request.Project = &project
			}
		}
	case ModeSessionSearch, ModeChipPicker:
		if m.sessionCursor >= 0 && m.sessionCursor < len(m.sessionResults) {
			meta := m.sessionResults[m.sessionCursor].Meta
			paletteApplyConversationContext(m, meta, &request)
		}
	case ModeSessionView, ModeMessageDetail:
		if m.viewerSession != nil {
			meta := *m.viewerSession
			paletteApplyConversationContext(m, meta, &request)
		}
	case ModeDeleteChoice:
		if m.deleteTarget != nil {
			meta := *m.deleteTarget
			paletteApplyConversationContext(m, meta, &request)
		}
	}
	return request
}

func paletteRunningSessionForWindow(m Model, windowID int) (session.RunningSession, bool) {
	for _, running := range m.sessions {
		if running.WindowID == windowID {
			return running, true
		}
	}
	for _, workspaceItem := range m.workspaceSnapshot.Workspaces {
		for _, live := range workspaceItem.LiveSessions {
			if live.Session.WindowID == windowID {
				return live.Session, true
			}
		}
	}
	return session.RunningSession{}, false
}

func paletteApplyWorkspaceItemContext(m Model, item workspaceDetailItem, request *actions.Request) {
	switch item.kind {
	case workspaceDetailLive:
		paletteApplyRunningContext(m, item.live, request)
	case workspaceDetailConversation:
		if meta, ok := m.workspaceConversationMeta[transcript.IndexKey(item.conversation.Harness, item.conversation.ID)]; ok {
			metaCopy := meta
			request.Conversation = &metaCopy
		}
	}
}

func paletteApplyRunningContext(m Model, running session.RunningSession, request *actions.Request) {
	runningCopy := running
	request.Session = &runningCopy
	if project, ok := findConfigSessionForRunning(m.cfg, running); ok {
		projectCopy := project
		request.Project = &projectCopy
	}
}

func paletteApplyConversationContext(m Model, meta transcript.SessionMeta, request *actions.Request) {
	metaCopy := meta
	request.Conversation = &metaCopy
	if m.cfg == nil {
		return
	}
	projects := m.cfg.Projects
	if len(projects) == 0 {
		projects = m.cfg.Sessions
	}
	for _, project := range projects {
		if project.Title == meta.Project && strings.EqualFold(project.EffectiveProfile(), meta.DisplayProfile()) {
			projectCopy := project
			request.Project = &projectCopy
			return
		}
	}
}

func paletteWorkspaceProject(m Model, title string, preferred config.Session) (config.Session, bool) {
	if preferred.Title != "" || preferred.Directory != "" {
		return preferred, true
	}
	if m.cfg == nil {
		return config.Session{}, false
	}
	projects := m.cfg.Projects
	if len(projects) == 0 {
		projects = m.cfg.Sessions
	}
	for _, project := range projects {
		if project.Title == title || (project.ID != "" && strings.EqualFold(project.ID, title)) {
			return project, true
		}
	}
	return config.Session{}, false
}

func paletteDependencies(m Model) actions.Dependencies {
	readers := m.transcriptReaders
	if readers == nil {
		readers = buildTranscriptReaders()
	}
	return actions.Dependencies{
		Kitty:     m.client,
		Config:    m.cfg,
		Clipboard: copyToClipboard,
		Daemon:    m.actionDaemon(),
		Readers:   readers,
		Activity:  m.activityMarker(),
	}
}

func paletteNavigationEntries(m Model) []paletteEntry {
	entries := []paletteEntry{{
		kind: paletteEntrySearch, key: "navigation:unified-search",
		label: "Unified search", description: "search all Agent Mux knowledge",
		search: []string{"unified search", "full text", "projects conversations plans handoffs activity"},
		navigate: func(model Model) (tea.Model, tea.Cmd) {
			return model.enterUnifiedSearch()
		},
	}}
	seenLive := make(map[int]bool)
	seenConversation := make(map[string]bool)
	seenPlan := make(map[string]bool)

	for wi := range m.workspaceSnapshot.Workspaces {
		workspaceIndex := wi
		workspaceItem := m.workspaceSnapshot.Workspaces[wi]
		entries = append(entries, paletteEntry{
			kind: paletteEntryWorkspace, key: "workspace:" + workspaceItem.ID,
			label: workspaceItem.DisplayName, description: workspaceItem.CanonicalPath,
			search: []string{workspaceItem.DisplayName, workspaceItem.ID, workspaceItem.CanonicalPath, strings.Join(workspaceItem.Aliases, " ")},
			navigate: func(model Model) (tea.Model, tea.Cmd) {
				model.mode = ModeWorkspace
				model.workspaceCursor = workspaceIndex
				model.workspaceFocus = workspaceFocusProjects
				model.workspaceDrillDown = false
				model.clampWorkspaceSelection()
				return model, nil
			},
		})

		for _, live := range workspaceItem.LiveSessions {
			liveCopy := live.Session
			seenLive[liveCopy.WindowID] = true
			entries = append(entries, paletteWorkspaceDetailNavigation(
				paletteEntryLive,
				fmt.Sprintf("live:%d", liveCopy.WindowID),
				runningSessionDisplayTitle(liveCopy),
				fmt.Sprintf("%s · %s", workspaceItem.DisplayName, liveCopy.EffectiveProfile()),
				[]string{liveCopy.Base, liveCopy.EffectiveProject(), liveCopy.EffectiveProfile(), workspaceItem.DisplayName},
				workspaceIndex,
				func(item workspaceDetailItem) bool {
					return item.kind == workspaceDetailLive && item.live.WindowID == liveCopy.WindowID
				},
			))
		}
		for _, conversation := range workspaceItem.Conversations {
			conversationCopy := conversation
			key := transcript.IndexKey(conversationCopy.Harness, conversationCopy.ID)
			seenConversation[key] = true
			label := conversationCopy.Project
			if label == "" {
				label = conversationCopy.ID
			}
			entries = append(entries, paletteWorkspaceDetailNavigation(
				paletteEntryConversation,
				"conversation:"+key,
				label,
				fmt.Sprintf("%s · %s/%s", workspaceItem.DisplayName, conversationCopy.Harness, conversationCopy.ID),
				[]string{conversationCopy.ID, conversationCopy.Harness, conversationCopy.Profile, conversationCopy.Project, conversationCopy.Snippet},
				workspaceIndex,
				func(item workspaceDetailItem) bool {
					return item.kind == workspaceDetailConversation && item.conversation.Harness == conversationCopy.Harness && item.conversation.ID == conversationCopy.ID
				},
			))
		}
		for _, plan := range workspaceItem.Plans {
			planCopy := plan
			planKey := planCopy.Directory
			if planKey == "" {
				planKey = planCopy.ID
			}
			seenPlan[planKey] = true
			entries = append(entries, paletteWorkspaceDetailNavigation(
				paletteEntryPlan,
				"plan:"+planKey,
				planCopy.Name,
				fmt.Sprintf("%s · %s", workspaceItem.DisplayName, planCopy.Directory),
				[]string{planCopy.Name, planCopy.Directory, planCopy.Snippet},
				workspaceIndex,
				func(item workspaceDetailItem) bool {
					return item.kind == workspaceDetailPlan && item.plan.Directory == planCopy.Directory
				},
			))
		}
	}

	for i := range m.sessions {
		if seenLive[m.sessions[i].WindowID] {
			continue
		}
		running := m.sessions[i]
		entries = append(entries, paletteEntry{
			kind: paletteEntryLive, key: fmt.Sprintf("live:%d", running.WindowID),
			label: runningSessionDisplayTitle(running), description: running.EffectiveProfile(),
			search: []string{running.Base, running.EffectiveProject(), running.EffectiveProfile(), running.Directory},
			navigate: func(model Model) (tea.Model, tea.Cmd) {
				model.mode = ModeList
				model.cursor = 0
				for filteredIndex := range model.filtered {
					if model.filtered[filteredIndex].WindowID == running.WindowID {
						model.cursor = filteredIndex
						break
					}
				}
				scrollListToCursor(&model)
				return model, nil
			},
		})
	}
	for i := range m.sessionResults {
		meta := m.sessionResults[i].Meta
		key := transcript.IndexKey(meta.Harness, meta.ID)
		if seenConversation[key] {
			continue
		}
		index := i
		label := meta.Project
		if label == "" {
			label = meta.ID
		}
		entries = append(entries, paletteEntry{
			kind: paletteEntryConversation, key: "conversation:" + key,
			label: label, description: fmt.Sprintf("%s/%s", meta.Harness, meta.ID),
			search: []string{meta.ID, meta.Harness, meta.DisplayProfile(), meta.Project, meta.Snippet},
			navigate: func(model Model) (tea.Model, tea.Cmd) {
				model.mode = ModeSessionSearch
				model.sessionSearchMode = false
				model.sessionCursor = index
				scrollSessionSearchToCursor(&model)
				return model, nil
			},
		})
	}
	for i := range m.planResults {
		meta := m.planResults[i].Meta
		if seenPlan[meta.DirPath] {
			continue
		}
		index := i
		entries = append(entries, paletteEntry{
			kind: paletteEntryPlan, key: "plan:" + plans.PlanKey(meta.DirPath),
			label: meta.Name, description: meta.DirPath,
			search: []string{meta.Name, meta.DirPath, meta.Snippet},
			navigate: func(model Model) (tea.Model, tea.Cmd) {
				model.mode = ModePlanSearch
				model.planSearchMode = false
				model.planCursor = index
				scrollPlanSearchToCursor(&model)
				return model, nil
			},
		})
	}
	return entries
}

func paletteWorkspaceDetailNavigation(kind paletteEntryKind, key, label, description string, search []string, workspaceIndex int, matches func(workspaceDetailItem) bool) paletteEntry {
	return paletteEntry{
		kind: kind, key: key, label: label, description: description, search: search,
		navigate: func(model Model) (tea.Model, tea.Cmd) {
			model.mode = ModeWorkspace
			model.workspaceCursor = workspaceIndex
			model.workspaceFocus = workspaceFocusDetail
			model.workspaceDrillDown = !workspaceUsesSplit(model)
			if workspaceIndex >= 0 && workspaceIndex < len(model.workspaceSnapshot.Workspaces) {
				for i, item := range workspaceItems(model.workspaceSnapshot.Workspaces[workspaceIndex]) {
					if matches(item) {
						model.workspaceDetailCursor = i
						break
					}
				}
			}
			model.clampWorkspaceSelection()
			return model, nil
		},
	}
}

func (m Model) runPaletteEntry(entry paletteEntry) (tea.Model, tea.Cmd) {
	if entry.kind != paletteEntryAction {
		if entry.navigate == nil {
			return m, nil
		}
		return entry.navigate(m)
	}
	if !entry.availability.Available {
		m.paletteMessage = entry.availability.Reason
		return m, nil
	}

	request := entry.request
	returnMode := m.paletteReturnMode
	if entry.actionID == actions.Launch && request.Project != nil {
		project := *request.Project
		m.targetSession = session.RunningSession{
			Base: project.Title, Project: project.Title, Directory: project.Directory,
			Profile: project.EffectiveProfile(), Account: project.EffectiveProfile(),
		}
		m.configPickerAccounts = append([]config.Account(nil), m.cfg.Accounts...)
		m.configPickerCurrent = project.EffectiveProfile()
		m.configPickerCursor = pickerAccountIndex(m.configPickerAccounts, project.EffectiveProfile())
		m.configPickerPurpose = ConfigPickerDashboardLaunch
		m.configPickerReturnMode = returnMode
		m.configPickerProjectIdx = -1
		m.paletteMessage = ""
		m.mode = ModeConfigPicker
		return m, nil
	}

	if entry.definition.Confirmation.Required {
		m.confirmReturnMode = returnMode
		switch entry.actionID {
		case actions.Close:
			if request.Session == nil {
				m.paletteMessage = "a running session is required"
				return m, nil
			}
			m.targetSession = *request.Session
			m.confirmAction = "close"
			m.confirmConsequence = closeConsequence
		case actions.Restart:
			if request.Session == nil {
				m.paletteMessage = "a running session is required"
				return m, nil
			}
			m.targetSession = *request.Session
			m.confirmAction = "restart"
			m.confirmConsequence = restartConsequence
		case actions.ForkWorktree:
			if request.Conversation == nil {
				m.paletteMessage = "an indexed conversation is required"
				return m, nil
			}
			meta := *request.Conversation
			m.isolatedForkConversation = &meta
			m.targetSession = session.RunningSession{Base: meta.Project, Project: meta.Project, Directory: meta.CWD, Profile: meta.DisplayProfile(), Account: meta.DisplayProfile(), Harness: meta.Harness}
			m.confirmAction = "fork worktree"
			m.confirmConsequence = worktreeForkConsequence
		default:
			m.paletteMessage = "this action requires an unsupported confirmation"
			return m, nil
		}
		m.mode = ModeConfirm
		return m, nil
	}

	m.mode = returnMode
	m.paletteMessage = ""
	if entry.actionID == actions.MarkRead {
		return m, m.markSelectedWorkspaceRead()
	}
	if entry.actionID == actions.MarkAllRead {
		return m, m.markAllWorkspaceActivityRead()
	}
	return m, paletteActionCmd(m, entry.actionID, request)
}

func paletteActionCmd(m Model, id actions.ID, request actions.Request) tea.Cmd {
	deps := paletteDependencies(m)
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		_, err := sharedActionRegistry.Execute(ctx, deps, id, request)
		switch id {
		case actions.Focus:
			return focusDoneMsg{err: err}
		case actions.OpenDirectory, actions.OpenEditor:
			return pickerOpenDoneMsg{err: err}
		default:
			return actionDoneMsg{err: err}
		}
	}
}

func renderPaletteBase(m Model) string {
	mode := paletteUnderlyingMode(m)
	switch mode {
	case ModeWorkspace:
		return renderWorkspaceHome(m)
	case ModePicker:
		return renderPicker(m)
	case ModeSessionSearch, ModeDeleteChoice, ModeChipPicker:
		return renderSessionSearch(m)
	case ModeSessionView:
		return renderSessionView(m)
	case ModeMessageDetail:
		return renderMessageDetail(m)
	case ModePlanSearch:
		return renderPlanSearch(m)
	case ModeUnifiedSearch:
		return renderUnifiedSearch(m)
	case ModePlanView:
		return renderPlanView(m)
	default:
		return renderDashboard(m)
	}
}

func paletteUnderlyingMode(m Model) Mode {
	switch m.paletteReturnMode {
	case ModeActions:
		return m.actionsReturnMode
	case ModeConfirm:
		return m.confirmReturnMode
	case ModeConfigPicker:
		return m.configPickerReturnMode
	case ModeHelp:
		return m.helpReturnMode
	case ModeDetail:
		return m.detailReturnMode
	default:
		return m.paletteReturnMode
	}
}

func paletteVisibleRows(m Model) int {
	rows := rootBodyHeight(m) - styleOverlayBox.GetVerticalFrameSize() - 8
	if rows < 1 {
		return 1
	}
	return rows
}

func renderPaletteBox(m Model) string {
	entries := paletteEntries(m)
	visible := paletteVisibleRows(m)
	offset := clamp(m.paletteOffset, 0, len(entries)-visible)
	if offset < 0 {
		offset = 0
	}
	end := offset + visible
	if end > len(entries) {
		end = len(entries)
	}

	innerWidth := 84
	if available := m.width - 8; available < innerWidth {
		innerWidth = available
	}
	if innerWidth < 12 {
		innerWidth = 12
	}
	rowWidth := innerWidth - styleOverlayBox.GetHorizontalFrameSize()
	if rowWidth < 1 {
		rowWidth = 1
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Command palette") + "\n")
	query := m.paletteQuery
	if query == "" {
		sb.WriteString(styleDim.Render(": type to filter actions and targets") + "\n")
	} else {
		sb.WriteString(":" + displayTruncate(query, rowWidth-1, "…") + "\n")
	}
	if m.paletteMessage != "" {
		sb.WriteString(styleFooterUnavailable.Render(displayTruncate(m.paletteMessage, rowWidth, "…")) + "\n")
	} else {
		sb.WriteString("\n")
	}
	if offset > 0 {
		sb.WriteString(styleDim.Render(fmt.Sprintf("▲ %d more", offset)) + "\n")
	} else {
		sb.WriteString("\n")
	}
	if len(entries) == 0 {
		sb.WriteString(styleDim.Render("  no matching commands or targets") + "\n")
	} else {
		for i := offset; i < end; i++ {
			entry := entries[i]
			kind := fmt.Sprintf("%-12s", entry.kind.label())
			detail := entry.description
			available := entry.kind != paletteEntryAction || entry.availability.Available
			if !available {
				detail = entry.availability.Reason
			}
			line := fmt.Sprintf("  %s  %s", kind, entry.label)
			if detail != "" {
				line += " — " + detail
			}
			line = displayTruncate(line, rowWidth, "…")
			if !available {
				line = styleFooterUnavailable.Render(line)
			}
			if i == m.paletteCursor {
				line = styleSelected.Render(displayTruncate("›"+line[1:], rowWidth, "…"))
			}
			sb.WriteString(line + "\n")
		}
	}
	if end < len(entries) {
		sb.WriteString(styleDim.Render(fmt.Sprintf("▼ %d more", len(entries)-end)) + "\n")
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString("\n" + styleDim.Render("enter run/open · esc return") + "\n")
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(sb.String(), "\n"))
}
