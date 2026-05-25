package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
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

// enterSessionSearch swaps to ModeSessionSearch, lazily builds the index +
// readers, and dispatches an async refresh. Subsequent entries reuse the
// cached index (no re-refresh per keypress — the dev machine takes ~10s to
// rescan ~1700 sessions, per S5 notes).
func (m Model) enterSessionSearch() (tea.Model, tea.Cmd) {
	m.mode = ModeSessionSearch
	m.sessionCursor = 0
	m.sessionOffset = 0
	m.sessionSearchMode = false
	m.sessionRefreshErr = nil
	m.sessionSearchErr = nil

	if m.transcriptIndex == nil {
		idx, err := transcript.LoadIndex()
		if err != nil {
			m.sessionRefreshErr = err
			m.sessionLoading = false
			return m, nil
		}
		m.transcriptIndex = idx
	}
	if m.transcriptReaders == nil {
		m.transcriptReaders = map[string]transcript.Reader{
			harness.Claude:  transcript.NewClaudeReader(),
			harness.Copilot: transcript.NewCopilotReader(),
			harness.Codex:   transcript.NewCodexReader(),
		}
	}

	m.sessionLoading = true
	m.sessionSearchSeq++
	seq := m.sessionSearchSeq
	var cmds []tea.Cmd
	cmds = append(cmds, sessionsRefreshCmd(m.transcriptIndex, m.cfg, m.transcriptReaders, seq))
	if c := m.pushToast(Toast{
		Severity: toastInfo,
		Text:     "Scanning transcripts…",
		Expires:  time.Now().Add(progressToastTTL),
	}); c != nil {
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
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
	case keyPgDown:
		step := sessionSearchVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.sessionCursor = clamp(m.sessionCursor+step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case keyPgUp:
		step := sessionSearchVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.sessionCursor = clamp(m.sessionCursor-step, 0, len(m.sessionResults)-1)
		scrollSessionSearchToCursor(&m)
		return m, nil
	case "h":
		m.filterHarness = cycleChip(m.filterHarness, harness.Names())
		return m, m.dispatchSessionSearch()
	case "p":
		m.filterProfile = cycleChip(m.filterProfile, profileNames(m))
		return m, m.dispatchSessionSearch()
	case "r":
		m.filterProject = cycleChip(m.filterProject, projectTitles(m))
		return m, m.dispatchSessionSearch()
	case keyEnter:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		return m.enterSessionView(m.sessionResults[m.sessionCursor].Meta)
	case keyD:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
			return m, nil
		}
		return m.enterDeleteChoice(m.sessionResults[m.sessionCursor].Meta)
	}
	return m, nil
}

// dispatchSessionSearch returns a tea.Cmd that fires after the debounce
// window expires. The actual search only runs if the user hasn't issued
// another keystroke or chip change in the meantime (seq check in the
// debounce handler).
func (m *Model) dispatchSessionSearch() tea.Cmd {
	m.sessionSearchSeq++
	seq := m.sessionSearchSeq
	m.sessionLoading = true
	return tea.Tick(sessionSearchDebounce, func(time.Time) tea.Msg {
		return sessionSearchDebounceMsg{seq: seq}
	})
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

// sessionsRefreshCmd runs Index.Refresh off-thread and emits a
// sessionsRefreshDoneMsg on completion. The seq is forwarded to the
// follow-up search dispatch so the result chain stays cancellable.
func sessionsRefreshCmd(idx *transcript.Index, cfg *config.Config, readers map[string]transcript.Reader, seq int) tea.Cmd {
	return func() tea.Msg {
		err := idx.Refresh(cfg, readers)
		return sessionsRefreshDoneMsg{err: err, seq: seq}
	}
}

// currentSessionQuery snapshots the chip filters + text query into a
// transcript.Query value the search engine consumes.
func (m Model) currentSessionQuery() transcript.Query {
	return transcript.Query{
		Text:    m.sessionQuery,
		Harness: m.filterHarness,
		Profile: m.filterProfile,
		Project: m.filterProject,
	}
}

// scrollSessionSearchToCursor keeps the cursor inside the visible window
// after navigation. Mirrors scrollListToCursor.
func scrollSessionSearchToCursor(m *Model) {
	height := sessionSearchVisibleHeight(*m)
	if height <= 0 || len(m.sessionResults) == 0 {
		m.sessionOffset = 0
		return
	}
	if m.sessionCursor < m.sessionOffset {
		m.sessionOffset = m.sessionCursor
	}
	if m.sessionCursor >= m.sessionOffset+height {
		m.sessionOffset = m.sessionCursor - height + 1
	}
	if m.sessionOffset < 0 {
		m.sessionOffset = 0
	}
}

// cycleChip cycles current through (none) → opts[0] → opts[1] → … → (none).
// current and opts are both []string but for v1 chips hold at most one value
// (single-select), so cycling a longer slice is not supported.
func cycleChip(current, opts []string) []string {
	if len(opts) == 0 {
		return nil
	}
	cur := ""
	if len(current) > 0 {
		cur = current[0]
	}
	if cur == "" {
		return []string{opts[0]}
	}
	for i, o := range opts {
		if o == cur {
			if i+1 >= len(opts) {
				return nil
			}
			return []string{opts[i+1]}
		}
	}
	// Current value not in opts (e.g., profiles changed at runtime) — reset.
	return []string{opts[0]}
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
