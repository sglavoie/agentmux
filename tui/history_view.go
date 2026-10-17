package tui

import (
	"time"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/history"
)

// This file is the seam between the root Model and the tui/history leaf. The
// leaf owns the session-history domain — the per-session event detail overlay
// and its timeline sub-view, the Past Sessions search results, the transcript
// viewer and its message overlay, and the transcript delete prompt — and reads
// its facts from explicit input values. The Model stays here, and every
// adapter below turns "what the model currently holds" into "what the history
// views need to answer a question".

// historyDetailInput projects the model onto the detail overlay's input.
// BodyHeight is computed here rather than in the leaf because charging the
// root footer and status bar is app orchestration.
func historyDetailInput(m Model) history.DetailInput {
	return history.DetailInput{
		Events:         m.detailEvents,
		WindowID:       m.detailWindowID,
		Sessions:       m.sessions,
		Cfg:            m.cfg,
		Watchdog:       m.watchdog,
		Err:            m.detailErr,
		Loading:        m.detailLoading,
		Scroll:         m.detailScroll,
		Width:          m.width,
		Height:         m.height,
		BodyHeight:     rootBodyHeight(m),
		ViewTimeline:   m.detailViewTimeline,
		ViewMetadata:   m.detailMetadata,
		TimelineScroll: m.detailTimelineScroll,
		TimelineFold:   m.detailTimelineFold,
		ShowSuppressed: m.detailShowSuppressed,
		ShowTimestamps: m.detailShowTimestamps,
		Now:            time.Now(),
	}
}

func renderDetail(m Model) string {
	return historyDetailInput(m).Render()
}

func renderDetailBox(m Model) string {
	return historyDetailInput(m).RenderBox()
}

func renderTimeline(m Model) string {
	return historyDetailInput(m).RenderTimeline()
}

func detailHeader(m Model) string {
	return historyDetailInput(m).Header()
}

func detailFooter(m Model, total, visible int) string {
	return historyDetailInput(m).Footer(total, visible)
}

func detailScrollBounds(m Model, visibleHeight int) (total, visible int) {
	return historyDetailInput(m).ScrollBounds(visibleHeight)
}

func detailVisibleHeight(m Model) int {
	return historyDetailInput(m).VisibleHeight()
}

// clampDetailScroll pins m.detailScroll to the range the current dimensions
// allow. Called from the WindowSizeMsg handler so a resize that shrinks the
// viewport doesn't leave a stale offset behind.
func clampDetailScroll(m *Model) {
	m.detailScroll = historyDetailInput(*m).ClampScroll()
}

// ── Past Sessions ─────────────────────────────────────────────────────────────

// historySearchInput projects the model onto the Past Sessions input. The chip
// filters arrive as single values because the chip picker (a root concern)
// already collapsed each multi-select down to at most one active entry.
func historySearchInput(m Model) history.SearchInput {
	return history.SearchInput{
		Results:           m.sessionResults,
		Cursor:            m.sessionCursor,
		Offset:            m.sessionOffset,
		Query:             m.sessionQuery,
		SearchMode:        m.sessionSearchMode,
		Loading:           m.sessionLoading,
		CacheLoadInFlight: m.sessionCacheLoadInFlight,
		IndexReady:        m.transcriptIndex != nil,
		RefreshInFlight:   m.sessionRefreshInFlight,
		RefreshErr:        m.sessionRefreshErr,
		RefreshErrLabel:   sessionRefreshErrorLabel(m),
		SearchErr:         m.sessionSearchErr,
		Width:             m.width,
		Height:            m.height,
		BodyHeight:        rootBodyHeight(m),
		Path:              selectedSessionSearchPath(m),
		HarnessChip:       singleChipValue(m.filterHarness),
		ProfileChip:       singleChipValue(m.filterProfile),
		ProjectChip:       singleChipValue(m.filterProject),
		MessageSourceChip: m.filterMessageSource,
		ProfileColors:     dashboard.ProfileColorMap(m.cfg),
		ContextualMeta:    m.contextualSessionMeta,
	}
}

func renderSessionSearch(m Model) string {
	return historySearchInput(m).Render()
}

func renderSessionSearchList(m Model, height int) string {
	return historySearchInput(m).RenderList(height)
}

func sessionSearchVisibleHeight(m Model) int {
	return historySearchInput(m).VisibleHeight()
}

// sessionRefreshErrorLabel stays in root because the sticky status bar renders
// the same label outside the Past Sessions view.
func sessionRefreshErrorLabel(m Model) string {
	if m.transcriptIndex == nil {
		return "cache load error"
	}
	return "refresh error"
}

// ── Transcript viewer and message overlay ─────────────────────────────────────

// historyViewerInput projects the model onto the transcript viewer's input at
// the model's current mode.
func historyViewerInput(m Model) history.ViewerInput {
	return history.ViewerInput{
		Messages:      m.viewerMessages,
		Session:       m.viewerSession,
		Loading:       m.viewerLoading,
		LoadErr:       m.viewerLoadErr,
		MatchLines:    m.viewerMatchLines,
		MatchIdx:      m.viewerMatchIdx,
		Cursor:        m.viewerCursor,
		Scroll:        m.viewerScroll,
		Width:         m.width,
		Height:        m.height,
		BodyHeight:    rootBodyHeight(m),
		Path:          currentSessionViewPath(m),
		MessageIdx:    m.messageDetailIdx,
		MessageScroll: m.messageDetailScroll,
	}
}

// historyViewerScrollInput sizes the viewer's scroll window under the viewer's
// own footer. Offsets are also recomputed when a transcript load lands behind
// an overlay or a resize arrives from another mode, and those footers are not
// the viewer's height, so reading m.mode there would size the wrong window.
func historyViewerScrollInput(m Model) history.ViewerInput {
	m.mode = ModeSessionView
	return historyViewerInput(m)
}

func renderSessionView(m Model) string {
	return historyViewerInput(m).Render()
}

func sessionViewVisibleHeight(m Model) int {
	return historyViewerInput(m).VisibleHeight()
}

func scrollViewerToCursor(m *Model) {
	m.viewerCursor, m.viewerScroll = historyViewerScrollInput(*m).ScrollToCursor()
}

func clampViewerScroll(m *Model) {
	m.viewerScroll = historyViewerScrollInput(*m).ClampScroll()
}

func messageDetailBodyLines(m Model) []string {
	return historyViewerInput(m).MessageBodyLines()
}

func messageDetailScrollBounds(m Model) (total, visible int) {
	return historyViewerInput(m).MessageScrollBounds()
}

func renderMessageDetail(m Model) string {
	return historyViewerInput(m).RenderMessage()
}

// ── Transcript delete prompt ──────────────────────────────────────────────────

func renderDeleteChoice(m Model) string {
	return history.DeleteInput{Target: m.deleteTarget, Width: m.width}.Render()
}

func renderDeleteChoiceBox(m Model) string {
	return history.DeleteInput{Target: m.deleteTarget, Width: m.width}.RenderBox()
}
