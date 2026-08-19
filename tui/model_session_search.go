package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
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
	m.clearJumpChord()
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

// exitSessionSearch returns to ModeList and clears transient input state
// (the query and chip filters are preserved so re-entry feels stateful).
func (m *Model) exitSessionSearch() {
	m.mode = ModeList
	m.sessionSearchMode = false
}

// handleSessionSearchKey dispatches by sub-mode: free-text input (after
// `/`) versus navigation. Mirrors handlePickerKey's split.
func (m Model) handleSessionSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.sessionSearchMode {
		return m.handleSessionSearchTextKey(msg)
	}
	return m.handleSessionSearchNavKey(msg)
}

func (m Model) handleSessionSearchTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.sessionSearchMode = false
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
		// exits the mode. Mirrors picker behaviour so a stray esc never
		// surprises the user with an unintended mode switch.
		if m.sessionQuery != "" {
			m.sessionQuery = ""
			return m, m.dispatchSessionSearch()
		}
		m.exitSessionSearch()
		return m, nil
	case keySlash:
		m.sessionSearchMode = true
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
	case keyG:
		m.sessionCursor = 0
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyGG:
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
		return m, copyTextCmd(path)
	case keyCopySessionID:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		id := m.sessionResults[m.sessionCursor].Meta.ID
		if id == "" {
			return m, nil
		}
		return m, copyTextCmd(id)
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
	m.configPickerCursor = pickerAccountIndex(m.configPickerAccounts, m.configPickerCurrent)
	m.configPickerPurpose = ConfigPickerHandoff
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
