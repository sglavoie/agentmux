package daemon

// The wire projection of the cached Kitty snapshot: what state_query and health
// say about a fresh, stale, and cold cache, and what they must not say. The
// cache's own freshness arithmetic is covered in snapshot_freshness_test.go and
// the per-socket coordination in snapshot_coordination_test.go; here the
// question is only what a client ends up holding.

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestStateQuerySnapshotBlockReportsFreshStaleAndCold walks the three
// conditions a client has to tell apart, through the one field set that
// distinguishes them. Cold is the interesting one: it is a real block, not an
// omitted one, because omission is reserved for a daemon too old to have the
// field and would send a new client off to do its own Kitty I/O.
func TestStateQuerySnapshotBlockReportsFreshStaleAndCold(t *testing.T) {
	t.Parallel()

	t.Run("cold", func(t *testing.T) {
		h := newTestHandler()
		block := stateQuerySnapshot(t, h)
		if block.Ready {
			t.Fatalf("block = %+v; want a cold cache before the first publication", block)
		}
		if block.Stale {
			t.Fatalf("block = %+v; want a cold cache reported as cold rather than stale", block)
		}
		if block.Generation != 0 || block.CapturedAt != 0 || block.AgeMS != 0 {
			t.Fatalf("block = %+v; want every freshness field zero for a cold cache", block)
		}
		if block.Windows != nil {
			t.Fatalf("block windows = %+v; want none from a cache that never listed", block.Windows)
		}
	})

	t.Run("fresh", func(t *testing.T) {
		h := newTestHandler()
		capturedAt := time.Now().Add(-time.Second)
		h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), capturedAt)
		block := stateQuerySnapshot(t, h)
		if !block.Ready || block.Stale {
			t.Fatalf("block = %+v; want a ready, non-stale snapshot one second old", block)
		}
		if block.Generation != 1 {
			t.Fatalf("generation = %d; want the first publication", block.Generation)
		}
		if block.CapturedAt != capturedAt.Unix() {
			t.Fatalf("captured_at = %d; want the list completion second %d", block.CapturedAt, capturedAt.Unix())
		}
		if block.AgeMS < 900 || block.AgeMS > 5_000 {
			t.Fatalf("age_ms = %d; want roughly one second", block.AgeMS)
		}
		if len(block.Windows) != 1 || block.Windows[0].Tabs[0].Windows[0].ID != 42 {
			t.Fatalf("block windows = %+v; want the published topology", block.Windows)
		}
	})

	t.Run("stale", func(t *testing.T) {
		h := newTestHandler()
		capturedAt := time.Now().Add(-protocol.KittySnapshotStaleAfter - time.Second)
		h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), capturedAt)
		block := stateQuerySnapshot(t, h)
		if !block.Ready {
			t.Fatalf("block = %+v; want an aged snapshot to stay ready", block)
		}
		if !block.Stale {
			t.Fatalf("block = %+v; want staleness past the %v threshold", block, protocol.KittySnapshotStaleAfter)
		}
		// A stale snapshot still ships its topology: the contract lets the
		// dashboard render it, and only forbids topology-derived Kitty writes.
		if len(block.Windows) != 1 {
			t.Fatalf("block windows = %+v; want a stale snapshot to still carry its tree", block.Windows)
		}
	})
}

// TestSnapshotBlockClampsNegativeAge pins the clock-anomaly rule at the wire
// boundary rather than only inside the cache: a snapshot captured "in the
// future" reads as brand new, never as negatively aged and never as stale.
func TestSnapshotBlockClampsNegativeAge(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	capturedAt := time.Now().Add(time.Hour)
	h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), capturedAt)

	block := stateQuerySnapshot(t, h)
	if block.AgeMS != 0 {
		t.Fatalf("age_ms = %d; want a backwards clock clamped to zero", block.AgeMS)
	}
	if block.Stale {
		t.Fatalf("block = %+v; want a future capture time reported as fresh", block)
	}
	if block.CapturedAt != capturedAt.Unix() {
		t.Fatalf("captured_at = %d; want the capture instant reported unchanged at %d",
			block.CapturedAt, capturedAt.Unix())
	}
}

// TestHealthCarriesStatusOnlySnapshot pins the split between the two request
// types and the independence rule that goes with it: health answers "how is the
// cache", the state query answers "what is in it", and neither staleness nor
// coldness may move Ready or DegradedReason.
func TestHealthCarriesStatusOnlySnapshot(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.MarkReady()
	h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"),
		time.Now().Add(-protocol.KittySnapshotStaleAfter-time.Second))

	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if resp.KittySnapshot == nil {
		t.Fatal("health response carries no kitty snapshot block")
	}
	if !resp.KittySnapshot.Stale {
		t.Fatalf("health block = %+v; want the aged snapshot reported stale", resp.KittySnapshot)
	}
	if resp.KittySnapshot.Windows != nil {
		t.Fatalf("health block windows = %+v; want status only", resp.KittySnapshot.Windows)
	}
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("ready = %v, degraded = %q; want a stale snapshot to leave readiness untouched",
			resp.Ready, resp.DegradedReason)
	}

	// The other direction: a daemon that started degraded keeps its own reason
	// even once the snapshot is perfectly fresh.
	degraded := newTestHandler()
	degraded.MarkDegraded("initial snapshot timed out")
	degraded.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), time.Now())
	resp = degraded.Handle(protocol.Request{Type: protocol.TypeHealth})
	if resp.KittySnapshot == nil || resp.KittySnapshot.Stale || !resp.KittySnapshot.Ready {
		t.Fatalf("health block = %+v; want a fresh ready snapshot", resp.KittySnapshot)
	}
	if resp.Ready || resp.DegradedReason != "initial snapshot timed out" {
		t.Fatalf("ready = %v, degraded = %q; want the daemon's own degraded reason preserved",
			resp.Ready, resp.DegradedReason)
	}

	// And a cold daemon publishes a block saying so rather than omitting one,
	// which is what lets a client tell "not listed yet" from "old daemon".
	cold := newTestHandler()
	resp = cold.Handle(protocol.Request{Type: protocol.TypeHealth})
	if resp.KittySnapshot == nil {
		t.Fatal("cold daemon omitted the health snapshot block; want a non-nil cold block")
	}
	if resp.KittySnapshot.Ready {
		t.Fatalf("health block = %+v; want cold", resp.KittySnapshot)
	}
}

// TestSnapshotBlockOmitsCoordinatorWithoutStatusSurface pins the degradation
// path for a client that predates the coordination accounting. fakeKittyClient
// implements kittyTitleClient and nothing more, which is exactly the shape the
// daemon's own sweep tests inject — a nil Coordinator must be the answer, not a
// panic and not a zero block that would read as a healthy socket.
func TestSnapshotBlockOmitsCoordinatorWithoutStatusSurface(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{})
	h.publishSnapshot(fakeOSWindowsWithTab(42, "W:api"), time.Now())

	block := stateQuerySnapshot(t, h)
	if block.Coordinator != nil {
		t.Fatalf("coordinator = %+v; want it omitted for a client with no ListStatus", block.Coordinator)
	}
	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if health.KittySnapshot == nil || health.KittySnapshot.Coordinator != nil {
		t.Fatalf("health block = %+v; want the same omission on the health path", health.KittySnapshot)
	}

	// A handler with no Kitty client at all takes the same path rather than
	// dereferencing a nil interface.
	bare := newTestHandler()
	if block := stateQuerySnapshot(t, bare); block.Coordinator != nil {
		t.Fatalf("coordinator = %+v; want none from a handler with no client", block.Coordinator)
	}
}

// TestSnapshotBlockCarriesEffectiveSocketCoordination drives the real
// coordinator through the daemon's own publication path and then asks for the
// block, so it pins both halves of the sampling rule: the counters describe the
// daemon's effective list socket, and taking them does not deadlock against
// stateMu even though kittyClientForList acquires that lock.
func TestSnapshotBlockCarriesEffectiveSocketCoordination(t *testing.T) {
	t.Parallel()

	runner := newGatedListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	h, _ := newCoordinatedHandler(t, runner)
	h.primeSnapshot()

	block := stateQuerySnapshot(t, h)
	if block.Coordinator == nil {
		t.Fatal("coordinator block missing after a real coordinated list")
	}
	if block.Coordinator.Socket != coordinationSocket {
		t.Fatalf("coordinator socket = %q; want the daemon's effective socket %q",
			block.Coordinator.Socket, coordinationSocket)
	}
	if block.Coordinator.Attempts != 1 || block.Coordinator.Failures != 0 {
		t.Fatalf("coordinator = %+v; want one settled successful attempt", block.Coordinator)
	}
	if block.Coordinator.ConsecutiveFailures != 0 || block.Coordinator.LastErrorClass != "" {
		t.Fatalf("coordinator = %+v; want a healthy socket", block.Coordinator)
	}
}

// TestStateQueryBindsSnapshotToCaptureSocket pins which Kitty instance a
// published tree belongs to, against the one thing that moves underneath it.
// The daemon's effective socket is retargeted the moment a hook arrives from a
// second Kitty; the snapshot published against the first one does not move with
// it. Answering "which Kitty produced this tree" with "which Kitty is the daemon
// talking to now" is what would let a dashboard in the second instance accept
// the first instance's window IDs as its own and write to them.
//
// The two sockets are deliberately both present in one block, because they
// answer different questions: Socket describes THIS publication, Coordinator
// describes the socket the daemon is reading through at response time.
func TestStateQueryBindsSnapshotToCaptureSocket(t *testing.T) {
	t.Parallel()

	runner := newGatedListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	h, client := newCoordinatedHandler(t, runner)
	h.primeSnapshot()

	// A hook from a second Kitty instance retargets the daemon. Nothing is
	// published, so the cached tree is still the first instance's.
	h.stateMu.Lock()
	h.rememberKittySocketLocked(42, otherSocket)
	h.stateMu.Unlock()
	// Give the new socket its own coordination accounting without publishing
	// anything, so the block below can carry both sockets at once.
	if _, err := client.WithSocket(otherSocket).ListWindows(); err != nil {
		t.Fatalf("listing the retargeted socket: %v", err)
	}

	block := stateQuerySnapshot(t, h)
	if block.Socket != coordinationSocket {
		t.Fatalf("snapshot socket = %q; want the %q the published windows were read through",
			block.Socket, coordinationSocket)
	}
	if block.Generation != 1 {
		t.Fatalf("generation = %d; want the retarget to have published nothing", block.Generation)
	}
	if block.Coordinator == nil || block.Coordinator.Socket != otherSocket {
		t.Fatalf("coordinator = %+v; want the daemon's current effective socket %q",
			block.Coordinator, otherSocket)
	}
	if len(block.Windows) != 1 || block.Windows[0].Tabs[0].Windows[0].ID != 42 {
		t.Fatalf("block windows = %+v; want the tree captured on the original socket", block.Windows)
	}

	// Health reports the same binding: the block is status-only, but the socket
	// it names is still the capture's rather than the daemon's current one.
	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if health.KittySnapshot == nil || health.KittySnapshot.Socket != coordinationSocket {
		t.Fatalf("health block = %+v; want the same capture socket %q",
			health.KittySnapshot, coordinationSocket)
	}

	// The binding follows publication, not response time: the first list taken
	// through the new socket rebinds the snapshot to it.
	h.primeSnapshot()
	rebound := stateQuerySnapshot(t, h)
	if rebound.Socket != otherSocket || rebound.Generation != 2 {
		t.Fatalf("rebound block = %+v; want generation 2 bound to %q", rebound, otherSocket)
	}
}

// TestStateQueryCapturesStateAndTopologyCoherently is the lock-order test. The
// publisher below stamps a generation tag into a window's state and publishes
// the matching topology in ONE stateMu critical section, exactly as
// publishSnapshotWithReceipts does with the mute cache. A state_query that read
// its states and its topology under two separate acquisitions would eventually
// answer with a tag from one publication and a tree from the next; reading both
// under the established stateMu → SnapshotStore order is what makes that
// impossible.
func TestStateQueryCapturesStateAndTopologyCoherently(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	const windowID = 42

	stop := make(chan struct{})
	var publisher sync.WaitGroup
	publisher.Add(1)
	go func() {
		defer publisher.Done()
		for generation := uint64(1); ; generation++ {
			select {
			case <-stop:
				return
			default:
			}
			tag := strconv.FormatUint(generation, 10)
			h.stateMu.Lock()
			st := h.ensureWindowStateLocked(windowID)
			st.StateSet = true
			st.LastAction = tag
			h.snapshots.Publish(probes.Publication{
				Windows:    fakeOSWindowsWithTab(windowID, "W:"+tag),
				CapturedAt: time.Now(),
			})
			h.stateMu.Unlock()
		}
	}()

	for i := 0; i < 500; i++ {
		resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
		block := resp.KittySnapshot
		if block == nil {
			t.Fatal("state query carries no kitty snapshot block")
		}
		if !block.Ready {
			// The first iterations can precede the first publication.
			continue
		}
		state, ok := resp.States[windowID]
		if !ok {
			t.Fatalf("window %d missing from states while topology generation %d is published",
				windowID, block.Generation)
		}
		if len(block.Windows) != 1 {
			t.Fatalf("block windows = %+v; want exactly the published tree", block.Windows)
		}
		if got, want := block.Windows[0].Tabs[0].Title, "W:"+state.LastAction; got != want {
			t.Fatalf("topology tab title %q does not match the state tag %q; "+
				"state and topology came from different publications", got, want)
		}
	}
	close(stop)
	publisher.Wait()
}

// stateQuerySnapshot issues a real state query through Handle and returns its
// snapshot block, failing the test when the block is absent — a new daemon owes
// one on every state query, whatever the cache holds.
func stateQuerySnapshot(t *testing.T, h *Handler) *protocol.KittySnapshot {
	t.Helper()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("state query failed: %s", resp.Error)
	}
	if resp.KittySnapshot == nil {
		t.Fatal("state query response carries no kitty snapshot block")
	}
	return resp.KittySnapshot
}

// statusClient is a kittyTitleClient that also carries coordination accounting,
// used to prove kittyListStatus reports what a status-bearing client says
// without going anywhere near a real Kitty.
type statusClient struct {
	fakeKittyClient
	status kitty.ListStatus
	ok     bool
}

func (c *statusClient) ListStatus() (kitty.ListStatus, bool) { return c.status, c.ok }

// TestKittyListStatusTreatsAbsentAccountingAsNoInformation pins the collapse of
// the three "nothing to say" cases onto one flag. An evicted or never-read
// socket answers false just like a client with no status surface, and all three
// must omit the block rather than publish zeros a consumer could mistake for a
// healthy socket.
func TestKittyListStatusTreatsAbsentAccountingAsNoInformation(t *testing.T) {
	t.Parallel()

	present := &statusClient{status: kitty.ListStatus{Socket: "unix:/tmp/kitty-1", Attempts: 3}, ok: true}
	status, ok := kittyListStatus(present, "")
	if !ok || status.Attempts != 3 {
		t.Fatalf("kittyListStatus = (%+v, %v); want the client's own accounting", status, ok)
	}

	absent := &statusClient{ok: false}
	if _, ok := kittyListStatus(absent, ""); ok {
		t.Fatal("kittyListStatus reported accounting for a never-read socket")
	}
	if _, ok := kittyListStatus(&fakeKittyClient{}, ""); ok {
		t.Fatal("kittyListStatus reported accounting for a client without the surface")
	}
	if _, ok := kittyListStatus(nil, "unix:/tmp/kitty-1"); ok {
		t.Fatal("kittyListStatus reported accounting for a nil client")
	}
}
