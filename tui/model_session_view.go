// This file is what the root model does about an open conversation: entering
// and leaving the transcript viewer, moving through its messages and matches,
// and the single-message detail pane one of those messages can expand into.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
)

// viewerMessagesLoadedMsg carries the result of an async ReadMessages call.
// seq matches the m.viewerLoadSeq snapshot at dispatch time so a slow load
// arriving after the user pressed esc (or opened a different session) is
// dropped silently in the Update handler.
type viewerMessagesLoadedMsg struct {
	seq      int
	messages []transcript.Message
	err      error
}

// enterSessionView switches to ModeSessionView for the given transcript and
// kicks off the async ReadMessages dispatch. Caller is responsible for
// passing in a SessionMeta whose Harness has a registered reader in
// m.transcriptReaders.
func (m Model) enterSessionView(meta transcript.SessionMeta) (tea.Model, tea.Cmd) {
	m.mode = ModeSessionView
	metaCopy := meta
	m.viewerSession = &metaCopy
	m.viewerMessages = nil
	m.viewerCursor = 0
	m.viewerScroll = 0
	m.viewerMatchIdx = 0
	m.viewerMatchLines = nil
	m.viewerLoading = true
	m.viewerLoadErr = nil
	m.viewerLoadSeq++
	seq := m.viewerLoadSeq

	reader, ok := m.transcriptReaders[meta.Harness]
	if !ok {
		m.viewerLoading = false
		m.viewerLoadErr = errUnknownHarness(meta.Harness)
		return m, nil
	}
	return m, readViewerMessagesCmd(seq, reader, meta.Path)
}

// exitSessionView returns to ModeSessionSearch and zeros every viewer-local
// field. The search-list state (query, results, cursor, chip filters) is
// preserved so the user lands back on the same row they entered from.
func (m *Model) exitSessionView() {
	m.mode = ModeSessionSearch
	m.viewerSession = nil
	m.viewerMessages = nil
	m.viewerCursor = 0
	m.viewerScroll = 0
	m.viewerMatchIdx = 0
	m.viewerMatchLines = nil
	m.viewerLoading = false
	m.viewerLoadErr = nil
}

// handleSessionViewKey processes keys while ModeSessionView is active.
// Mirrors handleDetailKey's j/k/g/G/pgup/pgdn/esc and adds n/N for jumping
// through the match lines computed at load time.
func (m Model) handleSessionViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := len(m.viewerMessages)
	visible := sessionViewVisibleHeight(m)
	lastIdx := total - 1
	if lastIdx < 0 {
		lastIdx = 0
	}

	switch msg.String() {
	case keyEsc, keyQ:
		m.exitSessionView()
		return m, nil
	case keyQuestion:
		m.openHelp(ModeSessionView)
		return m, nil
	case keySlash:
		// Back to the result list with the text query active, as the plan
		// viewer does, so a query can be refined without esc then /.
		m.exitSessionView()
		m.sessionSearchMode = true
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.viewerCursor < lastIdx {
			m.viewerCursor++
		}
		scrollViewerToCursor(&m)
	case keyK, keyUp:
		if m.viewerCursor > 0 {
			m.viewerCursor--
		}
		scrollViewerToCursor(&m)
	case keyCtrlD:
		step := halfPageStep(visible)
		m.viewerCursor += step
		if m.viewerCursor > lastIdx {
			m.viewerCursor = lastIdx
		}
		scrollViewerToCursor(&m)
	case keyCtrlU:
		step := halfPageStep(visible)
		m.viewerCursor -= step
		if m.viewerCursor < 0 {
			m.viewerCursor = 0
		}
		scrollViewerToCursor(&m)
	case keyPgDown, keyCtrlF:
		m.viewerCursor += visible
		if m.viewerCursor > lastIdx {
			m.viewerCursor = lastIdx
		}
		scrollViewerToCursor(&m)
	case keyPgUp, keyCtrlB:
		m.viewerCursor -= visible
		if m.viewerCursor < 0 {
			m.viewerCursor = 0
		}
		scrollViewerToCursor(&m)
	case keyG:
		m.viewerCursor = 0
		scrollViewerToCursor(&m)
	case keyGG:
		m.viewerCursor = lastIdx
		scrollViewerToCursor(&m)
	case keyN:
		m.jumpToNextMatch(1)
	case "N":
		m.jumpToNextMatch(-1)
	case keyCopyPath:
		path := currentSessionViewPath(m)
		if path == "" {
			return m, nil
		}
		return m, commands.CopyText(path)
	case keyY:
		if len(m.viewerMessages) == 0 ||
			m.viewerCursor < 0 ||
			m.viewerCursor >= len(m.viewerMessages) {
			return m, nil
		}
		return m, commands.CopyText(m.viewerMessages[m.viewerCursor].Text)
	case keyEnter:
		if len(m.viewerMessages) == 0 ||
			m.viewerCursor < 0 ||
			m.viewerCursor >= len(m.viewerMessages) {
			return m, nil
		}
		return m.enterMessageDetail()
	case keyC:
		if m.viewerSession == nil {
			return m, nil
		}
		return m, m.conversationActionCmd(m.transcriptReaders, *m.viewerSession, actions.Continue)
	case keyF:
		if m.viewerSession == nil {
			return m, nil
		}
		return m, m.conversationActionCmd(m.transcriptReaders, *m.viewerSession, actions.Fork)
	case keyWorktreeFork:
		if m.viewerSession == nil {
			return m, nil
		}
		m.startWorktreeForkConfirm(*m.viewerSession, ModeSessionView)
		return m, nil
	case "H", "E":
		if m.viewerSession == nil {
			return m, nil
		}
		m.startHandoffPicker(*m.viewerSession, ModeSessionView, msg.String() == "E")
		return m, nil
	case keyCopySessionID:
		if m.viewerSession == nil || m.viewerSession.ID == "" {
			return m, nil
		}
		return m, commands.CopyText(m.viewerSession.ID)
	}
	return m, nil
}

// jumpToNextMatch advances viewerMatchIdx by step (+1 for `n`, -1 for `N`)
// with wrap-around, then moves viewerCursor to the resulting match line and
// lets scrollViewerToCursor follow.
func (m *Model) jumpToNextMatch(step int) {
	n := len(m.viewerMatchLines)
	if n == 0 {
		return
	}
	m.viewerMatchIdx = (m.viewerMatchIdx + step + n) % n
	m.viewerCursor = m.viewerMatchLines[m.viewerMatchIdx]
	scrollViewerToCursor(m)
}

// readViewerMessagesCmd wraps reader.ReadMessages in a tea.Cmd so the parse
// happens off the UI goroutine. seq is forwarded back on the result message
// so a stale load (user already moved on) can be dropped in Update.
func readViewerMessagesCmd(seq int, reader transcript.Reader, path string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := reader.ReadMessages(path)
		return viewerMessagesLoadedMsg{seq: seq, messages: msgs, err: err}
	}
}

// errUnknownHarness reports a missing reader entry for a harness name. The
// search view should never route an Enter that produces this — it's defence
// in depth in case the readers map drifts from the index contents.
type unknownHarnessErr struct{ harness string }

func (e unknownHarnessErr) Error() string {
	return "no reader registered for harness " + e.harness
}

func errUnknownHarness(name string) error { return unknownHarnessErr{harness: name} }

// enterMessageDetail opens the full-message overlay for the message at
// viewerCursor. Callers should only invoke this when m.viewerMessages is
// non-empty and 0 <= m.viewerCursor < len(m.viewerMessages); the guard
// belongs at the call site so this helper stays a pure mode transition.
func (m Model) enterMessageDetail() (tea.Model, tea.Cmd) {
	m.mode = ModeMessageDetail
	m.messageDetailIdx = m.viewerCursor
	m.messageDetailScroll = 0
	return m, nil
}

// exitMessageDetail returns to ModeSessionView and zeroes the overlay
// state. viewerCursor is preserved so the user lands back on the same row
// they opened the overlay from.
func (m *Model) exitMessageDetail() {
	m.mode = ModeSessionView
	m.messageDetailIdx = -1
	m.messageDetailScroll = 0
}

// handleMessageDetailKey mirrors handleHelpKey: scroll-only navigation
// with esc/q/enter all closing the overlay. ctrl+c still quits the app.
func (m Model) handleMessageDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := messageDetailScrollBounds(m)
	switch msg.String() {
	case keyEsc, keyQ, keyEnter:
		m.exitMessageDetail()
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.messageDetailScroll+visible < total {
			m.messageDetailScroll++
		}
	case keyK, keyUp:
		if m.messageDetailScroll > 0 {
			m.messageDetailScroll--
		}
	case keyPgDown:
		m.messageDetailScroll += visible
		if m.messageDetailScroll+visible > total {
			m.messageDetailScroll = total - visible
		}
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	case keyPgUp:
		m.messageDetailScroll -= visible
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	case keyG:
		m.messageDetailScroll = 0
	case keyGG:
		m.messageDetailScroll = total - visible
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	}
	return m, nil
}

// clampMessageDetailScroll pins m.messageDetailScroll to [0, max(0, total -
// visible)] at the model's current dimensions. Mirrors clampHelpOffset:
// called from the WindowSizeMsg handler so a viewport shrink doesn't leave
// a stale offset the user has to scroll through before the overlay reacts.
func clampMessageDetailScroll(m *Model) {
	total, visible := messageDetailScrollBounds(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.messageDetailScroll > maxOffset {
		m.messageDetailScroll = maxOffset
	}
	if m.messageDetailScroll < 0 {
		m.messageDetailScroll = 0
	}
}
