package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestHandler_StateQuery(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true for state_query, got %+v", resp)
	}
	if resp.Activity == nil {
		t.Fatalf("want non-nil Activity map, got %+v", resp)
	}
	if len(resp.Activity) != 0 {
		t.Fatalf("want empty Activity map for fresh handler, got %+v", resp.Activity)
	}
}

func TestHandler_StateQueryReturnsActivity(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active"})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "permission"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	if _, ok := resp.Activity[7]; !ok {
		t.Errorf("expected Activity[7], got %+v", resp.Activity)
	}
	if _, ok := resp.Activity[9]; !ok {
		t.Errorf("expected Activity[9], got %+v", resp.Activity)
	}
}

func TestHandler_StateQueryReturnsDaemonStateSnapshot(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "permission"})
	h.stateMu.Lock()
	h.store[7].Looping = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	got, ok := resp.States[7]
	if !ok {
		t.Fatalf("state query omitted window 7: %+v", resp.States)
	}
	if got.IconState != "key" || !got.Looping || got.LastAction != "permission" || got.LastEventAt == 0 || got.GuardAction != "permission" {
		t.Fatalf("state snapshot = %+v, want key looping permission timestamp guard", got)
	}
}

// TestHandler_StateQueryReportsUnseenAndOmitsItWhenSeen pins the wire half of
// the unseen bit. The projection carries the durable field unchanged, and the
// inversion has to survive the encoding as well as the struct: a seen window
// must write no `unseen` key at all, so a dashboard reading a response from a
// daemon too old to carry the field decodes exactly what a seen window decodes.
func TestHandler_StateQueryReportsUnseenAndOmitsItWhenSeen(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "stop"})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "stop"})
	// Seeded rather than driven through a focus snapshot: this test is about the
	// projection, and the transitions that raise and clear the bit have their own
	// table in shadow_unseen_test.go.
	seedUnseen(h, 7, true, false)
	seedUnseen(h, 9, false, false)

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	got, ok := resp.States[7]
	if !ok {
		t.Fatalf("state query omitted window 7: %+v", resp.States)
	}
	if !got.Unseen {
		t.Fatalf("Unseen = false for an unread stop, want true: %+v", got)
	}
	seen, ok := resp.States[9]
	if !ok {
		t.Fatalf("state query omitted window 9: %+v", resp.States)
	}
	if seen.Unseen {
		t.Fatalf("Unseen = true for a read stop, want false: %+v", seen)
	}

	encoded, err := json.Marshal(seen)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if strings.Contains(string(encoded), "unseen") {
		t.Fatalf("seen snapshot wrote the field on the wire: %s", encoded)
	}
	raised, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if !strings.Contains(string(raised), `"unseen":true`) {
		t.Fatalf("unseen snapshot did not write the field on the wire: %s", raised)
	}
}

// TestHandler_StateQueryReportsPromptKindAndOmitsItWhenUnlabelled pins the wire
// half of the blocked reason. The projection carries the durable field
// unchanged, and absent has to survive the encoding as well as the struct: a
// blocked window nothing labelled must write no `reason` key at all, so a
// dashboard reading a response from a daemon too old to carry the field decodes
// exactly what the unlabelled window decodes. That equivalence is what makes
// "no reason" a supported steady state rather than a version skew to detect.
func TestHandler_StateQueryReportsPromptKindAndOmitsItWhenUnlabelled(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "permission"})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "permission"})
	// Seeded rather than driven through a screen probe: this test is about the
	// projection. Which prompt produces which kind is pinned by the probe tables
	// in shadow_prompt_kind_test.go and claude_prompt_kind_test.go, and the rule
	// that clears a kind when the icon stops blocking belongs to
	// stampPromptKindLocked, which has its own coverage.
	h.stateMu.Lock()
	h.store[7].PromptKind = "command_approval"
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	got, ok := resp.States[7]
	if !ok {
		t.Fatalf("state query omitted window 7: %+v", resp.States)
	}
	if got.Reason != "command_approval" {
		t.Fatalf("Reason = %q for a labelled prompt, want %q: %+v", got.Reason, "command_approval", got)
	}
	bare, ok := resp.States[9]
	if !ok {
		t.Fatalf("state query omitted window 9: %+v", resp.States)
	}
	if bare.Reason != "" {
		t.Fatalf("Reason = %q for a blocked window no probe labelled, want empty: %+v", bare.Reason, bare)
	}

	encoded, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if strings.Contains(string(encoded), "reason") {
		t.Fatalf("unlabelled snapshot wrote the field on the wire: %s", encoded)
	}
	labelled, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if !strings.Contains(string(labelled), `"reason":"command_approval"`) {
		t.Fatalf("labelled snapshot did not write the field on the wire: %s", labelled)
	}
}

func TestHandler_StateQueryReportsBusySinceAndOmitsItWhenIdle(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", IsPrompt: true})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 9, Action: "permission"})

	anchor := busySince(t, h, 7)
	if anchor.IsZero() {
		t.Fatal("active window has no anchor to report")
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	busy, ok := resp.States[7]
	if !ok {
		t.Fatalf("state query omitted window 7: %+v", resp.States)
	}
	if busy.BusySinceAt != anchor.Unix() {
		t.Fatalf("BusySinceAt = %d, want the daemon's anchor %d", busy.BusySinceAt, anchor.Unix())
	}
	idle, ok := resp.States[9]
	if !ok {
		t.Fatalf("state query omitted window 9: %+v", resp.States)
	}
	if idle.BusySinceAt != 0 {
		t.Fatalf("BusySinceAt = %d for a paused window, want 0", idle.BusySinceAt)
	}

	encoded, err := json.Marshal(idle)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if strings.Contains(string(encoded), "busy_since_at") {
		t.Fatalf("idle snapshot wrote the field on the wire: %s", encoded)
	}
}

func TestHandler_PromptBypassesStopGuard(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "stop"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:bolt} for prompt bypass, got %+v", resp)
	}
}

func TestHandler_Shutdown(t *testing.T) {
	h := newTestHandler()
	called := make(chan struct{}, 1)
	h.onShutdown = func() { called <- struct{}{} }

	resp := h.Handle(protocol.Request{Type: protocol.TypeShutdown})
	if !resp.OK {
		t.Fatalf("want ok=true for shutdown, got %+v", resp)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("onShutdown not called within 1s")
	}
}
