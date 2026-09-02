package daemon

// What a sweep does to the busy-stretch anchor. The anchor measures one
// uninterrupted stretch of work, so the rule is about the class the window ends
// up in, not about the sweep itself: a window that re-reduces back into the
// active class never stopped working and must keep counting, while one that
// lands outside it — or has no reducer at all — must have its anchor cleared so
// the timer does not keep running behind a cleared icon.

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestSweepStale_ClearsBusySinceWithNilReducer pins the busy-stretch anchor to
// the sweep's own scalar reset. A window that never built a reducer never
// reaches publishEffectiveLocked, so without an explicit clear here the swept
// window would keep a live anchor and go on counting after its icon is gone.
func TestSweepStale_ClearsBusySinceWithNilReducer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.Activity = now.Add(-15 * time.Minute)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.BusySince = now.Add(-15 * time.Minute)
		h.windowMetadata[42] = protocol.WindowDebugMetadata{}
		h.stateMu.Unlock()

		before := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
		if got := before.WindowMetadata[42].Shadow.BusySinceAt; got != st.BusySince.Unix() {
			t.Fatalf("busy_since_at before sweep = %d; want %d", got, st.BusySince.Unix())
		}

		h.SweepStale(now)
		synctest.Wait()

		h.stateMu.Lock()
		hasReducer := h.store[42].Reducer != nil
		anchor := h.store[42].BusySince
		h.stateMu.Unlock()
		if hasReducer {
			t.Fatal("window gained a reducer; the nil-reducer path was not exercised")
		}
		if !anchor.IsZero() {
			t.Fatalf("BusySince[42] = %v; want zero after the sweep", anchor)
		}

		after := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
		encoded, err := json.Marshal(after.WindowMetadata[42].Shadow)
		if err != nil {
			t.Fatalf("Marshal(swept shadow): %v", err)
		}
		if strings.Contains(string(encoded), `"busy_since_at"`) {
			t.Fatalf("stale-swept shadow emitted busy_since_at: %s", encoded)
		}
	})
}

// TestSweepStale_PreservesBusySinceWhenReductionStaysActive pins the anchor
// across a whole-window stale decision that re-reduces back into the active
// class. The bolt fact expires on its own 10-minute threshold while the
// subagent fact survives its 30-minute one, so the visible icon goes bolt →
// subagent without ever leaving the class: the busy stretch never ended and
// the timer must not restart.
func TestSweepStale_PreservesBusySinceWhenReductionStaysActive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, _ := newSweepTestHandlerWithMap(map[string]time.Duration{
			"active":   10 * time.Minute,
			"subagent": 30 * time.Minute,
		}, nil)
		now := time.Now()

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.Activity = now.Add(-15 * time.Minute)
		h.stateMu.Unlock()
		seedReducerFact(h, 42, now,
			protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
				Action: "active", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
			protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"},
				Action: "subagent", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
		)

		anchor := now.Add(-20 * time.Minute)
		h.stateMu.Lock()
		h.store[42].BusySince = anchor
		h.stateMu.Unlock()

		h.SweepStaleWithSnapshot(now, nil)
		synctest.Wait()

		if got := effectiveState(h, 42).Icon; got != "subagent" {
			t.Fatalf("icon after sweep = %q; want subagent (the surviving fact keeps the tab)", got)
		}
		h.stateMu.Lock()
		lastAction := h.store[42].LastAction
		busy := h.store[42].BusySince
		h.stateMu.Unlock()
		if lastAction != "" {
			t.Fatalf("lastAction = %q; want cleared by the whole-window sweep", lastAction)
		}
		if !busy.Equal(anchor) {
			t.Fatalf("BusySince = %v; want the original anchor %v", busy, anchor)
		}
	})
}

// TestSweepStale_ClearsBusySinceWhenReductionLeavesActiveClass pins the other
// half of the rule: the surviving fact here is a permission, so the swept
// window reduces to an attention icon. The busy stretch really did end and the
// anchor must go with it.
func TestSweepStale_ClearsBusySinceWhenReductionLeavesActiveClass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, _ := newSweepTestHandlerWithMap(map[string]time.Duration{
			"active": 10 * time.Minute,
		}, nil)
		now := time.Now()

		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.LastSessionTitle = "W:api"
		st.Activity = now.Add(-15 * time.Minute)
		h.stateMu.Unlock()
		seedReducerFact(h, 42, now,
			protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
				Action: "active", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
			protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "permission"},
				Action: "permission", Seq: 1, At: now.Add(-15 * time.Minute).Unix(),
			},
		)

		h.stateMu.Lock()
		h.store[42].BusySince = now.Add(-20 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStaleWithSnapshot(now, nil)
		synctest.Wait()

		if got := effectiveState(h, 42).Icon; got != "key" {
			t.Fatalf("icon after sweep = %q; want key (the attention fact keeps the tab)", got)
		}
		h.stateMu.Lock()
		busy := h.store[42].BusySince
		h.stateMu.Unlock()
		if !busy.IsZero() {
			t.Fatalf("BusySince = %v; want zero once the icon left the active class", busy)
		}
	})
}
