package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// idleSeedHarness folds a harness event into a window through the normal state
// path so the window has a live reducer and effective state before an
// authoritative idle observation is applied.
func idleSeedHarness(t *testing.T, h *Handler, windowID int, session, action string, at int64) {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:" + session},
		Action:    action,
		SessionID: session,
		At:        at,
		Event:     "Evt",
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: "Evt", Normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed harness %s/%s rejected: %+v", session, action, resp)
	}
}

// idleSeedHarnessSeq folds a harness event carrying an EXPLICIT per-source
// sequence into a window, so a test can land an accepted mutation whose wire
// sequence is at or below the current window receipt generation.
func idleSeedHarnessSeq(t *testing.T, h *Handler, windowID int, session, action string, seq uint64, at int64) {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:" + session},
		Action:    action,
		SessionID: session,
		Seq:       seq,
		At:        at,
		Event:     "Evt",
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: "Evt", Normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed harness %s/%s seq %d rejected: %+v", session, action, seq, resp)
	}
}

// idleShadowSeq reads a window's current reducer receipt sequence, the value an
// adapter captures before its screen read.
func idleShadowSeq(t *testing.T, h *Handler, windowID int) uint64 {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.ShadowSeq
}
