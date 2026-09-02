package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestUserVarV1IgnoresBusySinceAndSelfHealsOnRestore pins the deliberate gap in
// migrateReducerV1: the anchor is a v2+ field, so a v1 payload restores zero
// even if something injected a bs key, and the restore-time invariant re-stamps
// it from the icon the window actually restored with.
func TestUserVarV1IgnoresBusySinceAndSelfHealsOnRestore(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	raw := fmt.Sprintf(`{"a":"active","acct":"work","h":"codex","t":%d,"bs":1710000000}`, now.Add(-time.Minute).Unix())

	var decoded state.WindowState
	if err := decoded.UnmarshalUserVar(raw); err != nil {
		t.Fatalf("UnmarshalUserVar(v1) error: %v", err)
	}
	if !decoded.BusySince.IsZero() {
		t.Fatalf("v1 restore read an anchor = %v; want zero", decoded.BusySince)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: raw},
			}},
		}},
	}}, now)

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	if desiredIconForState(st) != "bolt" {
		t.Fatalf("restored icon = %q, want bolt", desiredIconForState(st))
	}
	if !st.BusySince.After(now.Add(-time.Second)) {
		t.Fatalf("anchor = %v; want it self-healed to restore time, not the payload's stale value", st.BusySince)
	}
}
