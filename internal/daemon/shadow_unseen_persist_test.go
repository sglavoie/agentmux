package daemon

// The unseen bit's DURABILITY, end to end: a real stop event, a real focus
// observation, the renderer's own SetUserVars write, a restart that restores
// from the tree that write left behind, and the dashboard group the restored row
// lands in.
//
// The sibling tests in shadow_unseen_test.go pin the two writers against
// in-memory state, which is the right shape for a transition table but cannot
// see the failure this file exists for: a clear that never leaves the process.
// Unseen is carried by amx_state alone — no tab title mentions it — so a clear
// that skipped the dirty mark is invisible to the title-drift leg of
// enqueueSnapshotCandidates and simply evaporates at the next restart, restoring
// the row the user just read straight back into Needs Attention.
//
// The imports point from the daemon's tests at the TUI leaves rather than the
// other way round, which is deliberate and test-only: the wire projection
// (handleStateQuery), the overlay (commands.ApplyDaemonStates), and the bucket
// (dashboard.StateGroup) are the three real consumers of the bit, and a test
// that re-implemented any of them would be asserting its own copy instead of the
// path a user's dashboard actually runs. Production code in internal/daemon must
// keep importing neither.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// The two dashboard.StateGroup buckets this file distinguishes.
const (
	groupNeedsAttention = 0
	groupIdle           = 2
)

// focusableTree is fakeOSWindowsWithTab plus the three flags the focus
// observation reads. The tab and the pane are always active, so flipping
// `focused` models the one thing a user actually changes by switching OS
// windows; the profile/harness user vars are what makes the window eligible for
// restore at all.
func focusableTree(windowID int, tabTitle string, focused bool) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: focused,
		Tabs: []kitty.Tab{{
			ID:       100,
			IsActive: true,
			Title:    tabTitle,
			Windows: []kitty.Window{{
				ID:       windowID,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
			}},
		}},
	}}
}

// fakeTree reads the fake's canonical tree the way the daemon does — through
// ListWindows, which deep-copies — so every snapshot the test publishes carries
// whatever amx_state and tab title the renderer has written by that point.
func fakeTree(t *testing.T, f *fakeKittyClient) []kitty.OSWindow {
	t.Helper()
	windows, err := f.ListWindows()
	if err != nil {
		t.Fatalf("fake ListWindows: %v", err)
	}
	return windows
}

// focusFakeTree moves OS-window focus in place, which is what keeps the
// persisted amx_state on the tree instead of rebuilding it away.
func focusFakeTree(f *fakeKittyClient, focused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.windows {
		f.windows[i].IsFocused = focused
	}
}

// fakeAMXState returns the amx_state payload the renderer has written onto the
// window, which is exactly what a restarted daemon would read back.
func fakeAMXState(t *testing.T, f *fakeKittyClient, windowID int) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.windows {
		for j := range f.windows[i].Tabs {
			for k := range f.windows[i].Tabs[j].Windows {
				if win := f.windows[i].Tabs[j].Windows[k]; win.ID == windowID {
					if win.UserVars.AMXState == "" {
						t.Fatalf("window %d carries no persisted amx_state", windowID)
					}
					return win.UserVars.AMXState
				}
			}
		}
	}
	t.Fatalf("window %d is not in the fake tree", windowID)
	return ""
}

// windowRenderGen reads the renderer generation, the dirty flag, and the
// generation persistence has reached as one value, so a caller cannot pair a
// sequence from one moment with a dirty flag from another.
func windowRenderGen(t *testing.T, h *Handler, windowID int) (seq uint64, dirty bool, persisted uint64) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.Seq, st.Dirty, st.PersistedGen
}

// daemonRowGroup runs the bit through its three real consumers: the query
// projection, the dashboard's daemon-state overlay, and the grouping rule.
func daemonRowGroup(t *testing.T, h *Handler, windowID int) int {
	t.Helper()
	resp := h.handleStateQuery()
	if !resp.OK {
		t.Fatalf("state query failed: %v", resp.Error)
	}
	rows := []session.RunningSession{{WindowID: windowID}}
	commands.ApplyDaemonStates(rows, resp.States)
	if rows[0].State != "stop" {
		t.Fatalf("row state after restore = %q; want stop", rows[0].State)
	}
	return dashboard.StateGroup(rows[0], nil, time.Minute)
}

// TestFocusClearPersistsUnseenAcrossRestart is the durability half of the unseen
// bit, driven only through paths production uses: a hook stop that lands while
// the pane is unfocused, a maintenance publish that observes focus, the render
// worker's own amx_state write, a fresh Handler restoring from the tree that
// write produced, and the dashboard group the restored row lands in.
//
// The control at the end is what makes the round trip mean anything: the payload
// persisted BEFORE the focus observation is restored into its own Handler and
// must land in Needs Attention. Without it, a test that never persisted the
// clear at all would still pass on a payload that happened to restore as seen.
func TestFocusClearPersistsUnseenAcrossRestart(t *testing.T) {
	const windowID = 42

	h := newTestHandler()
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	fake.windows = focusableTree(windowID, "W:api", false)

	// 1. The turn finishes while the user is looking elsewhere. The snapshot is
	// published first so the publish path has a real unfocused observation to
	// read rather than the cold-cache default.
	h.publishSnapshot(fakeTree(t, fake), time.Now())
	resp := h.handleStateUpdate(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: windowID,
		Action:   "stop",
		Event:    "Stop",
	})
	if !resp.OK {
		t.Fatalf("stop event rejected: %v", resp.Error)
	}
	if !unseen(t, h, windowID) {
		t.Fatal("a stop that landed while the pane was unfocused did not raise the unseen bit")
	}

	// 2. Let it converge: the unseen row is persisted and the window is clean.
	h.renderSnapshot(fakeTree(t, fake))
	unseenPayload := fakeAMXState(t, fake, windowID)
	seq, dirty, persisted := windowRenderGen(t, h, windowID)
	if dirty || persisted != seq {
		t.Fatalf("unseen stop did not converge: dirty=%v persisted=%d seq=%d", dirty, persisted, seq)
	}

	// 3. Converged means converged: further unfocused snapshots and flushes
	// schedule no write at all. This is the state the defect hid in — with
	// nothing pending, nothing was going to carry a later clear to Kitty.
	writesBeforeFocus := len(fake.userVarsCalls())
	h.publishSnapshot(fakeTree(t, fake), time.Now())
	h.renderSnapshot(fakeTree(t, fake))
	if got := len(fake.userVarsCalls()); got != writesBeforeFocus {
		t.Fatalf("a converged window wrote amx_state %d extra time(s) with nothing to say", got-writesBeforeFocus)
	}

	// 4. The user looks at the pane. One snapshot, no key and no command.
	focusFakeTree(fake, true)
	h.publishSnapshot(fakeTree(t, fake), time.Now())

	if unseen(t, h, windowID) {
		t.Fatal("a snapshot observing focus did not clear the unseen bit")
	}
	focusSeq, focusDirty, _ := windowRenderGen(t, h, windowID)
	if focusSeq != seq+1 {
		t.Fatalf("focus clear advanced the renderer generation from %d to %d; want exactly one advance", seq, focusSeq)
	}
	if !focusDirty {
		t.Fatal("focus clear left durable state clean, so no amx_state write is scheduled and the bit dies with the process")
	}
	if h.renderQueue.Empty() {
		t.Fatal("focus clear queued no render")
	}

	// 5. Exactly once. The pane keeps focus for as long as the user reads it, so
	// every following snapshot re-observes focus; none of them may re-dirty a
	// window whose bit is already clear.
	for i := 0; i < 3; i++ {
		h.publishSnapshot(fakeTree(t, fake), time.Now())
	}
	if repeatSeq, _, _ := windowRenderGen(t, h, windowID); repeatSeq != focusSeq {
		t.Fatalf("repeated focused snapshots advanced the generation to %d; want it parked at %d", repeatSeq, focusSeq)
	}

	// 6. The renderer performs the write.
	h.renderSnapshot(fakeTree(t, fake))
	if got := len(fake.userVarsCalls()); got != writesBeforeFocus+1 {
		t.Fatalf("amx_state writes after the focus clear = %d; want exactly one more than the %d before it", got, writesBeforeFocus)
	}
	if seqAfter, dirtyAfter, persistedAfter := windowRenderGen(t, h, windowID); dirtyAfter || persistedAfter != seqAfter {
		t.Fatalf("focus clear did not converge: dirty=%v persisted=%d seq=%d", dirtyAfter, persistedAfter, seqAfter)
	}

	// 7. Restart. A fresh Handler restores from the tree the write left behind.
	restarted := newTestHandler()
	restarted.SetKittyClient(fake)
	restarted.restoreFromUserVars(fakeTree(t, fake), time.Now())
	if unseen(t, restarted, windowID) {
		t.Fatal("the restarted daemon restored the row as unseen: the focus clear never reached amx_state")
	}
	if group := daemonRowGroup(t, restarted, windowID); group != groupIdle {
		t.Fatalf("restored row grouped into %d; want Idle (%d)", group, groupIdle)
	}

	// 8. The control. Restoring the pre-focus payload must still produce a Needs
	// Attention row, or step 7 proved only that some payload restores as seen.
	preFocus := focusableTree(windowID, "W:api", false)
	preFocus[0].Tabs[0].Windows[0].UserVars.AMXState = unseenPayload
	control := newTestHandler()
	control.SetKittyClient(&fakeKittyClient{windows: preFocus})
	control.restoreFromUserVars(preFocus, time.Now())
	if !unseen(t, control, windowID) {
		t.Fatal("the payload persisted before the focus observation did not restore as unseen; the round trip above proves nothing")
	}
	if group := daemonRowGroup(t, control, windowID); group != groupNeedsAttention {
		t.Fatalf("pre-focus payload grouped into %d; want Needs Attention (%d)", group, groupNeedsAttention)
	}
}
