package daemon

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// screenProbeMutationFingerprint captures every state family a discarded
// screen/transcript observation is forbidden to touch. Encoding the value
// gives tests a simple exact comparison while retaining reducer facts, audit
// rows, durable metadata, mute state, notification state, and render state.
func screenProbeMutationFingerprint(t *testing.T, h *Handler, windowID int) string {
	t.Helper()
	type fingerprint struct {
		WindowState         string
		Events              []protocol.SessionEvent
		EventSeq            uint64
		Metadata            protocol.WindowDebugMetadata
		Guard               string
		RenderPending       bool
		PendingNotification bool
		IdleSamples         [3]int
		IdleGenerations     [3]int64
	}

	h.stateMu.Lock()
	st := h.store[windowID]
	if st == nil {
		h.stateMu.Unlock()
		t.Fatalf("window %d has no state", windowID)
	}
	windowState, err := st.MarshalUserVar()
	if err != nil {
		h.stateMu.Unlock()
		t.Fatalf("marshal window %d state: %v", windowID, err)
	}
	refs := [3]idleSampleRef{
		{windowID: windowID, key: screenPromptProbeSource},
		{windowID: windowID, key: claudeScreenStopProbeSource},
		{windowID: windowID, key: copilotTranscriptStopProbeSource},
	}
	got := fingerprint{
		WindowState: windowState,
		Events:      append([]protocol.SessionEvent(nil), st.EventLog...),
		EventSeq:    h.eventSeq,
		Metadata:    h.windowMetadata[windowID],
	}
	for i, ref := range refs {
		got.IdleSamples[i] = h.idleSampleStreak[ref]
		got.IdleGenerations[i] = h.idleSampleGen[ref]
	}
	h.stateMu.Unlock()

	got.Guard = h.guard.Current(windowID)
	h.renderQueue.mu.Lock()
	got.RenderPending = h.renderQueue.pending[windowID]
	h.renderQueue.mu.Unlock()
	if h.notifier != nil {
		h.notifier.mu.Lock()
		_, got.PendingNotification = h.notifier.pending[windowID]
		h.notifier.mu.Unlock()
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal screen-probe fingerprint: %v", err)
	}
	return string(encoded)
}

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
