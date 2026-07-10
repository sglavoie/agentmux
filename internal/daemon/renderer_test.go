package daemon

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestRendererConvergesDriftAndPreservesForeignTitleParts(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)

	liveTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:renamed",
		State:   "stop",
		Starred: true,
		Muted:   true,
	})
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:renamed",
		State:   "bolt",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	fake.windows = fakeOSWindowsWithTab(42, liveTitle)

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.Looping = true
	st.LoopingSet = true
	h.stateMu.Unlock()

	h.renderSnapshot(fake.windows)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestRendererUsesLatestLiveTitleSnapshotAndPreservesStarAndMuteMarkers(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)

	// An earlier renderer snapshot did not contain the externally owned
	// markers. Before this renderer pass, star and mute writers updated the
	// live title; the current snapshot must be the source for the next title.
	earlierTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:  "W:api",
		State: "stop",
	})
	latestLiveTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:api",
		State:   "stop",
		Starred: true,
		Muted:   true,
	})
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "W:api",
		State:   "bolt",
		Starred: true,
		Muted:   true,
	})
	fake.windows = fakeOSWindowsWithTab(42, earlierTitle)

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).DesiredIcon = "bolt"
	h.stateMu.Unlock()

	// Model external star/mute writes that happened after the older snapshot.
	fake.windows = fakeOSWindowsWithTab(42, latestLiveTitle)
	h.renderSnapshot(fake.windows)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestRendererUsesActiveManagedTitleOwner(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithSplitTab("W:api", []kitty.Window{
		{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
		},
		{
			ID:       43,
			IsActive: false,
			UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
		},
	})

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).DesiredIcon = "bolt"
	h.ensureWindowStateLocked(43).DesiredIcon = "error"
	h.stateMu.Unlock()

	h.renderSnapshot(fake.windows)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	want := palette.BuildTitle("W:api", "bolt", false)
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestRendererRetriesFailedWrites(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{setTitleErr: errors.New("kitty unavailable")}
	h.SetKittyClient(fake)
	fake.windows = fakeOSWindowsWithTab(42, "W:api")

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.DesiredIcon = "bolt"
	st.Dirty = true
	st.Seq = 1
	h.stateMu.Unlock()

	h.renderSnapshot(fake.windows)

	h.stateMu.Lock()
	dirtyAfterFailure := h.store[42].Dirty
	failures := h.store[42].RenderFailures
	h.stateMu.Unlock()
	if !dirtyAfterFailure || failures != 1 {
		t.Fatalf("after failure dirty=%v failures=%d, want dirty=true failures=1", dirtyAfterFailure, failures)
	}

	fake.mu.Lock()
	fake.setTitleErr = nil
	fake.mu.Unlock()
	h.renderSnapshot(fake.windows)

	h.stateMu.Lock()
	dirtyAfterRetry := h.store[42].Dirty
	failuresAfterRetry := h.store[42].RenderFailures
	h.stateMu.Unlock()
	if dirtyAfterRetry || failuresAfterRetry != 0 {
		t.Fatalf("after retry dirty=%v failures=%d, want dirty=false failures=0", dirtyAfterRetry, failuresAfterRetry)
	}
	if got := len(fake.userVarsCalls()); got != 1 {
		t.Fatalf("SetUserVars calls after retry = %d, want 1", got)
	}
}

func TestRendererStaleSeqDoesNotClearDirty(t *testing.T) {
	h := newTestHandler()
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.Dirty = true
	st.Seq = 2
	h.stateMu.Unlock()

	h.finishRenderCandidate(renderCandidate{windowID: 42, seq: 1, dirty: true}, nil)

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.store[42].Dirty {
		t.Fatalf("stale render candidate cleared Dirty")
	}
}
