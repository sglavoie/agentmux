package daemon

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// seedOwnerState seeds the guard, desired icon, and last action a managed
// window is currently showing so a follow-up user_clear has something to wipe.
func seedOwnerState(h *Handler, windowID int, action, icon string) {
	h.guard.TryUpdate(windowID, action, false)
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(windowID)
	st.DesiredIcon = icon
	st.StateSet = true
	st.LastAction = action
	h.stateMu.Unlock()
}

func TestHandler_UserClearWipesManagedPaneIcons(t *testing.T) {
	cases := []struct {
		action string
		icon   string
	}{
		{"stop", "stop"},
		{"permission", "key"},
		{"failure", "warning"},
		{"stop_failure", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.icon, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{windows: hookEventWindows(
				palette.BuildTitle("api", tc.icon, false),
				[]kitty.Window{{
					ID:       42,
					IsActive: true,
					UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
				}},
			)}
			h.SetKittyClient(fake)
			seedOwnerState(h, 42, tc.action, tc.icon)

			resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
			if !resp.OK {
				t.Fatalf("user_clear response = %+v", resp)
			}

			h.stateMu.Lock()
			st := h.store[42]
			h.stateMu.Unlock()
			if st == nil || st.DesiredIcon != "" || !st.StateSet || !st.Dirty {
				t.Fatalf("state = %+v; want empty desired icon, StateSet, dirty", st)
			}
			if st.LastAction != "" {
				t.Fatalf("LastAction = %q; want cleared", st.LastAction)
			}
			if g := h.guard.Current(42); g != "" {
				t.Fatalf("guard = %q; want cleared", g)
			}
			detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if len(detail.Events) != 1 || detail.Events[0].Action != "user_clear" || detail.Events[0].Suppressed {
				t.Fatalf("events = %+v; want one allowed user_clear", detail.Events)
			}
		})
	}
}

func TestHandler_UserClearFromUnmanagedSiblingTargetsOwner(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
		palette.BuildTitle("api", "stop", false),
		[]kitty.Window{
			{ID: 42, IsActive: false, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
			{ID: 43, IsActive: true}, // unmanaged sibling: F8 pressed here
		},
	)}
	h.SetKittyClient(fake)
	seedOwnerState(h, 42, "stop", "stop")

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	owner := h.store[42]
	h.stateMu.Unlock()
	if owner == nil || owner.DesiredIcon != "" || !owner.Dirty {
		t.Fatalf("owner state = %+v; want cleared and dirty", owner)
	}
	if g := h.guard.Current(42); g != "" {
		t.Fatalf("owner guard = %q; want cleared", g)
	}

	// The source pane must not log an owner_mismatch suppression.
	srcDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	for _, ev := range srcDetail.Events {
		if ev.SuppressionReason == suppressionReasonOwnerMismatch {
			t.Fatalf("source event = %+v; want no owner_mismatch", ev)
		}
	}
	// The clear is logged against the owner.
	ownerDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(ownerDetail.Events) != 1 || ownerDetail.Events[0].Action != "user_clear" || ownerDetail.Events[0].Suppressed {
		t.Fatalf("owner events = %+v; want one allowed user_clear", ownerDetail.Events)
	}
}

func TestHandler_UserClearFromManagedNonOwnerTargetsOwner(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
		palette.BuildTitle("api", "key", false),
		[]kitty.Window{
			{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex}},     // owner
			{ID: 43, IsActive: false, UserVars: kitty.UserVars{Profile: "scratch", Harness: harness.Codex}}, // managed non-owner
		},
	)}
	h.SetKittyClient(fake)
	seedOwnerState(h, 42, "permission", "key")

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	owner := h.store[42]
	src := h.store[43]
	h.stateMu.Unlock()
	if owner == nil || owner.DesiredIcon != "" || !owner.Dirty {
		t.Fatalf("owner state = %+v; want cleared and dirty", owner)
	}
	if src != nil && (src.DesiredIcon != "" || src.StateSet) {
		t.Fatalf("source state = %+v; managed non-owner must not gain a desired icon", src)
	}
	srcDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	for _, ev := range srcDetail.Events {
		if ev.SuppressionReason == suppressionReasonOwnerMismatch {
			t.Fatalf("source event = %+v; want no owner_mismatch", ev)
		}
	}
}

func TestHandler_UserClearNoOwnerLeavesTabUnchanged(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
		"api",
		[]kitty.Window{{ID: 43, IsActive: true}}, // no managed pane
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	// No durable renderer state may be created for an ownerless tab.
	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st != nil && (st.DesiredIcon != "" || st.StateSet || st.Dirty) {
		t.Fatalf("state = %+v; ownerless user_clear must not create renderer state", st)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 0 {
		t.Fatalf("events = %+v; want none for ownerless no-op", detail.Events)
	}

	// The renderer must not touch the tab title either.
	h.renderSnapshot(fake.windows)
	if calls := fake.titleCalls(); len(calls) != 0 {
		t.Fatalf("SetTabTitle calls = %+v; want none", calls)
	}
}

// TestHandler_UserClearOwnerlessTabWithGlyphClearsSource verifies the
// subprocess case: a harness such as `claude -p` can leave a recognized state
// icon on a tab with no managed title owner, and F8 must still dismiss it.
func TestHandler_UserClearOwnerlessTabWithGlyphClearsSource(t *testing.T) {
	h := newTestHandler()
	liveTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "stop",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	wantTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		Starred: true,
		Muted:   true,
	})
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
		liveTitle,
		[]kitty.Window{{ID: 43, IsActive: true}}, // no managed pane owns the tab
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "" || !st.StateSet || !st.Dirty {
		t.Fatalf("source state = %+v; want an explicit empty state queued for rendering", st)
	}
	if h.renderQueue.Empty() {
		t.Fatal("ownerless visible-icon clear was not queued immediately")
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || detail.Events[0].Action != "user_clear" ||
		detail.Events[0].Suppressed || detail.Events[0].RouteDecision != events.RouteDecisionSource {
		t.Fatalf("events = %+v; want one allowed source-routed user_clear", detail.Events)
	}

	h.renderSnapshot(fake.windows)
	calls := fake.titleCalls()
	if len(calls) != 1 || calls[0].match != kitty.MatchTabByWindowID(43) || calls[0].title != wantTitle {
		t.Fatalf("SetTabTitle calls = %+v; want ownerless tab converged to %q", calls, wantTitle)
	}
}

func TestHandler_UserClearOwnerlessRecordedStateWithoutLiveIcon(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
		"api",
		[]kitty.Window{{ID: 43, IsActive: true}},
	)}
	h.SetKittyClient(fake)
	seedOwnerState(h, 43, "stop", "stop")

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "" || !st.StateSet || !st.Dirty {
		t.Fatalf("source state = %+v; want recorded ownerless state cleared", st)
	}
	if g := h.guard.Current(43); g != "" {
		t.Fatalf("source guard = %q; want cleared", g)
	}
}

// TestHandler_UserClearKittyLookupFailureWipesSourceFallback verifies that when
// the Kitty snapshot cannot be read, F8 is not silently dropped. Because title
// ownership is unknown, the wipe falls back to the source pane that fired the
// clear rather than vanishing.
func TestHandler_UserClearKittyLookupFailureWipesSourceFallback(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows(palette.BuildTitle("api", "stop", false), []kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}}),
		listErr: errors.New("kitty unavailable"),
	}
	h.SetKittyClient(fake)
	seedOwnerState(h, 42, "stop", "stop")

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "" || !st.StateSet {
		t.Fatalf("state = %+v; a lookup failure must still wipe the source pane icon", st)
	}
	if g := h.guard.Current(42); g != "" {
		t.Fatalf("guard = %q; want cleared on the fallback wipe", g)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Action != "user_clear" || detail.Events[0].Suppressed {
		t.Fatalf("events = %+v; want one allowed user_clear on the fallback source pane", detail.Events)
	}
}

func TestHandler_UserClearPreservesForeignTitleParts(t *testing.T) {
	h := newTestHandler()
	liveTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:renamed",
		State:   "stop",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:renamed",
		State:   "",
		Starred: true,
		Muted:   true,
	})
	fake := &fakeKittyClient{windows: hookEventWindows(liveTitle, []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
	}})}
	h.SetKittyClient(fake)

	seedOwnerState(h, 42, "stop", "stop")
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.Looping = true
	st.LoopingSet = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.renderSnapshot(fake.windows)
	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(calls), calls)
	}
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestHandler_UserClearClearsGuardActionAndStaleLoop(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "error", Looping: true}),
		[]kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}},
	)}
	h.SetKittyClient(fake)

	seedOwnerState(h, 42, "stop_failure", "error")
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.LastActiveEvent = "PreToolUse"
	st.Looping = true
	st.LoopingSet = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.stateMu.Lock()
	st = h.store[42]
	h.stateMu.Unlock()
	if g := h.guard.Current(42); g != "" {
		t.Fatalf("guard = %q; want cleared", g)
	}
	if st.LastAction != "" || st.LastActiveEvent != "" || st.DesiredIcon != "" {
		t.Fatalf("state = %+v; want last action/event and desired icon cleared", st)
	}
	if st.Looping || !st.LoopingSet {
		t.Fatalf("state = %+v; stale loop state must be explicitly cleared", st)
	}
}

func TestHandler_UserClearConvergesDriftedTitleAndResetsFailures(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitle("api", "stop", false), // live title drifted to stop
		[]kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}},
	)}
	h.SetKittyClient(fake)

	seedOwnerState(h, 42, "stop", "stop")
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.RenderFailures = 2
	st.LastRenderError = "boom"
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{
		Type:          protocol.TypeHookEvent,
		WindowID:      42,
		Event:         "user_clear",
		KittyListenOn: "unix:/tmp/kitty-1",
	})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	h.renderSnapshot(fake.windows)
	calls := fake.titleCalls()
	want := palette.BuildTitle("api", "", false)
	if len(calls) != 1 || calls[0].title != want {
		t.Fatalf("SetTabTitle calls = %+v; want one converging to %q", calls, want)
	}

	h.stateMu.Lock()
	st = h.store[42]
	h.stateMu.Unlock()
	if st.Dirty || st.RenderFailures != 0 || st.LastRenderError != "" {
		t.Fatalf("state = %+v; successful render must reset failure counters and dirty flag", st)
	}
}

func TestHandler_UserClearThenPromptSetsBolt(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitle("api", "stop", false),
		[]kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}},
	)}
	h.SetKittyClient(fake)
	seedOwnerState(h, 42, "stop", "stop")

	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
		t.Fatalf("UserPromptSubmit response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "bolt" {
		t.Fatalf("state = %+v; want bolt after post-clear prompt", st)
	}
}

func TestHandler_UserClearLoopOnlyTab(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, recorded := range []bool{false, true} {
			t.Run(fmt.Sprintf("managed=%t/recorded=%t", managed, recorded), func(t *testing.T) {
				h := newTestHandler()
				win := kitty.Window{ID: 42, IsActive: true}
				if managed {
					win.UserVars = kitty.UserVars{Profile: "work", Harness: harness.Codex}
				}
				liveTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", Starred: true, Muted: true, Looping: true})
				fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(liveTitle, []kitty.Window{win})}
				h.SetKittyClient(fake)
				if recorded {
					h.stateMu.Lock()
					st := h.ensureWindowStateLocked(42)
					st.Looping = true
					st.LoopingSet = true
					h.stateMu.Unlock()
				}
				if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
					t.Fatalf("user_clear response = %+v", resp)
				}
				if h.renderQueue.Empty() {
					t.Fatal("loop-only clear must render immediately")
				}
				h.renderSnapshot(fake.windows)
				want := palette.BuildTitleParts(palette.TitleParts{Base: "api", Starred: true, Muted: true})
				calls := fake.titleCalls()
				if len(calls) != 1 || calls[0].title != want {
					t.Fatalf("title calls = %+v; want %q", calls, want)
				}
				// A later reducer publication must not restore the dismissed marker.
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active"})
				if got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[42]; got.Looping {
					t.Fatalf("later activity restored stale loop: %+v", got)
				}
			})
		}
	}
}

func TestHandler_UserClearKeepsLiveLoopLease(t *testing.T) {
	for _, phase := range []string{state.ASLPhaseTurn, state.ASLPhaseBetween, state.ASLPhaseWaiting} {
		t.Run(phase, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{windows: hookEventWindows(
				palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "key", Looping: true}),
				[]kitty.Window{{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}}},
			)}
			h.SetKittyClient(fake)
			h.stateMu.Lock()
			st := h.ensureWindowStateLocked(42)
			st.Reducer = state.NewReducer(h.reducerCfg)
			st.Reducer.Apply(protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "live-run"},
				Phase:  phase, Action: "permission", Seq: 1, At: time.Now().Unix(),
			})
			h.publishEffectiveLocked(42, st, st.Reducer.Reduce(time.Now().Unix()))
			h.stateMu.Unlock()
			if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
				t.Fatalf("user_clear response = %+v", resp)
			}
			got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[42]
			if !got.Looping || got.IconState != "" {
				t.Fatalf("state = %+v; want live loop without state icon", got)
			}
			h.renderSnapshot(fake.windows)
			calls := fake.titleCalls()
			want := palette.BuildTitleParts(palette.TitleParts{Base: "api", Looping: true})
			if len(calls) != 1 || calls[0].title != want {
				t.Fatalf("title calls = %+v; want %q", calls, want)
			}
		})
	}
}
