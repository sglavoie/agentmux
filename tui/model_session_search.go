// This file is what the root model does about the Past Sessions browser: the
// query, cursor, and scroll it maintains, and the transcript refresh, search,
// and preview commands it issues to keep them answerable.

package tui

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
)

// sessionSearchDebounce is the wait between the user's last keystroke and
// when a content search actually fires. Coarse enough to coalesce rapid
// typing into a single search, fine enough to feel responsive.
const sessionSearchDebounce = 250 * time.Millisecond

// sessionsRefreshDoneMsg carries the result of an Index.Refresh dispatch.
// Always followed by an immediate search dispatch that populates
// sessionResults from the freshly refreshed index.
type sessionsRefreshDoneMsg struct {
	err error
	seq int // sessionSearchSeq snapshot at dispatch time
}

// sessionCacheLoadStartedMsg marks the startup cache read as in-flight. It is
// emitted before the load command so entry during startup joins that work.
type sessionCacheLoadStartedMsg struct{}

// sessionCacheLoadDoneMsg carries the recovering filesystem-cache snapshot
// and its immediate empty-query search. loadErr may be a non-fatal recovery
// warning when idx is non-nil.
type sessionCacheLoadDoneMsg struct {
	seq       int
	idx       *transcript.Index
	readers   map[string]transcript.Reader
	results   []transcript.Result
	loadErr   error
	searchErr error
}

// sessionWarmRefreshDoneMsg carries the background refresh/prune result that
// follows startup cache loading, plus a fresh result set for the query that was
// active when the refresh started.
type sessionWarmRefreshDoneMsg struct {
	seq        int
	results    []transcript.Result
	refreshErr error
	searchErr  error
}

// sessionSearchDebounceMsg fires after sessionSearchDebounce. The handler
// dispatches the actual search iff this message's seq still matches the
// current sessionSearchSeq — otherwise the user kept typing and a fresher
// dispatch is already inbound.
type sessionSearchDebounceMsg struct{ seq int }

// sessionSearchDoneMsg carries the result of a transcript.Search call. seq
// matches the dispatch's sessionSearchSeq snapshot so stale results from
// slow content scans never overwrite a fresher query's output.
type sessionSearchDoneMsg struct {
	seq     int
	results []transcript.Result
	err     error
}

// enterSessionSearch swaps to ModeSessionSearch and reuses any warmed
// sessionResults immediately. If startup cache loading has not produced an
// index yet, entry starts or joins that cache-load path instead of kicking a
// duplicate filesystem refresh.
func (m Model) enterSessionSearch() (tea.Model, tea.Cmd) {
	m.mode = ModeSessionSearch
	m.sessionCursor = 0
	m.sessionOffset = 0
	m.sessionSearchMode = false
	m.sessionRefreshErr = nil
	m.sessionSearchErr = nil

	if m.transcriptIndex != nil && m.transcriptReaders == nil {
		m.transcriptReaders = buildTranscriptReaders()
	}

	if m.transcriptIndex == nil {
		m.sessionLoading = len(m.sessionResults) == 0
		if m.sessionCacheLoadInFlight {
			return m, nil
		}
		m.sessionCacheLoadInFlight = true
		return m, startupSessionWarmupCmd(m.sessionSearchSeq)
	}

	if m.sessionCacheLoadInFlight {
		m.sessionLoading = len(m.sessionResults) == 0
		return m, nil
	}
	if m.sessionRefreshInFlight {
		m.sessionLoading = false
		return m, nil
	}

	m.sessionLoading = false
	m.sessionRefreshInFlight = true
	return m, m.runSessionWarmRefreshCmd(m.transcriptIndex, m.cfg, m.transcriptReaders, m.sessionSearchSeq, m.currentSessionQuery())
}

// enterAllSessionSearch opens history across all projects, newest first. A
// previous project search must not narrow this entry point or publish its
// pending results after the filters have been cleared.
func (m Model) enterAllSessionSearch() (tea.Model, tea.Cmd) {
	if sessionQueryIsEmpty(m.currentSessionQuery()) {
		return m.enterSessionSearch()
	}
	m.sessionQuery = ""
	m.filterHarness = nil
	m.filterProfile = nil
	m.filterProject = nil
	m.filterMessageSource = ""
	m.sessionResults = nil
	m.sessionSearchSeq++

	updated, cmd := m.enterSessionSearch()
	m = updated.(Model)
	if m.transcriptIndex == nil || m.sessionCacheLoadInFlight {
		return m, cmd
	}
	// Search the ready cache while the filesystem refresh runs. Advancing the
	// generation also makes any in-flight warm refresh search the latest query
	// again on completion, so a pre-refresh search cannot leave stale rows.
	searchCmd := m.dispatchSessionSearch()
	return m, tea.Batch(cmd, searchCmd)
}

// enterSessionSearchForProject resets Past Sessions to a single project chip
// and uses the normal entry/cache path before dispatching a filtered search
// when the transcript cache is already available.
func (m Model) enterSessionSearchForProject(project string) (tea.Model, tea.Cmd) {
	if project == "" {
		return m, nil
	}
	m.sessionQuery = ""
	m.filterHarness = nil
	m.filterProfile = nil
	m.filterProject = []string{project}
	m.filterMessageSource = ""

	updated, cmd := m.enterSessionSearch()
	m = updated.(Model)
	if m.transcriptIndex == nil || m.sessionCacheLoadInFlight {
		return m, cmd
	}
	searchCmd := m.dispatchSessionSearch()
	if cmd == nil {
		return m, searchCmd
	}
	refreshCmd := m.runSessionsRefreshCmd(m.transcriptIndex, m.cfg, m.transcriptReaders, m.sessionSearchSeq)
	return m, tea.Batch(searchCmd, refreshCmd)
}

// beginSessionQueryEdit opens the Past Sessions query prompt and remembers the
// query it opened on, which is what esc in the prompt restores.
func (m *Model) beginSessionQueryEdit() {
	m.sessionSearchMode = true
	m.sessionQuerySaved = m.sessionQuery
}

// exitSessionSearch returns to ModeList and clears transient input state
// (the query and chip filters are preserved so re-entry feels stateful).
func (m *Model) exitSessionSearch() {
	m.mode = ModeList
	m.sessionSearchMode = false
}

// handleSessionSearchKey dispatches by sub-mode: free-text input (after
// `/`) versus navigation. Mirrors handlePickerKey's split.
func (m Model) handleSessionSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The operator has taken the list over; results arriving later must not
	// move the cursor back to the conversation the view was opened on.
	m.sessionWantedKey = ""
	if m.sessionSearchMode {
		return m.handleSessionSearchTextKey(msg)
	}
	return m.handleSessionSearchNavKey(msg)
}

func (m Model) handleSessionSearchTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEnter:
		m.sessionSearchMode = false
		return m, nil
	case keyEsc:
		// Cancel restores the query `/` opened on, as the dashboard's filter
		// prompt does; enter is what keeps the edit.
		m.sessionSearchMode = false
		if m.sessionQuery != m.sessionQuerySaved {
			m.sessionQuery = m.sessionQuerySaved
			return m, m.dispatchSessionSearch()
		}
		return m, nil
	case keyBackspace:
		if r := []rune(m.sessionQuery); len(r) > 0 {
			m.sessionQuery = string(r[:len(r)-1])
			return m, m.dispatchSessionSearch()
		}
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.sessionQuery += t
			return m, m.dispatchSessionSearch()
		}
		return m, nil
	}
}

func (m Model) handleSessionSearchNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyQ, keyEsc:
		// Two-stage esc: first clears the text query if one is set, then
		// exits the mode, so a stray esc never surprises the user with an
		// unintended mode switch. q leaves in one step, as it does in the
		// launcher.
		if msg.String() == keyEsc && m.sessionQuery != "" {
			m.sessionQuery = ""
			return m, m.dispatchSessionSearch()
		}
		if m.historyOnly {
			return m, tea.Quit
		}
		m.exitSessionSearch()
		return m, nil
	case keySlash:
		m.beginSessionQueryEdit()
		return m, nil
	case keyQuestion:
		m.openHelp(ModeSessionSearch)
		return m, nil
	case keyJ, keyDown:
		m.sessionCursor = clamp(m.sessionCursor+1, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyK, keyUp:
		m.sessionCursor = clamp(m.sessionCursor-1, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyG, keyHome:
		m.sessionCursor = 0
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyGG, keyEnd:
		m.sessionCursor = len(m.sessionResults) - 1
		if m.sessionCursor < 0 {
			m.sessionCursor = 0
		}
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyCopyPath:
		path := selectedSessionSearchPath(m)
		if path == "" {
			return m, nil
		}
		return m, commands.CopyText(path)
	case keyCopySessionID:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		id := m.sessionResults[m.sessionCursor].Meta.ID
		if id == "" {
			return m, nil
		}
		return m, commands.CopyText(id)
	case keyCtrlD:
		step := halfPageStep(sessionSearchVisibleHeight(m))
		m.sessionCursor = clamp(m.sessionCursor+step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyCtrlU:
		step := halfPageStep(sessionSearchVisibleHeight(m))
		m.sessionCursor = clamp(m.sessionCursor-step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyPgDown, keyCtrlF:
		step := fullPageStep(sessionSearchVisibleHeight(m))
		m.sessionCursor = clamp(m.sessionCursor+step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyPgUp, keyCtrlB:
		step := fullPageStep(sessionSearchVisibleHeight(m))
		m.sessionCursor = clamp(m.sessionCursor-step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case "h":
		return m.enterChipPicker(chipPickerHarness)
	case "p":
		return m.enterChipPicker(chipPickerProfile)
	case "r":
		return m.enterChipPicker(chipPickerProject)
	case "m":
		return m.enterChipPicker(chipPickerMessageSource)
	case keyEnter:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		return m.enterSessionView(m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta))
	case keyC:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		meta := m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta)
		return m, m.conversationActionCmd(m.transcriptReaders, meta, actions.Continue)
	case keyF:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		meta := m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta)
		return m, m.conversationActionCmd(m.transcriptReaders, meta, actions.Fork)
	case keyWorktreeFork:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		m.startWorktreeForkConfirm(m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta), ModeSessionSearch)
		return m, nil
	case "H", "E":
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		m.startHandoffPicker(m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta), ModeSessionSearch, msg.String() == "E")
		return m, nil
	case keyD:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		return m.enterDeleteChoice(m.contextualSessionMeta(m.sessionResults[m.sessionCursor].Meta))
	}
	return m, nil
}

// startWorktreeForkConfirm stages an isolated Git worktree fork of meta and
// raises the confirmation prompt. returnMode is where both accept and cancel
// land, so the search list and the transcript viewer share one implementation.
func (m *Model) startWorktreeForkConfirm(meta transcript.SessionMeta, returnMode Mode) {
	m.isolatedForkConversation = &meta
	m.targetSession = session.RunningSession{Base: meta.Project, Project: meta.Project, Directory: meta.CWD, Profile: meta.DisplayProfile(), Account: meta.DisplayProfile(), Harness: meta.Harness}
	m.confirmAction = "fork worktree"
	m.confirmConsequence = worktreeForkConsequence
	m.confirmReturnMode = returnMode
	m.mode = ModeConfirm
}

// startHandoffPicker stages a cross-harness handoff of meta and opens the
// profile picker. edit carries the `E` variant's request to review the pack
// before launching; returnMode is where accept and cancel both land.
func (m *Model) startHandoffPicker(meta transcript.SessionMeta, returnMode Mode, edit bool) {
	m.handoffConversation = &meta
	m.targetSession = session.RunningSession{
		Base: meta.Project, Project: meta.Project, Directory: meta.CWD,
		Profile: meta.DisplayProfile(), Account: meta.DisplayProfile(), Harness: meta.Harness,
	}
	m.configPickerAccounts = m.cfg.Accounts
	if len(m.configPickerAccounts) == 0 {
		m.configPickerAccounts = m.cfg.Profiles
	}
	m.configPickerCurrent = meta.DisplayProfile()
	m.configPickerCursor = launch.AccountIndex(m.configPickerAccounts, m.configPickerCurrent)
	m.configPickerPurpose = launch.ConfigPickerHandoff
	m.configPickerReturnMode = returnMode
	m.configPickerProjectIdx = -1
	m.handoffEdit = edit
	m.mode = ModeConfigPicker
}

// contextualSessionMeta returns a copy whose primary project reflects the
// active single project chip when that alias belongs to the transcript. The
// cached search result remains the stable primary metadata; only rendering
// and actions launched from the filtered view receive the contextual title.
func (m Model) contextualSessionMeta(meta transcript.SessionMeta) transcript.SessionMeta {
	if len(m.filterProject) != 1 || m.filterProject[0] == "" || !meta.MatchesAnyProject(m.filterProject) {
		return meta
	}
	meta.Project = m.filterProject[0]
	return meta
}

// dispatchSessionSearch returns a tea.Cmd that fires after the debounce
// window expires. The actual search only runs if the user hasn't issued
// another keystroke or chip change in the meantime (seq check in the
// debounce handler).
func (m *Model) dispatchSessionSearch() tea.Cmd {
	m.sessionSearchSeq++
	seq := m.sessionSearchSeq
	m.sessionLoading = true
	m.sessionSearchErr = nil
	return tea.Tick(sessionSearchDebounce, func(time.Time) tea.Msg {
		return sessionSearchDebounceMsg{seq: seq}
	})
}

func startupSessionWarmupCmd(seq int) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg { return sessionCacheLoadStartedMsg{} },
		sessionCacheLoadCmd(seq),
	)
}

func sessionCacheLoadCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		idx, readers, loadErr := loadTranscriptCache()
		if idx == nil {
			return sessionCacheLoadDoneMsg{seq: seq, loadErr: loadErr}
		}
		results, searchErr := transcript.Search(idx, readers, transcript.Query{MainOnly: true})
		return sessionCacheLoadDoneMsg{
			seq:       seq,
			idx:       idx,
			readers:   readers,
			results:   results,
			loadErr:   loadErr,
			searchErr: searchErr,
		}
	}
}

func loadTranscriptCache() (*transcript.Index, map[string]transcript.Reader, error) {
	idx, err := transcript.LoadIndex()
	if idx == nil {
		return nil, nil, err
	}
	readers := buildTranscriptReaders()
	migrateErr := idx.MigrateSessionClasses(readers)
	return idx, readers, errors.Join(err, migrateErr)
}

func sessionQueryIsEmpty(q transcript.Query) bool {
	return q.Conversation == "" && q.Text == "" && len(q.Harness) == 0 && len(q.Profile) == 0 && len(q.Project) == 0 && (q.MessageSource == "" || q.MessageSource == "any")
}

func buildTranscriptReaders() map[string]transcript.Reader {
	return map[string]transcript.Reader{
		harness.Claude:  transcript.NewClaudeReader(),
		harness.Copilot: transcript.NewCopilotReader(),
		harness.Codex:   transcript.NewCodexReader(),
	}
}

// runSessionSearchCmd kicks off a transcript.Search dispatch without
// debouncing. Used both by the post-refresh handler and by the debounce
// trigger once the wait elapses.
func runSessionSearchCmd(seq int, idx *transcript.Index, readers map[string]transcript.Reader, q transcript.Query) tea.Cmd {
	return func() tea.Msg {
		results, err := transcript.Search(idx, readers, q)
		return sessionSearchDoneMsg{seq: seq, results: results, err: err}
	}
}

func (m Model) runSessionSearchCmd(seq int, idx *transcript.Index, readers map[string]transcript.Reader, q transcript.Query) tea.Cmd {
	if m.sessionSearchCommand != nil {
		return m.sessionSearchCommand(seq, q)
	}
	return runSessionSearchCmd(seq, idx, readers, q)
}

// sessionsRefreshCmd runs Index.Refresh and retention pruning off-thread, then
// emits a sessionsRefreshDoneMsg on completion. The seq is forwarded to the
// follow-up search dispatch so the result chain stays cancellable.
func sessionsRefreshCmd(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader, seq int) tea.Cmd {
	return func() tea.Msg {
		return sessionsRefreshDoneMsg{err: refreshAndPruneSessions(idx, cfg, readers), seq: seq}
	}
}

func sessionWarmRefreshCmd(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader, seq int, q transcript.Query) tea.Cmd {
	return func() tea.Msg {
		refreshErr := refreshAndPruneSessions(idx, cfg, readers)
		results, searchErr := transcript.Search(idx, readers, q)
		return sessionWarmRefreshDoneMsg{
			seq:        seq,
			results:    results,
			refreshErr: refreshErr,
			searchErr:  searchErr,
		}
	}
}

func (m Model) runSessionWarmRefreshCmd(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader, seq int, q transcript.Query) tea.Cmd {
	if m.sessionWarmRefreshCommand != nil {
		return m.sessionWarmRefreshCommand(seq, q)
	}
	return sessionWarmRefreshCmd(idx, cfg, readers, seq, q)
}

func (m Model) runSessionsRefreshCmd(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader, seq int) tea.Cmd {
	if m.sessionRefreshCommand != nil {
		return m.sessionRefreshCommand(seq)
	}
	return sessionsRefreshCmd(idx, cfg, readers, seq)
}

func refreshAndPruneSessions(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader) error {
	refreshErr := idx.Refresh(cfg, readers)
	hidden, pruneErr := transcript.Prune(idx, cfg, time.Now())
	if pruneErr != nil {
		slog.Warn("transcript.prune failed", "err", pruneErr)
	} else if hidden > 0 {
		slog.Debug("transcript.prune", "hidden", hidden)
	}
	if refreshErr != nil && pruneErr != nil {
		return fmt.Errorf("refresh failed: %w; prune failed: %w", refreshErr, pruneErr)
	}
	if refreshErr != nil {
		return refreshErr
	}
	if pruneErr != nil {
		return pruneErr
	}
	return nil
}

// currentSessionQuery snapshots the chip filters + text query into a
// transcript.Query value the search engine consumes.
func (m Model) currentSessionQuery() transcript.Query {
	return transcript.Query{
		Text:          m.sessionQuery,
		MessageSource: m.filterMessageSource,
		Harness:       m.filterHarness,
		Profile:       m.filterProfile,
		Project:       m.filterProject,
		MainOnly:      m.historyConversation == "",
		Conversation:  m.historyConversation,
	}
}

// scrollSessionSearchToCursor keeps the cursor inside the visible window
// after navigation. Mirrors scrollListToCursor: renderSessionSearchList
// reserves one row for ▲ when offset > 0 and one row for ▼ when items
// remain past the window, so the scroll budget must subtract them too —
// otherwise the cursor falls off the bottom of the rendered list.
func scrollSessionSearchToCursor(m *Model) {
	total := len(m.sessionResults)
	height := sessionSearchVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.sessionOffset = 0
		return
	}
	if m.sessionCursor < 0 {
		m.sessionCursor = 0
	}
	if m.sessionCursor >= total {
		m.sessionCursor = total - 1
	}
	if m.sessionCursor < m.sessionOffset {
		m.sessionOffset = m.sessionCursor
	}
	for m.sessionOffset < m.sessionCursor {
		budget := height
		if m.sessionOffset > 0 {
			budget--
		}
		if m.sessionCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.sessionCursor-m.sessionOffset+1 <= budget {
			break
		}
		m.sessionOffset++
	}
	if m.sessionOffset < 0 {
		m.sessionOffset = 0
	}
}

func profileNames(m Model) []string {
	names := make([]string, 0, len(m.cfg.Profiles))
	for _, p := range m.cfg.Profiles {
		names = append(names, p.Name)
	}
	return names
}

func projectTitles(m Model) []string {
	titles := make([]string, 0, len(m.cfg.Projects))
	for _, p := range m.cfg.Projects {
		titles = append(titles, p.Title)
	}
	return titles
}
