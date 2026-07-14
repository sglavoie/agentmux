package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/plans"
)

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
// `o` (open the plan dir in $EDITOR) and `/` (re-enter the originating plan
// picker with the current query).
func (m Model) handlePlanViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := len(m.planViewerLines)
	visible := planViewerVisibleHeight(m)
	lastIdx := total - 1
	if lastIdx < 0 {
		lastIdx = 0
	}

	switch msg.String() {
	case keyEsc:
		m.exitPlanView()
		return m, nil
	case keyQ:
		m.exitPlanView()
		m.exitPlanSearch()
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
	case keyG:
		m.planViewerCursor = 0
		scrollPlanViewerToCursor(&m)
	case keyGG:
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
		return m, copyTextCmd(path)
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
		return m, copyTextCmd(m.planViewerLines[m.planViewerCursor])
	case keyO:
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
	step := planViewerPageStep(total, m.planViewerScroll, visible)
	if half {
		step = halfPageStep(step)
	}
	m.planViewerScroll += direction * step
	clampPlanViewerScroll(m)
	if m.planViewerScroll < 0 || m.planViewerScroll >= total {
		return
	}
	sourceLine := rows[m.planViewerScroll].sourceLine
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

// computePlanViewerMatches scans lines for a case-insensitive substring
// of query and returns the indices of matching lines. Returns nil when
// query is empty so n/N have no effect.
func computePlanViewerMatches(lines []string, query string) []int {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	needle := strings.ToLower(q)
	var matches []int
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			matches = append(matches, i)
		}
	}
	return matches
}

// planViewerVisibleHeight returns the number of body rows that fit in the
// root body area. The viewer's chrome matches the session viewer: header (1)
// + blank (1), plus the plan path when available and any inline read-error row.
func planViewerVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	avail := rootBodyHeight(m) - 2 - pathRowLines(currentPlanViewPath(m)) - planViewerErrorLines(m)
	if avail < 1 {
		return 1
	}
	return avail
}

func planViewerErrorLines(m Model) int {
	if m.planViewerLoadErr != nil {
		return 1
	}
	return 0
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
	height := planViewerVisibleHeight(*m)
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
	cursorRow := planViewerFirstRowForLine(rows, m.planViewerCursor)
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
	visible := planViewerVisibleHeight(*m)
	maxOffset := planViewerMaxScrollOffset(total, visible)
	if m.planViewerScroll > maxOffset {
		m.planViewerScroll = maxOffset
	}
	if m.planViewerScroll < 0 {
		m.planViewerScroll = 0
	}
}
