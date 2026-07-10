package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestRestoreFromUserVarsRestoresStateAndFutureGuard(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	guardExpiry := now.Add(time.Minute)
	encoded, err := WindowState{
		Activity:       now.Add(-time.Minute),
		LastAction:     "permission",
		LastAccount:    "work",
		LastHarness:    "codex",
		Looping:        true,
		GuardAction:    "permission",
		GuardExpiresAt: guardExpiry,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: encoded},
			}},
		}},
	}}, now)

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("restored state missing")
	}
	if st.LastAction != "permission" || st.DesiredIcon != "key" || !st.Looping || !st.LoopingSet || !st.Dirty {
		t.Fatalf("restored state = %+v, want action=permission desired=key looping dirty", st)
	}
	if got := h.guard.Current(42); got != "permission" {
		t.Fatalf("guard.Current(42) = %q, want permission", got)
	}
}

func TestRestoreFromUserVarsSkipsExpiredGuardAndUnmanagedWindows(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	encoded, err := WindowState{
		LastAction:     "active",
		GuardAction:    "active",
		GuardExpiresAt: now.Add(-time.Second),
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}

	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{
				{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: encoded}},
				{ID: 43, UserVars: kitty.UserVars{AMXState: encoded}},
			},
		}},
	}}, now)

	h.stateMu.Lock()
	_, managedOK := h.store[42]
	_, unmanagedOK := h.store[43]
	h.stateMu.Unlock()
	if !managedOK {
		t.Fatalf("managed window was not restored")
	}
	if unmanagedOK {
		t.Fatalf("unmanaged window was restored")
	}
	if got := h.guard.Current(42); got != "" {
		t.Fatalf("expired guard restored as %q", got)
	}
}
