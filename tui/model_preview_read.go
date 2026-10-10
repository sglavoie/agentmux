// This file is the inspector's read controller: the one place that decides when
// the pane asks Kitty for scrollback, how many of those reads may be outstanding
// at once, and which of the answers that come back is allowed to paint.
//
// Two identities travel with every read and they answer different questions. The
// TARGET generation (previewTargetSeq, bumped by retargetPreview) says what the
// pane is pointed at and decides whether CONTENT may paint. The REQUEST identity
// (previewRequestSeq, drawn once per fetch) says which read this is and decides
// only whether the in-flight slot may be RELEASED. Keeping them apart is what
// makes a stale reply harmless instead of fatal: a read issued for the selection
// the operator has already left still hands back the slot it holds, and its text
// is dropped on the floor. Settling on the target generation instead would leave
// the slot held by a reply nobody will ever accept, and the pane would never read
// again — the exact starvation the split exists to prevent.
//
// The concurrency contract is one read at a time, one coalesced catch-up behind
// it. A burst of cursor moves, ticks, and refreshes costs one extra read in
// total rather than one each, and nothing here ever blocks the update loop: the
// read runs inside a tea.Cmd and its worst case is the Kitty runner's own
// subprocess deadline (internal/kitty/exec.go), which this file reuses as its
// timeout boundary rather than adding a second one.

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
)

// requestPreviewRead asks for a read of whatever the pane is pointed at now. If
// a read is already out the request collapses into the pending bit and no
// command is returned: the reply that settles the outstanding read starts the
// single catch-up, and that catch-up reads the target as it stands by then, not
// the one that asked.
//
// Callers establish that the pane is visible; the catch-up re-checks it, because
// an unbounded amount of time passes between asking and being served.
func (m *Model) requestPreviewRead() tea.Cmd {
	if m.previewReadInFlight {
		m.previewReadPending = true
		return nil
	}
	return m.startPreviewRead()
}

// startPreviewRead draws the next request identity and dispatches the fetch. It
// assumes the caller has established that no read is in flight.
func (m *Model) startPreviewRead() tea.Cmd {
	m.previewRequestSeq++
	m.previewReadActiveSeq = m.previewRequestSeq
	m.previewReadInFlight = true
	m.previewReadPending = false
	if read := m.backgroundCommands.scrollback; read != nil {
		return fetchPreviewWith(read, m.previewWindowID, m.previewTargetSeq, m.previewReadActiveSeq)
	}
	return fetchPreviewCmd(m.client, m.previewWindowID, m.previewTargetSeq, m.previewReadActiveSeq)
}

// settlePreviewRead reports whether a reply carrying this request identity is
// the read currently in flight, releasing the slot when it is. A reply that
// arrives with nothing outstanding was already settled, and one stamped with
// another identity belongs to a read this controller is no longer waiting on;
// neither may release a slot it does not hold.
func (m *Model) settlePreviewRead(requestSeq int) bool {
	if !m.previewReadInFlight || requestSeq != m.previewReadActiveSeq {
		return false
	}
	m.previewReadInFlight = false
	return true
}

// catchUpPreviewRead starts the single read that coalesced while the one just
// settled was out. A pane that has since been hidden drops the request instead:
// whichever path shows it again reads for itself, and polling a surface nobody
// is looking at is the cost this controller exists to avoid.
func (m *Model) catchUpPreviewRead() tea.Cmd {
	if m.previewReadInFlight || !m.previewReadPending {
		return nil
	}
	if !m.activePreviewEnabled() {
		m.previewReadPending = false
		return nil
	}
	return m.startPreviewRead()
}

// previewAcceptsContent reports whether a reply describes what the pane is
// pointed at right now. Both halves of the target identity are checked. The
// generation alone would accept a stale window under a reused counter after a
// restore, and the window alone cannot tell a read issued before the cursor
// detoured through a family summary from one issued after it came back.
func (m Model) previewAcceptsContent(msg previewContentMsg) bool {
	if !m.activePreviewEnabled() {
		return false
	}
	return msg.targetSeq == m.previewTargetSeq && msg.windowID == m.previewWindowID
}

// startPreviewPoll retires the current polling chain and opens a new one for the
// visible pane. Every path that (re)shows a pane goes through here, so showing
// one twice in quick succession cannot leave two chains alive: the bump alone
// retires whatever ticks the old chain still has in flight.
func (m *Model) startPreviewPoll() tea.Cmd {
	if !m.activePreviewEnabled() {
		return nil
	}
	m.previewPollSeq++
	return previewTickCmd(previewRefreshInterval(m.cfg), m.previewPollSeq)
}

// previewRefreshInterval is the configured poll cadence. Config normalization
// supplies the 500ms default and refuses anything under 50ms, so this is a
// straight conversion rather than a second policy.
func previewRefreshInterval(cfg *config.Config) time.Duration {
	return time.Duration(cfg.Defaults.Preview.RefreshMs) * time.Millisecond
}
