// This file is what the root model does about plans: running the Plans browser
// — query, sort, project filter, cursor and scroll — and the plan viewer it
// opens, including loading a plan's text and stepping through its matches.

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
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

// planCacheLoadDoneMsg carries the lazy recovering filesystem-cache snapshot
// and a search of its warm rows. loadErr may be a non-fatal quarantine or
// read warning when idx is non-nil.
type planCacheLoadDoneMsg struct {
	seq       int
	idx       *plans.Index
	results   []plans.Result
	loadErr   error
	searchErr error
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
	m.mode = ModePlanSearch
	m.planCursor = 0
	m.planOffset = 0
	m.planSearchMode = false
	m.planRefreshErr = nil
	m.planSearchErr = nil

	if m.plansIndex == nil {
		m.planLoading = true
		if m.planCacheLoadInFlight {
			return m, nil
		}
		m.planCacheLoadInFlight = true
		m.planSearchSeq++
		return m, planCacheLoadCmd(m.planSearchSeq, m.currentPlanQuery())
	}
	return m.startPlanSearchRefresh()
}

// planCacheLoadCmd lazily loads the default recovering filesystem cache and
// searches its warm snapshot before the slower source refresh begins.
func planCacheLoadCmd(seq int, query plans.Query) tea.Cmd {
	return func() tea.Msg {
		idx, loadErr := plans.LoadIndex()
		var results []plans.Result
		var searchErr error
		if idx != nil {
			results, searchErr = plans.Search(idx, query)
		}
		return planCacheLoadDoneMsg{
			seq: seq, idx: idx, results: results,
			loadErr: loadErr, searchErr: searchErr,
		}
	}
}

func (m Model) startPlanSearchRefresh() (tea.Model, tea.Cmd) {
	m.planLoading = true
	m.planSearchSeq++
	seq := m.planSearchSeq
	var cmds []tea.Cmd
	cmds = append(cmds, m.runPlansRefreshCmd(m.plansIndex, m.cfg, seq))
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
	case keyQuestion:
		m.openHelp(ModePlanSearch)
		return m, nil
	case keyR:
		return m.enterChipPicker(chipPickerProject)
	case keyJ, keyDown:
		m.planCursor = clamp(m.planCursor+1, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyK, keyUp:
		m.planCursor = clamp(m.planCursor-1, 0, len(m.planResults)-1)
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyG, keyHome:
		m.planCursor = 0
		scrollPlanSearchToCursor(&m)
		return m, nil
	case keyGG, keyEnd:
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
		return m, commands.CopyText(path)
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
	case keyPlanOpenEditor, keyOpenEditor:
		if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
			return m, nil
		}
		return m, openPlanInEditorCmd(m.client, m.planResults[m.planCursor].Meta, m.cfg)
	case keyEnter:
		if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
			return m, nil
		}
		return m.enterPlanView(m.planResults[m.planCursor].Meta)
	case keySortPlan:
		m.planSortMode = tuiplans.NextSortMode(m.planSortMode)
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

func (m Model) runPlanSearchCmd(seq int, idx *plans.Index, q plans.Query) tea.Cmd {
	if m.planSearchCommand != nil {
		return m.planSearchCommand(seq, idx, q)
	}
	return runPlanSearchCmd(seq, idx, q)
}

// plansRefreshCmd runs Index.Refresh off-thread, then emits a
// plansRefreshDoneMsg. The seq is forwarded to the follow-up search dispatch
// so the result chain stays cancellable.
func plansRefreshCmd(idx *plans.Index, cfg *config.Config, seq int) tea.Cmd {
	return func() tea.Msg {
		err := idx.Refresh(config.ExpandTilde(cfg.Defaults.PlansDirectory), cfg.Projects...)
		return plansRefreshDoneMsg{err: err, seq: seq}
	}
}

func (m Model) runPlansRefreshCmd(idx *plans.Index, cfg *config.Config, seq int) tea.Cmd {
	if m.planRefreshCommand != nil {
		return m.planRefreshCommand(idx, cfg, seq)
	}
	return plansRefreshCmd(idx, cfg, seq)
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
	return plans.Query{Text: m.planQuery, Sort: m.planSortMode, Project: m.planFilterProject}
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

// planFilesLoadedMsg carries the result of an async file-read pass. seq
// matches the m.planViewerLoadSeq snapshot at dispatch time so a slow
// load arriving after the user pressed esc (or opened a different plan)
// is dropped silently in the Update handler.
type planFilesLoadedMsg struct {
	seq   int
	lines []string
	err   error
}

// enterPlanView switches to ModePlanView for the given plan and dispatches
// the async file-read. A value copy of the meta is held so the viewer is
// not invalidated if the underlying index entry is mutated mid-read.
func (m Model) enterPlanView(meta plans.PlanMeta) (tea.Model, tea.Cmd) {
	m.mode = ModePlanView
	metaCopy := meta
	m.viewerPlan = &metaCopy
	m.planViewerLines = nil
	m.planViewerCursor = 0
	m.planViewerScroll = 0
	m.planViewerMatchIdx = 0
	m.planViewerMatchLines = nil
	m.planViewerLoading = true
	m.planViewerLoadErr = nil
	m.planViewerLoadSeq++
	seq := m.planViewerLoadSeq
	return m, readPlanFilesCmd(seq, metaCopy)
}

// exitPlanView returns to the plan picker mode that opened it and zeros every
// viewer-local field. The picker state (query, results, cursor) is preserved
// so the user lands back on the same row they entered from.
func (m *Model) exitPlanView() {
	m.mode = ModePlanSearch
	m.viewerPlan = nil
	m.planViewerLines = nil
	m.planViewerCursor = 0
	m.planViewerScroll = 0
	m.planViewerMatchIdx = 0
	m.planViewerMatchLines = nil
	m.planViewerLoading = false
	m.planViewerLoadErr = nil
}

// handlePlanViewKey processes keys while ModePlanView is active. j/k/g/G
// and n/N move between logical source lines, pgup/pgdn page through visual
// wrapped rows, and `y` copies the selected raw source line. Also supports
// `e` (open the plan dir in $EDITOR) and `/` (re-enter the originating plan
// picker with the current query).
func (m Model) handlePlanViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := len(m.planViewerLines)
	visible := planViewerVisibleHeight(m)
	lastIdx := total - 1
	if lastIdx < 0 {
		lastIdx = 0
	}

	switch msg.String() {
	case keyEsc, keyQ:
		// q backs out one level, as it does in the Past Sessions viewer and
		// in every list; a second q (or esc) leaves Plans from the list.
		m.exitPlanView()
		return m, nil
	case keyQuestion:
		m.openHelp(ModePlanView)
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.planViewerCursor < lastIdx {
			m.planViewerCursor++
		}
		scrollPlanViewerToCursor(&m)
	case keyK, keyUp:
		if m.planViewerCursor > 0 {
			m.planViewerCursor--
		}
		scrollPlanViewerToCursor(&m)
	case keyCtrlD:
		halfPagePlanViewerRows(&m, visible, 1)
	case keyCtrlU:
		halfPagePlanViewerRows(&m, visible, -1)
	case keyPgDown, keyCtrlF:
		pagePlanViewerRows(&m, visible, 1)
	case keyPgUp, keyCtrlB:
		pagePlanViewerRows(&m, visible, -1)
	case keyG, keyHome:
		m.planViewerCursor = 0
		scrollPlanViewerToCursor(&m)
	case keyGG, keyEnd:
		m.planViewerCursor = lastIdx
		scrollPlanViewerToCursor(&m)
	case keyN:
		m.jumpToNextPlanMatch(1)
	case "N":
		m.jumpToNextPlanMatch(-1)
	case keyCopyPath:
		path := currentPlanViewPath(m)
		if path == "" {
			return m, nil
		}
		return m, commands.CopyText(path)
	case keySlash:
		// Return to the picker with the editable text query active so the
		// user can refine the query without re-traversing the navigation
		// stack.
		m.mode = ModePlanSearch
		m.planSearchMode = true
		return m, nil
	case keyY:
		if len(m.planViewerLines) == 0 ||
			m.planViewerCursor < 0 ||
			m.planViewerCursor >= len(m.planViewerLines) {
			return m, nil
		}
		return m, commands.CopyText(m.planViewerLines[m.planViewerCursor])
	case keyPlanOpenEditor, keyOpenEditor:
		if m.viewerPlan == nil {
			return m, nil
		}
		return m, openPlanInEditorCmd(m.client, *m.viewerPlan, m.cfg)
	}
	return m, nil
}

// jumpToNextPlanMatch advances planViewerMatchIdx by step (+1 for `n`,
// -1 for `N`) with wrap-around, then moves planViewerCursor to the
// resulting logical source line. Mirrors jumpToNextMatch.
func (m *Model) jumpToNextPlanMatch(step int) {
	n := len(m.planViewerMatchLines)
	if n == 0 {
		return
	}
	m.planViewerMatchIdx = (m.planViewerMatchIdx + step + n) % n
	m.planViewerCursor = m.planViewerMatchLines[m.planViewerMatchIdx]
	scrollPlanViewerToCursor(m)
}

// pagePlanViewerRows shifts the window by visual wrapped rows, then syncs the
// logical cursor to the source line at the new top row.
func pagePlanViewerRows(m *Model, visible, direction int) {
	movePlanViewerRows(m, visible, direction, false)
}

func halfPagePlanViewerRows(m *Model, visible, direction int) {
	movePlanViewerRows(m, visible, direction, true)
}

func movePlanViewerRows(m *Model, visible, direction int, half bool) {
	rows := planViewerRows(*m)
	total := len(rows)
	if visible <= 0 || total == 0 {
		m.planViewerScroll = 0
		m.planViewerCursor = 0
		return
	}
	clampPlanViewerScroll(m)
	step := tuiplans.PageStep(total, m.planViewerScroll, visible)
	if half {
		step = halfPageStep(step)
	}
	m.planViewerScroll += direction * step
	clampPlanViewerScroll(m)
	if m.planViewerScroll < 0 || m.planViewerScroll >= total {
		return
	}
	sourceLine := rows[m.planViewerScroll].SourceLine
	if sourceLine >= 0 && sourceLine < len(m.planViewerLines) {
		m.planViewerCursor = sourceLine
	}
}

// readPlanFilesCmd reads every file in meta.Files (relative paths under
// meta.DirPath), splits each into lines, and prefixes each chunk with a
// "--- <rel> ---" separator so the viewer can render file boundaries
// without losing track of source provenance. Any single read error
// short-circuits the whole load with err set on the message.
func readPlanFilesCmd(seq int, meta plans.PlanMeta) tea.Cmd {
	return func() tea.Msg {
		var lines []string
		for _, rel := range meta.Files {
			full := filepath.Join(meta.DirPath, rel)
			data, err := os.ReadFile(full)
			if err != nil {
				return planFilesLoadedMsg{seq: seq, err: err}
			}
			lines = append(lines, "--- "+rel+" ---")
			body := strings.TrimRight(string(data), "\n")
			if body == "" {
				continue
			}
			lines = append(lines, strings.Split(body, "\n")...)
		}
		return planFilesLoadedMsg{seq: seq, lines: lines}
	}
}

// planViewerScrollHeight sizes the plan viewer's scroll window under the plan
// viewer's own footer. Mirrors viewerScrollHeight: offsets are recomputed for
// loads that land behind an overlay and for resizes arriving from another mode.
func planViewerScrollHeight(m Model) int {
	m.mode = ModePlanView
	return planViewerVisibleHeight(m)
}

// scrollPlanViewerToCursor advances m.planViewerScroll, a visual wrapped-row
// offset, the minimum amount needed to keep m.planViewerCursor, a logical
// source-line index, visible inside the windowed body. Mirrors
// scrollViewerToCursor: the body reserves one row for ▲ when scroll > 0 and
// one row for ▼ when more rows remain past the window, so the budget must
// subtract them too.
func scrollPlanViewerToCursor(m *Model) {
	totalLines := len(m.planViewerLines)
	rows := planViewerRows(*m)
	totalRows := len(rows)
	height := planViewerScrollHeight(*m)
	if height <= 0 || totalLines == 0 || totalRows == 0 {
		m.planViewerScroll = 0
		m.planViewerCursor = 0
		return
	}
	if m.planViewerCursor < 0 {
		m.planViewerCursor = 0
	}
	if m.planViewerCursor >= totalLines {
		m.planViewerCursor = totalLines - 1
	}
	cursorRow := tuiplans.FirstRowForLine(rows, m.planViewerCursor)
	if cursorRow < 0 {
		cursorRow = 0
	}
	if cursorRow < m.planViewerScroll {
		m.planViewerScroll = cursorRow
	}
	for m.planViewerScroll < cursorRow {
		budget := height
		if m.planViewerScroll > 0 {
			budget--
		}
		if cursorRow < totalRows-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if cursorRow-m.planViewerScroll+1 <= budget {
			break
		}
		m.planViewerScroll++
	}
	clampPlanViewerScroll(m)
}

// clampPlanViewerScroll pins m.planViewerScroll to a valid wrapped-row
// offset at the model's current dimensions. Protects against a stale offset
// surviving a resize that changes the wrapped row count.
func clampPlanViewerScroll(m *Model) {
	total := len(planViewerRows(*m))
	visible := planViewerScrollHeight(*m)
	maxOffset := tuiplans.MaxScrollOffset(total, visible)
	if m.planViewerScroll > maxOffset {
		m.planViewerScroll = maxOffset
	}
	if m.planViewerScroll < 0 {
		m.planViewerScroll = 0
	}
}
