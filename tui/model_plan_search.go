package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
)

// planEditorErrMsg surfaces an OpenPathInEditor failure back to the model so
// it can be rendered as a toast on the next Update tick.
type planEditorErrMsg struct{ err error }

// plansRefreshDoneMsg carries the result of a plans.Index.Refresh dispatch.
// Always followed by an immediate search dispatch that populates planResults
// from the freshly refreshed index.
type plansRefreshDoneMsg struct {
	err error
	seq int // planSearchSeq snapshot at dispatch time
}

// planSearchDebounceMsg fires after sessionSearchDebounce. The handler
// dispatches the actual search iff this message's seq still matches the
// current planSearchSeq — otherwise the user kept typing and a fresher
// dispatch is already inbound.
type planSearchDebounceMsg struct{ seq int }

// planSearchDoneMsg carries the result of a plans.Search call. seq matches
// the dispatch's planSearchSeq snapshot so stale results from slow scans
// never overwrite a fresher query's output.
type planSearchDoneMsg struct {
	seq     int
	results []plans.Result
	err     error
}

// enterPlanSearch swaps to ModePlanSearch, lazily builds the index, and
// dispatches an async refresh. Subsequent entries reuse the cached index.
func (m Model) enterPlanSearch() (tea.Model, tea.Cmd) {
	m.clearJumpChord()
	m.mode = ModePlanSearch
	m.planCursor = 0
	m.planOffset = 0
	m.planSearchMode = false
	m.planRefreshErr = nil
	m.planSearchErr = nil

	if m.plansIndex == nil {
		idx, err := plans.LoadIndex()
		if err != nil {
			m.planRefreshErr = err
			m.planLoading = false
			return m, nil
		}
		m.plansIndex = idx
	}

	m.planLoading = true
	m.planSearchSeq++
	seq := m.planSearchSeq
	var cmds []tea.Cmd
	cmds = append(cmds, plansRefreshCmd(m.plansIndex, m.cfg, seq))
	if c := m.pushToast(Toast{
		Severity: toastInfo,
		Text:     "Scanning plans…",
		Expires:  time.Now().Add(progressToastTTL),
	}); c != nil {
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

// exitPlanSearch returns to ModeList and clears transient input state.
func (m *Model) exitPlanSearch() {
	m.mode = ModeList
	m.planSearchMode = false
}

// handlePlanSearchKey dispatches by sub-mode: free-text input after `/`
// versus navigation. Mirrors handleSessionSearchKey's split.
func (m Model) handlePlanSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.planSearchMode {
		return m.handlePlanSearchTextKey(msg)
	}
	return m.handlePlanSearchNavKey(msg)
}

func (m Model) handlePlanSearchTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.planSearchMode = false
		return m, nil
	case keyBackspace:
		if r := []rune(m.planQuery); len(r) > 0 {
			m.planQuery = string(r[:len(r)-1])
			return m, m.dispatchPlanSearch()
		}
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.planQuery += t
			return m, m.dispatchPlanSearch()
		}
		return m, nil
	}
}

func (m Model) handlePlanSearchNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyQ, keyEsc:
		// Two-stage esc: first clears the text query, then exits the mode.
		// Mirrors session-search behaviour so a stray esc never surprises
		// the user with an unintended mode switch.
		if m.planQuery != "" {
			m.planQuery = ""
			return m, m.dispatchPlanSearch()
		}
		m.exitPlanSearch()
		return m, nil
	case keySlash:
		m.planSearchMode = true
		return m, nil
	case keyJ, keyDown:
		m.planCursor = clamp(m.planCursor+1, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyK, keyUp:
		m.planCursor = clamp(m.planCursor-1, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyG:
		m.planCursor = 0
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyGG:
		m.planCursor = len(m.planResults) - 1
		if m.planCursor < 0 {
			m.planCursor = 0
		}
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyCopyPath:
		path := selectedPlanSearchPath(m)
		if path == "" {
			return m, nil
		}
		return m, copyTextCmd(path)
	case keyCtrlD:
		step := halfPageStep(planSearchVisibleHeight(m))
		m.planCursor = clamp(m.planCursor+step, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyCtrlU:
		step := halfPageStep(planSearchVisibleHeight(m))
		m.planCursor = clamp(m.planCursor-step, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyPgDown, keyCtrlF:
		step := fullPageStep(planSearchVisibleHeight(m))
		m.planCursor = clamp(m.planCursor+step, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyPgUp, keyCtrlB:
		step := fullPageStep(planSearchVisibleHeight(m))
		m.planCursor = clamp(m.planCursor-step, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyO:
		if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
			return m, nil
		}
		return m, openPlanInEditorCmd(m.client, m.planResults[m.planCursor].Meta, m.cfg)
	case keyTogglePreview:
		return m, nil
	case keyEnter:
		if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
			return m, nil
		}
		return m.enterPlanView(m.planResults[m.planCursor].Meta)
	case keySortPlan:
		m.planSortMode = nextPlanSortMode(m.planSortMode)
		var anchor string
		if m.planCursor >= 0 && m.planCursor < len(m.planResults) {
			anchor = m.planResults[m.planCursor].Meta.DirPath
		}
		plans.SortResults(m.planResults, m.planSortMode)
		m.planCursor = 0
		if anchor != "" {
			for i := range m.planResults {
				if m.planResults[i].Meta.DirPath == anchor {
					m.planCursor = i
					break
				}
			}
		}
		scrollPlanSearchToCursor(&m)
		return m, nil
	}
	return m, nil
}

// nextPlanSortMode advances mode through the cycle:
// SortByMatches → SortByModTime → SortByMatches.
func nextPlanSortMode(mode plans.SortMode) plans.SortMode {
	switch mode {
	case plans.SortByMatches:
		return plans.SortByModTime
	default:
		return plans.SortByMatches
	}
}

// dispatchPlanSearch returns a tea.Cmd that fires after the debounce
// window expires. The actual search only runs if the user hasn't issued
// another keystroke in the meantime (seq check in the debounce handler).
func (m *Model) dispatchPlanSearch() tea.Cmd {
	m.planSearchSeq++
	seq := m.planSearchSeq
	m.planLoading = true
	return tea.Tick(sessionSearchDebounce, func(time.Time) tea.Msg {
		return planSearchDebounceMsg{seq: seq}
	})
}

// runPlanSearchCmd kicks off a plans.Search dispatch without debouncing.
// Used both by the post-refresh handler and the debounce trigger.
func runPlanSearchCmd(seq int, idx *plans.Index, q plans.Query) tea.Cmd {
	return func() tea.Msg {
		results, err := plans.Search(idx, q)
		return planSearchDoneMsg{seq: seq, results: results, err: err}
	}
}

// plansRefreshCmd runs Index.Refresh off-thread, then emits a
// plansRefreshDoneMsg. The seq is forwarded to the follow-up search dispatch
// so the result chain stays cancellable.
func plansRefreshCmd(idx *plans.Index, cfg *config.Config, seq int) tea.Cmd {
	return func() tea.Msg {
		err := idx.Refresh(config.ExpandTilde(cfg.Defaults.PlansDirectory))
		return plansRefreshDoneMsg{err: err, seq: seq}
	}
}

// openPlanInEditorCmd invokes session.OpenPathInEditor against the plan's
// directory and surfaces an error back to the model on failure.
func openPlanInEditorCmd(client kitty.KittyClient, meta plans.PlanMeta, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		if err := session.OpenPathInEditor(client, meta.DirPath, "plan: "+meta.Name, cfg); err != nil {
			return planEditorErrMsg{err: err}
		}
		return nil
	}
}

// currentPlanQuery snapshots the text query into a plans.Query value.
func (m Model) currentPlanQuery() plans.Query {
	return plans.Query{Text: m.planQuery, Sort: m.planSortMode}
}

// scrollPlanSearchToCursor keeps the cursor inside the visible window after
// navigation. Mirrors scrollSessionSearchToCursor: renderPlanSearchList
// reserves one row for ▲ when offset > 0 and one row for ▼ when items remain
// past the window, so the scroll budget must subtract them too.
func scrollPlanSearchToCursor(m *Model) {
	total := len(m.planResults)
	height := planSearchVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.planOffset = 0
		return
	}
	if m.planCursor < 0 {
		m.planCursor = 0
	}
	if m.planCursor >= total {
		m.planCursor = total - 1
	}
	if m.planCursor < m.planOffset {
		m.planOffset = m.planCursor
	}
	for m.planOffset < m.planCursor {
		budget := height
		if m.planOffset > 0 {
			budget--
		}
		if m.planCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.planCursor-m.planOffset+1 <= budget {
			break
		}
		m.planOffset++
	}
	if m.planOffset < 0 {
		m.planOffset = 0
	}
}
