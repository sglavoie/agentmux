package daemon

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestHookRoutingReadsCacheNotLiveList proves the hook path resolves ownership
// from the published snapshot and never calls ListWindows: after priming the
// cache, the live client is set to fail every list, yet a hook still enriches
// correctly from the cache (current icon, profile, harness).
func TestHookRoutingReadsCacheNotLiveList(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows(palette.BuildTitle("api", "key", false), []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
		}}),
	}
	// SetKittyClient primes the cache from the good windows above.
	h.SetKittyClient(fake)

	// Now make any live ListWindows fail. If the hook path listed live, routing
	// would degrade; instead it must read the primed cache.
	fake.mu.Lock()
	fake.listErr = errors.New("kitty is wedged")
	fake.mu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %d, want 1 (%+v)", len(detail.Events), detail.Events)
	}
	ev := detail.Events[0]
	if ev.Action != "active" || ev.CurrentIconState != "key" {
		t.Fatalf("event = %+v; want active enriched with current icon key from the cache", ev)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.LastAccount != "work" {
		t.Fatalf("state = %+v; want account work resolved from the cached snapshot", st)
	}
}

// TestHookRoutingColdCacheFallsBackToSourcePane confirms that before any
// snapshot is published, a user_clear cannot silently vanish: an unreadable tab
// falls back to wiping the source pane rather than being dropped.
func TestHookRoutingColdCacheFallsBackToSourcePane(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	// Install a client but never prime the cache: use the field-only setter so
	// the snapshot stays cold, modelling a degraded startup that has not yet
	// reconciled.
	h.useKittyClient(&fakeKittyClient{})

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	// The cold-cache fallback wipes the source pane, so it must have a state entry.
	h.stateMu.Lock()
	_, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatal("cold-cache user_clear should fall back to wiping the source pane, not drop silently")
	}
}
