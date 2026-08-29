package tui

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

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
	return q.Text == "" && len(q.Harness) == 0 && len(q.Profile) == 0 && len(q.Project) == 0
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
		return fmt.Errorf("refresh failed: %v; prune failed: %w", refreshErr, pruneErr)
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
		Text:     m.sessionQuery,
		Harness:  m.filterHarness,
		Profile:  m.filterProfile,
		Project:  m.filterProject,
		MainOnly: true,
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
