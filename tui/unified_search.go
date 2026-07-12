package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/session"
)

type unifiedSearchDebounceMsg struct{ seq int }

type unifiedSearchDoneMsg struct {
	seq     int
	results []unifiedsearch.Result
	err     error
}

func (m Model) enterUnifiedSearch() (tea.Model, tea.Cmd) {
	returnMode := m.mode
	if returnMode == ModePalette {
		returnMode = m.paletteReturnMode
	}
	if returnMode == ModeUnifiedSearch || returnMode == ModePalette {
		returnMode = ModeWorkspace
	}
	m.clearJumpChord()
	m.mode = ModeUnifiedSearch
	m.unifiedReturnMode = returnMode
	m.unifiedInput = true
	m.unifiedQuery = ""
	m.unifiedResults = nil
	m.unifiedCursor = 0
	m.unifiedOffset = 0
	m.unifiedLoading = false
	m.unifiedSearchErr = nil
	return m, nil
}

func (m *Model) exitUnifiedSearch() {
	returnMode := m.unifiedReturnMode
	if returnMode == ModeUnifiedSearch || returnMode == ModePalette {
		returnMode = ModeWorkspace
	}
	m.mode = returnMode
	m.unifiedInput = false
	m.unifiedLoading = false
	m.unifiedSearchErr = nil
	m.unifiedReturnMode = ModeWorkspace
	m.pendingUnifiedPlanID = ""
	m.pendingUnifiedConversationID = ""
}

func (m Model) handleUnifiedSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.unifiedInput {
		return m.handleUnifiedSearchTextKey(msg)
	}
	return m.handleUnifiedSearchNavKey(msg)
}

func (m Model) handleUnifiedSearchTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.unifiedInput = false
		return m, nil
	case keyBackspace:
		if runes := []rune(m.unifiedQuery); len(runes) > 0 {
			m.unifiedQuery = string(runes[:len(runes)-1])
			return m, m.dispatchUnifiedSearch()
		}
		return m, nil
	default:
		if text := msg.Key().Text; text != "" {
			m.unifiedQuery += text
			return m, m.dispatchUnifiedSearch()
		}
		return m, nil
	}
}

func (m Model) handleUnifiedSearchNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyQ, keyEsc:
		if m.unifiedQuery != "" {
			m.unifiedQuery = ""
			return m, m.dispatchUnifiedSearch()
		}
		m.exitUnifiedSearch()
		return m, nil
	case keySlash:
		m.unifiedInput = true
		return m, nil
	case keyJ, keyDown:
		m.unifiedCursor = clamp(m.unifiedCursor+1, 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyK, keyUp:
		m.unifiedCursor = clamp(m.unifiedCursor-1, 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyG:
		m.unifiedCursor = 0
		scrollUnifiedSearchToCursor(&m)
	case keyGG:
		m.unifiedCursor = max(0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyCtrlD:
		m.unifiedCursor = clamp(m.unifiedCursor+halfPageStep(unifiedSearchVisibleHeight(m)), 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyCtrlU:
		m.unifiedCursor = clamp(m.unifiedCursor-halfPageStep(unifiedSearchVisibleHeight(m)), 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyPgDown, keyCtrlF:
		m.unifiedCursor = clamp(m.unifiedCursor+fullPageStep(unifiedSearchVisibleHeight(m)), 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyPgUp, keyCtrlB:
		m.unifiedCursor = clamp(m.unifiedCursor-fullPageStep(unifiedSearchVisibleHeight(m)), 0, len(m.unifiedResults)-1)
		scrollUnifiedSearchToCursor(&m)
	case keyEnter:
		return m.openUnifiedSearchResult()
	}
	return m, nil
}

func (m *Model) dispatchUnifiedSearch() tea.Cmd {
	m.unifiedSearchSeq++
	seq := m.unifiedSearchSeq
	m.unifiedLoading = strings.TrimSpace(m.unifiedQuery) != ""
	m.unifiedSearchErr = nil
	if !m.unifiedLoading {
		m.unifiedResults = nil
		m.unifiedCursor = 0
		m.unifiedOffset = 0
		return nil
	}
	return tea.Tick(sessionSearchDebounce, func(time.Time) tea.Msg { return unifiedSearchDebounceMsg{seq: seq} })
}

func (m Model) handleUnifiedSearchDebounce(msg unifiedSearchDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.unifiedSearchSeq {
		return m, nil
	}
	text := m.unifiedQuery
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return m, func() tea.Msg {
		query, err := parseUnifiedSearchQuery(text)
		if err != nil {
			return unifiedSearchDoneMsg{seq: msg.seq, err: err}
		}
		results, err := m.executeUnifiedSearch(ctx, query)
		return unifiedSearchDoneMsg{seq: msg.seq, results: results, err: err}
	}
}

func (m Model) executeUnifiedSearch(ctx context.Context, query unifiedsearch.Query) ([]unifiedsearch.Result, error) {
	if m.dataServices == nil {
		return nil, errors.New("unified search services are not ready")
	}
	snapshot, ready := m.dataServices.Snapshot()
	if !ready || snapshot.unifiedSearch == nil {
		return nil, errors.New("unified search services are not ready")
	}
	return snapshot.unifiedSearch.Search(ctx, query)
}

func (m Model) handleUnifiedSearchDone(msg unifiedSearchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.unifiedSearchSeq {
		return m, nil
	}
	m.unifiedLoading = false
	m.unifiedSearchErr = msg.err
	if msg.err != nil {
		m.unifiedResults = nil
		m.unifiedCursor = 0
		m.unifiedOffset = 0
		return m, nil
	}
	anchor := ""
	if m.unifiedCursor >= 0 && m.unifiedCursor < len(m.unifiedResults) {
		anchor = unifiedResultKey(m.unifiedResults[m.unifiedCursor])
	}
	m.unifiedResults = append([]unifiedsearch.Result(nil), msg.results...)
	m.unifiedCursor = 0
	for i := range m.unifiedResults {
		if unifiedResultKey(m.unifiedResults[i]) == anchor {
			m.unifiedCursor = i
			break
		}
	}
	scrollUnifiedSearchToCursor(&m)
	return m, nil
}

func parseUnifiedSearchQuery(text string) (unifiedsearch.Query, error) {
	var query unifiedsearch.Query
	terms := make([]string, 0)
	for _, token := range strings.Fields(text) {
		key, value, found := strings.Cut(token, ":")
		if !found {
			terms = append(terms, token)
			continue
		}
		key = strings.ToLower(key)
		if value == "" {
			switch key {
			case "type", "workspace", "harness", "profile", "from", "to":
				return unifiedsearch.Query{}, fmt.Errorf("search %s filter requires a value", key)
			}
		}
		switch key {
		case "type":
			entityType, err := unifiedsearch.ParseEntityType(value)
			if err != nil {
				return unifiedsearch.Query{}, err
			}
			query.Types = append(query.Types, entityType)
		case "workspace":
			query.Workspaces = appendFilterValue(query.Workspaces, key, value)
		case "harness":
			query.Harnesses = appendFilterValue(query.Harnesses, key, value)
		case "profile":
			query.Profiles = appendFilterValue(query.Profiles, key, value)
		case "from", "to":
			bound, err := parseUnifiedSearchDate(value, key == "to")
			if err != nil {
				return unifiedsearch.Query{}, err
			}
			if key == "from" {
				query.From = bound
			} else {
				query.To = bound
			}
		default:
			terms = append(terms, token)
		}
	}
	query.Text = strings.Join(terms, " ")
	query.Limit = 100
	if strings.TrimSpace(query.Text) == "" {
		return unifiedsearch.Query{}, errors.New("search text is required in addition to filters")
	}
	return query, nil
}

func appendFilterValue(values []string, key, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return append(values, "")
	}
	return append(values, value)
}

func parseUnifiedSearchDate(value string, endOfDay bool) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("search date filter requires a value")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid search date %q", value)
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return parsed, nil
}

func (m Model) openUnifiedSearchResult() (tea.Model, tea.Cmd) {
	if m.unifiedCursor < 0 || m.unifiedCursor >= len(m.unifiedResults) {
		return m, nil
	}
	result := m.unifiedResults[m.unifiedCursor]
	switch result.EntityType {
	case unifiedsearch.EntityProject, unifiedsearch.EntityActivity:
		workspaceID := result.WorkspaceID
		if workspaceID == "" {
			workspaceID = result.EntityID
		}
		for i := range m.workspaceSnapshot.Workspaces {
			if m.workspaceSnapshot.Workspaces[i].ID == workspaceID {
				m.mode = ModeWorkspace
				m.workspaceCursor = i
				m.workspaceFocus = workspaceFocusProjects
				m.workspaceDrillDown = false
				m.clampWorkspaceSelection()
				return m, nil
			}
		}
		m.unifiedSearchErr = fmt.Errorf("workspace %q is not in the current projection", workspaceID)
	case unifiedsearch.EntityConversation:
		if m.transcriptIndex == nil {
			m.pendingUnifiedConversationID = result.EntityID
			m.unifiedLoading = true
			return m, m.dispatchDataServicesInit()
		}
		meta, found := m.transcriptIndex.Get(result.EntityID)
		if !found {
			m.unifiedSearchErr = fmt.Errorf("conversation %q is not in the transcript index", result.EntityID)
			return m, nil
		}
		if m.transcriptReaders == nil {
			m.transcriptReaders = buildTranscriptReaders()
		}
		return m, paletteActionCmd(m, actions.Continue, actions.Request{Conversation: &meta})
	case unifiedsearch.EntityPlan:
		if m.plansIndex == nil {
			m.pendingUnifiedPlanID = result.EntityID
			m.unifiedLoading = true
			return m, m.dispatchDataServicesInit()
		}
		meta, found := m.plansIndex.Get(result.EntityID)
		if !found {
			m.unifiedSearchErr = fmt.Errorf("plan %q is not in the plan index", result.EntityID)
			return m, nil
		}
		return m.enterPlanView(meta)
	case unifiedsearch.EntityHandoff:
		if strings.TrimSpace(result.Target) == "" {
			m.unifiedSearchErr = fmt.Errorf("handoff %q has no path", result.EntityID)
			return m, nil
		}
		return m, func() tea.Msg {
			return planEditorErrMsg{err: session.OpenPathInEditor(m.client, result.Target, "handoff: "+result.Title, m.cfg)}
		}
	}
	return m, nil
}

func unifiedResultKey(result unifiedsearch.Result) string {
	return string(result.EntityType) + ":" + result.EntityID
}

func unifiedSearchVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	height := rootBodyHeight(m) - 4
	if m.unifiedSearchErr != nil {
		height--
	}
	if height < 1 {
		return 1
	}
	return height
}

func scrollUnifiedSearchToCursor(m *Model) {
	total := len(m.unifiedResults)
	height := unifiedSearchVisibleHeight(*m)
	if total == 0 || height <= 0 {
		m.unifiedOffset = 0
		return
	}
	m.unifiedCursor = clamp(m.unifiedCursor, 0, total-1)
	if m.unifiedCursor < m.unifiedOffset {
		m.unifiedOffset = m.unifiedCursor
	}
	if m.unifiedCursor >= m.unifiedOffset+height {
		m.unifiedOffset = m.unifiedCursor - height + 1
	}
	maxOffset := max(0, total-height)
	m.unifiedOffset = clamp(m.unifiedOffset, 0, maxOffset)
}

func renderUnifiedSearch(m Model) string {
	var builder strings.Builder
	header := "Unified Search"
	if m.unifiedLoading {
		header += "  " + styleDim.Render("(searching…)")
	} else {
		header += "  " + styleDim.Render(fmt.Sprintf("[%d result%s]", len(m.unifiedResults), plural(len(m.unifiedResults))))
	}
	builder.WriteString(clampSearchLine(styleHeader.Render(header), m.width))
	builder.WriteByte('\n')
	if m.unifiedInput {
		builder.WriteString(clampSearchLine(styleSearch.Render("/")+m.unifiedQuery+"█", m.width))
	} else if m.unifiedQuery != "" {
		builder.WriteString(clampSearchLine(styleSearch.Render("/")+m.unifiedQuery, m.width))
	} else {
		builder.WriteString(clampSearchLine(styleDim.Render("type to search · filters: type: workspace: harness: profile: from: to:"), m.width))
	}
	builder.WriteString("\n\n")
	if m.unifiedSearchErr != nil {
		builder.WriteString(clampSearchLine(styleError.Render("search error: "+m.unifiedSearchErr.Error()), m.width))
		builder.WriteByte('\n')
	}
	builder.WriteString(renderUnifiedSearchList(m, unifiedSearchVisibleHeight(m)))
	return builder.String()
}

func renderUnifiedSearchList(m Model, height int) string {
	if len(m.unifiedResults) == 0 {
		if m.unifiedLoading {
			return styleDim.Render("  (searching…)") + "\n"
		}
		return styleDim.Render("  (no cross-entity matches)") + "\n"
	}
	window := flatListWindowFor(len(m.unifiedResults), m.unifiedOffset, height)
	var builder strings.Builder
	if window.showTop {
		builder.WriteString(styleDim.Render("  ▲") + "\n")
	}
	for i := window.start; i < window.end; i++ {
		line := formatUnifiedSearchRow(m.unifiedResults[i], m.width)
		if i == m.unifiedCursor {
			line = styleSelected.Render(line)
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	if window.showBottom {
		builder.WriteString(styleDim.Render("  ▼") + "\n")
	}
	return builder.String()
}

func formatUnifiedSearchRow(result unifiedsearch.Result, width int) string {
	when := "          "
	if !result.OccurredAt.IsZero() {
		when = formatSessionWhen(result.OccurredAt)
	}
	title := result.Title
	if title == "" {
		title = result.EntityID
	}
	head := fmt.Sprintf("  %-12s  %s  %s", result.EntityType, when, title)
	remaining := maxRunes(width - displayStyledWidth(head) - 2)
	if remaining > 0 && result.Snippet != "" {
		head += "  " + styleDim.Render(displayTruncate(strings.Join(strings.Fields(result.Snippet), " "), remaining, "…"))
	}
	return displayStyledTruncate(head, width, "…")
}
