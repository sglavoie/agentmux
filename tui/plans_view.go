package tui

// This file is the seam between the root Bubble Tea model and the tui/plans
// leaf. The leaf owns the plans domain — the Plans browser's header, chip,
// query bar and result rows, and the plan viewer's header and wrapped body —
// and reads its facts from explicit input values. The Model stays here, and
// every adapter below exists to turn "what the model holds" into "what the
// leaf needs", then re-expose the leaf's answer under the name the rest of the
// root package and its tests already use. Deciding what a keystroke does to
// the plan cursor and scroll offset stays in the root.

import tuiplans "github.com/sglavoie/agentmux/tui/plans"

// planSearchInput projects the model onto the Plans browser's input value.
func planSearchInput(m Model) tuiplans.SearchInput {
	return tuiplans.SearchInput{
		Width:         m.width,
		Height:        m.height,
		BodyHeight:    rootBodyHeight(m),
		PathRow:       renderPathRow("plan path", selectedPlanSearchPath(m), m.width),
		Query:         m.planQuery,
		SearchMode:    m.planSearchMode,
		Loading:       m.planLoading,
		Scanning:      planScanning(m),
		Results:       m.planResults,
		Cursor:        m.planCursor,
		Offset:        m.planOffset,
		SortMode:      m.planSortMode,
		FilterProject: m.planFilterProject,
		RefreshErr:    m.planRefreshErr,
		SearchErr:     m.planSearchErr,
	}
}

// planScanning reports whether the plan files themselves are being read: the
// warm cache load or the source refresh, rather than a query alone.
func planScanning(m Model) bool {
	return m.planLoading && (m.planCacheLoadInFlight || m.planScanSeq != 0)
}

// planViewerInput projects the model onto the plan viewer's input value.
func planViewerInput(m Model) tuiplans.ViewerInput {
	return tuiplans.ViewerInput{
		Width:      m.width,
		Height:     m.height,
		BodyHeight: rootBodyHeight(m),
		PathRow:    renderPathRow("plan path", currentPlanViewPath(m), m.width),
		Plan:       m.viewerPlan,
		Lines:      m.planViewerLines,
		Loading:    m.planViewerLoading,
		LoadErr:    m.planViewerLoadErr,
		MatchLines: m.planViewerMatchLines,
		MatchIdx:   m.planViewerMatchIdx,
		Cursor:     m.planViewerCursor,
		Scroll:     m.planViewerScroll,
	}
}

// planSearchVisibleHeight returns the number of result rows that fit in the
// root body area.
func planSearchVisibleHeight(m Model) int {
	return planSearchInput(m).VisibleHeight()
}

// renderPlanSearch renders the plan browser: header, project chip, query bar,
// and results list.
func renderPlanSearch(m Model) string {
	return planSearchInput(m).Render()
}

// renderPlanSearchList renders the scrollable results list at the given
// per-call row budget.
func renderPlanSearchList(m Model, height int) string {
	return planSearchInput(m).RenderList(height)
}

// planViewerVisibleHeight returns the number of body rows that fit in the root
// body area.
func planViewerVisibleHeight(m Model) int {
	return planViewerInput(m).VisibleHeight()
}

// planViewerRows wraps the loaded plan lines to the model's current width.
func planViewerRows(m Model) []tuiplans.ViewerRow {
	return tuiplans.ViewerRows(m.planViewerLines, m.width)
}

// renderPlanView renders the full ModePlanView layout: a one-line header
// summarising the plan and a scrollable list of wrapped visual rows below.
func renderPlanView(m Model) string {
	return planViewerInput(m).Render()
}

// renderPlanViewBody renders wrapped visual rows derived from the logical
// source lines in planViewerLines.
func renderPlanViewBody(m Model, height int) string {
	return planViewerInput(m).RenderBody(height)
}
