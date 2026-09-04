package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func presentationChangedAt(t *testing.T, h *Handler, windowID int) time.Time {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.PresentationChangedAt
}

func TestPresentationChangedAtTracksOnlyVisiblePublicationTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		h.publishSnapshot(unseenTree(7, false), time.Now())

		first := time.Now()
		publishIcon(h, 7, "bolt")
		if got := presentationChangedAt(t, h, 7); !got.Equal(first) {
			t.Fatalf("initial icon transition stamped %v; want %v", got, first)
		}

		// Source and looping changes that preserve the published icon are reducer
		// churn, not presentation changes.
		time.Sleep(time.Minute)
		h.stateMu.Lock()
		st := h.store[7]
		h.publishEffectiveLocked(7, st, state.EffectiveState{
			Icon: "bolt", Looping: true,
			Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"},
		})
		h.stateMu.Unlock()
		if got := presentationChangedAt(t, h, 7); !got.Equal(first) {
			t.Fatalf("same-icon reducer churn restamped %v; want %v", got, first)
		}

		// Entering rest while unfocused changes both the icon and unseen bit, but
		// the publication owns one transition instant.
		time.Sleep(time.Minute)
		finished := time.Now()
		publishIcon(h, 7, "stop")
		if !unseen(t, h, 7) {
			t.Fatal("unfocused stop did not raise unseen")
		}
		if got := presentationChangedAt(t, h, 7); !got.Equal(finished) {
			t.Fatalf("combined icon/unseen transition stamped %v; want %v", got, finished)
		}

		time.Sleep(time.Minute)
		publishIcon(h, 7, "stop")
		if got := presentationChangedAt(t, h, 7); !got.Equal(finished) {
			t.Fatalf("same-state republish restamped %v; want %v", got, finished)
		}
	})
}

func TestPresentationChangedAtStampsUnseenOnlyPublicationChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		publishIcon(h, 7, "bolt")
		seedUnseen(h, 7, true, false)
		time.Sleep(time.Minute)
		clearedAt := time.Now()

		publishIcon(h, 7, "bolt")

		if unseen(t, h, 7) {
			t.Fatal("non-resting republish did not clear stale unseen bit")
		}
		if got := presentationChangedAt(t, h, 7); !got.Equal(clearedAt) {
			t.Fatalf("unseen-only transition stamped %v; want %v", got, clearedAt)
		}
	})
}

func TestFocusClearUsesSnapshotObservationTimeAndDoesNotRestamp(t *testing.T) {
	h := newTestHandler()
	publishIcon(h, 7, "bolt")
	h.publishSnapshot(unseenTree(7, false), time.Now())
	publishIcon(h, 7, "stop")

	observedAt := time.Unix(1_710_000_200, 987_000_000)
	h.publishSnapshot(unseenTree(7, true), observedAt)
	if got := presentationChangedAt(t, h, 7); !got.Equal(observedAt) {
		t.Fatalf("focus clear stamped %v; want snapshot observation %v", got, observedAt)
	}

	h.publishSnapshot(unseenTree(7, true), observedAt.Add(time.Minute))
	if got := presentationChangedAt(t, h, 7); !got.Equal(observedAt) {
		t.Fatalf("repeated focused snapshot restamped %v; want %v", got, observedAt)
	}
}

func TestLegacySameStateRestoreDoesNotFabricatePresentationRecency(t *testing.T) {
	var restored state.WindowState
	if err := restored.UnmarshalUserVar(`{"v":4,"a":"stop","f":[{"k":"harness","y":"claude:s1","a":"stop","i":"stop","q":1,"t":1710000100,"tm":true,"ta":1710000100}],"ei":"stop","sq":1}`); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if !restored.PresentationChangedAt.IsZero() {
		t.Fatalf("legacy restore fabricated %v before publication", restored.PresentationChangedAt)
	}

	h := newTestHandler()
	h.stateMu.Lock()
	h.store[7] = &restored
	h.publishEffectiveLocked(7, &restored, restored.Effective)
	h.stateMu.Unlock()
	if got := presentationChangedAt(t, h, 7); !got.IsZero() {
		t.Fatalf("same-state restore publication fabricated recency %v", got)
	}
}
