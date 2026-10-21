package daemon

// The busy-stretch anchor. It measures one uninterrupted stretch of work, so it
// is maintained as an invariant of publishEffectiveLocked rather than as
// transition detection: entering the active class from outside it stamps the
// anchor, staying inside it — however much the icon churns between bolt,
// subagent, and compact — leaves the stamp alone, and any icon outside the
// class clears it. Paths that never publish an effective state, including
// icon-preserving skipReduce updates, must not disturb a live stretch.

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// busySince reads the window's busy-stretch anchor.
func busySince(t *testing.T, h *Handler, windowID int) time.Time {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.BusySince
}

// publishIcon drives one reduced publish through publishEffectiveLocked, the
// single funnel the busy-stretch anchor is maintained from.
func publishIcon(h *Handler, windowID int, icon string) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.publishEffectiveLocked(windowID, h.ensureWindowStateLocked(windowID), state.EffectiveState{Icon: icon})
}

func TestBusySinceStampedOnActiveClassEntry(t *testing.T) {
	cases := []struct{ name, from, to string }{
		{"stop to bolt", "stop", "bolt"},
		{"empty to bolt", "", "bolt"},
		{"stop to subagent", "stop", "subagent"},
		{"empty to compact", "", "compact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTestHandler()
				publishIcon(h, 7, tc.from)
				if got := busySince(t, h, 7); !got.IsZero() {
					t.Fatalf("anchor before entry = %v; want zero", got)
				}

				time.Sleep(time.Minute)
				entered := time.Now()
				publishIcon(h, 7, tc.to)

				if got := busySince(t, h, 7); !got.Equal(entered) {
					t.Fatalf("anchor = %v; want the entry instant %v", got, entered)
				}
			})
		})
	}
}

// TestBusySinceSurvivesActiveClassChurn pins why the anchor is an idempotent
// invariant rather than transition detection: a bolt handing off to a subagent
// and back is one busy stretch, not three.
func TestBusySinceSurvivesActiveClassChurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		publishIcon(h, 7, "bolt")
		anchor := busySince(t, h, 7)
		if anchor.IsZero() {
			t.Fatal("bolt did not stamp the anchor")
		}

		for _, icon := range []string{"subagent", "bolt", "compact", "bolt"} {
			time.Sleep(30 * time.Second)
			publishIcon(h, 7, icon)
			if got := busySince(t, h, 7); !got.Equal(anchor) {
				t.Fatalf("anchor after %q = %v; want the stretch's original %v", icon, got, anchor)
			}
		}
	})
}

func TestBusySinceClearedByEveryNonActiveIcon(t *testing.T) {
	t.Parallel()

	for _, icon := range []string{"stop", "key", "question", "warning", "error", ""} {
		name := icon
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			h := newTestHandler()
			publishIcon(h, 7, "bolt")
			if busySince(t, h, 7).IsZero() {
				t.Fatal("bolt did not stamp the anchor")
			}

			publishIcon(h, 7, icon)

			if got := busySince(t, h, 7); !got.IsZero() {
				t.Fatalf("anchor after %q = %v; want zero", icon, got)
			}
		})
	}
}

// TestBusySinceNotRestampedByIdenticalRepublish covers publishEffectiveLocked's
// early-exit path: a republish of an unchanged effective state skips the
// generation bump but still reaches the anchor invariant, which must leave a
// live stretch alone instead of restarting it.
func TestBusySinceNotRestampedByIdenticalRepublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		publishIcon(h, 7, "bolt")
		anchor := busySince(t, h, 7)
		h.stateMu.Lock()
		genBefore := h.store[7].EffectiveGen
		h.stateMu.Unlock()

		time.Sleep(5 * time.Minute)
		publishIcon(h, 7, "bolt")

		h.stateMu.Lock()
		genAfter := h.store[7].EffectiveGen
		h.stateMu.Unlock()
		if genAfter != genBefore {
			t.Fatalf("EffectiveGen = %d; want %d so the republish exercises the early exit", genAfter, genBefore)
		}
		if got := busySince(t, h, 7); !got.Equal(anchor) {
			t.Fatalf("anchor = %v; want the unchanged %v", got, anchor)
		}
	})
}

// TestBusySinceUntouchedBySkipReduceUpdate pins the assumption that lets the
// legacy scalar DesiredIcon writer keep the anchor out of handleStateUpdate:
// a skipReduce request is icon-preserving, never reaches
// publishEffectiveLocked, and must leave a live stretch running.
func TestBusySinceUntouchedBySkipReduceUpdate(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	if resp := h.handleStateUpdate(protocol.Request{
		Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "UserPromptSubmit", IsPrompt: true,
	}); !resp.OK || !resp.Allowed {
		t.Fatalf("active update rejected: %+v", resp)
	}
	anchor := busySince(t, h, 7)
	if anchor.IsZero() {
		t.Fatal("active update did not stamp the anchor")
	}

	h.stateMu.Lock()
	shadowSeqBefore := h.store[7].ShadowSeq
	factsBefore := h.store[7].Reducer.Facts()
	h.stateMu.Unlock()

	if resp := h.handleStateUpdate(protocol.Request{
		Type: protocol.TypeStateUpdate, WindowID: 7, Action: "active", Event: "PostToolUse", PreservesIcon: true,
	}); !resp.OK {
		t.Fatalf("child-complete update = %+v; want OK", resp)
	}

	h.stateMu.Lock()
	shadowSeqAfter := h.store[7].ShadowSeq
	factsAfter := h.store[7].Reducer.Facts()
	h.stateMu.Unlock()
	if shadowSeqAfter != shadowSeqBefore || !reflect.DeepEqual(factsAfter, factsBefore) {
		t.Fatalf("child-complete update reached the reducer: shadow_seq %d/%d facts %+v/%+v",
			shadowSeqAfter, shadowSeqBefore, factsAfter, factsBefore)
	}
	if got := busySince(t, h, 7); !got.Equal(anchor) {
		t.Fatalf("anchor = %v; want the untouched %v", got, anchor)
	}
}
