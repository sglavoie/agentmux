package daemon

// user_active (F11 / the dashboard's "Mark active") is the operator forcing ⚡
// onto a tab. It acknowledges whatever is visible exactly as user_clear does,
// then contributes a bolt under its own synthetic source, which the next real
// hook/ASL event retires before it applies. Probe facts never retire it, and it
// ages out at StaleActive like any other ⚡.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// userActiveOwnerFixture is a one-pane managed Claude tab showing icon.
func userActiveOwnerFixture(t *testing.T, icon string) (*Handler, *fakeKittyClient) {
	t.Helper()
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitle("api", icon, false),
		[]kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}},
	)}
	h.SetKittyClient(fake)
	return h, fake
}

// seedHarnessAction folds one real session-scoped Claude action.
func seedHarnessAction(t *testing.T, h *Handler, windowID int, action, event string, at time.Time) {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:sess"},
		Action:    action,
		SessionID: "sess",
		Event:     event,
		At:        at.Unix(),
		Prompt:    event == "UserPromptSubmit",
	}
	req := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: event, IsPrompt: ev.Prompt, Normalized: &ev}
	if resp := h.handleStateUpdate(req); !resp.OK {
		t.Fatalf("seed %s rejected: %+v", action, resp)
	}
}

func markActive(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "user_active"}); !resp.OK {
		t.Fatalf("user_active response = %+v", resp)
	}
}

func TestHandler_UserActiveReplacesSettledIcons(t *testing.T) {
	cases := []struct {
		action string
		icon   string
	}{
		{"stop", "stop"},
		{"permission", "key"},
		{"stop_failure", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.icon, func(t *testing.T) {
			h, _ := userActiveOwnerFixture(t, tc.icon)
			seedHarnessAction(t, h, 42, tc.action, "Stop", time.Now())
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.icon {
				t.Fatalf("seeded effective = %+v, want %s", eff, tc.icon)
			}

			markActive(t, h, 42)

			eff, desired := windowEffective(t, h, 42)
			if eff.Icon != "bolt" || desired != "bolt" {
				t.Fatalf("after user_active: effective %+v desired %q, want bolt", eff, desired)
			}
			if eff.Source != protocol.UserActiveSource() {
				t.Fatalf("bolt source = %+v, want the user_active source", eff.Source)
			}
			if g := h.guard.Current(42); g != "active" {
				t.Fatalf("guard = %q, want active", g)
			}
			h.stateMu.Lock()
			dirty := h.store[42].Dirty
			h.stateMu.Unlock()
			if !dirty {
				t.Fatal("window not dirty after user_active; the ⚡ would wait for maintenance")
			}
		})
	}
}

func TestHandler_UserActiveFromSiblingPanesTargetsOwner(t *testing.T) {
	// The managed case mirrors TestHandler_UserClearFromManagedNonOwnerTargetsOwner:
	// the active managed pane owns the title, and F11 arrives from the other one.
	siblings := map[string][2]kitty.Window{
		"unmanaged": {
			{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
			{ID: 43, IsActive: true},
		},
		"managed": {
			{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
			{ID: 43, UserVars: kitty.UserVars{Profile: "scratch", Harness: harness.Claude}},
		},
	}
	for name, panes := range siblings {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler()
			owner, sibling := panes[0], panes[1]
			fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
				palette.BuildTitle("api", "stop", false),
				[]kitty.Window{owner, sibling},
			)}
			h.SetKittyClient(fake)
			seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())

			markActive(t, h, 43)

			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("owner effective = %+v, want bolt", eff)
			}
			h.stateMu.Lock()
			src := h.store[43]
			h.stateMu.Unlock()
			if src != nil && src.Effective.Icon != "" {
				t.Fatalf("source pane effective = %+v, want untouched", src.Effective)
			}
			for _, ev := range h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43}).Events {
				if ev.SuppressionReason == suppressionReasonOwnerMismatch {
					t.Fatalf("source event = %+v; want no owner_mismatch", ev)
				}
			}
		})
	}
}

func TestHandler_UserActiveOwnerlessIconFreeTabIsNoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab("api", []kitty.Window{{ID: 43, IsActive: true}})}
	h.SetKittyClient(fake)

	markActive(t, h, 43)

	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st != nil && (st.DesiredIcon != "" || st.StateSet || st.Dirty) {
		t.Fatalf("state = %+v; ownerless icon-free user_active must not create renderer state", st)
	}
}

// TestHandler_UserActiveNextRealEventDecides pins the retirement contract: the
// next real hook event replaces the forced ⚡ with its own outcome.
func TestHandler_UserActiveNextRealEventDecides(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after real Stop = %+v, want stop", eff)
		}
	})
	t.Run("permission", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "permission", "PermissionRequest", time.Now())
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "key" {
			t.Fatalf("effective after real permission = %+v, want key", eff)
		}
	})
	t.Run("prompt", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "active", "UserPromptSubmit", time.Now())
		eff, _ := windowEffective(t, h, 42)
		if eff.Icon != "bolt" || eff.Source.Kind != protocol.SourceHarness {
			t.Fatalf("effective after real prompt = %+v, want the harness's own bolt", eff)
		}
		h.stateMu.Lock()
		retired := h.store[42].Reducer.RetireUserActive(time.Now().Unix())
		h.stateMu.Unlock()
		if retired {
			t.Fatal("the user_active fact was still live after a real prompt")
		}
	})
}

// TestHandler_UserActiveFromDashboardRetiresLikeF11 covers the un-stamped
// state_update the TUI sends: it must land on the same retirable source.
func TestHandler_UserActiveFromDashboardRetiresLikeF11(t *testing.T) {
	h := newTestHandler()
	seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_active"}); !resp.OK {
		t.Fatalf("user_active response = %+v", resp)
	}
	eff, _ := windowEffective(t, h, 42)
	if eff.Icon != "bolt" || eff.Source != protocol.UserActiveSource() {
		t.Fatalf("effective after dashboard mark = %+v, want the user_active bolt", eff)
	}
	seedHarnessAction(t, h, 42, "stop", "Stop", time.Now().Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after real Stop = %+v, want stop", eff)
	}
}

// TestHandler_UserActiveKeepsClearHoldAndAgesOut pins the F8-then-F11 flow on a
// stale Claude title: the forced ⚡ survives the screen probes, keeps the clear's
// promotion hold, and ages out at StaleActive to an empty tab rather than to a
// re-promoted ⚡ or a probe-settled 🛑.
func TestHandler_UserActiveKeepsClearHoldAndAgesOut(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "✳ File refactoring for code size reduction"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.SetKittyClient(fake)

	claudeSeedActiveBolt(t, h, 42, "sess-done", base.Unix())
	seedHarnessAction(t, h, 42, "stop", "Stop", base.Add(time.Second))
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	markActive(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after F8 then F11 = %+v, want bolt", eff)
	}
	h.stateMu.Lock()
	held := h.store[42].BusyPromotionHeld()
	h.stateMu.Unlock()
	if !held {
		t.Fatal("user_active released the clear's busy-promotion hold")
	}

	marked := base.Add(2 * time.Second)
	for sample := 1; sample <= claudeIdleProbeLongestRun+2; sample++ {
		h.ProbeClaudeScreenStop(marked.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after probe sample %d = %+v, want the forced bolt to hold", sample, eff)
		}
	}

	aged := marked.Add(11 * time.Minute)
	h.SweepStaleWithSnapshot(aged, nil)
	h.ProbeClaudeScreenStop(aged.Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after the forced bolt aged out = %+v, want empty", eff)
	}
	for _, name := range []string{screenActiveProbeEvent, screenStopProbeEvent} {
		if got := countEventsNamed(t, h, 42, name); got != 0 {
			t.Fatalf("%s rows = %d, want 0 under the forced bolt and the held clear", name, got)
		}
	}
}
