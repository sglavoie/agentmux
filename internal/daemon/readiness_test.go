package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// blockingListKittyClient parks ListWindows until release is closed, modelling a
// slow or wedged Kitty during startup. GetText/SetTabTitle/SetUserVars satisfy
// the interface but are never exercised by the bounded-restore tests.
type blockingListKittyClient struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingListKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return nil, nil
}

func (b *blockingListKittyClient) GetText(string, string) (string, error) { return "", nil }

func (b *blockingListKittyClient) SetTabTitle(string, string) error { return nil }

func (b *blockingListKittyClient) SetUserVars(string, map[string]string) error { return nil }

// errorThenListKittyClient returns an injected error on its FIRST ListWindows,
// modelling a fast startup list failure that returns well before any bounded
// timeout, and serves the persisted windows on every later call. It lets a test
// drive an immediate startup list error followed by a successful maintenance
// snapshot without any blocking or timing dependence.
type errorThenListKittyClient struct {
	windows []kitty.OSWindow
	err     error

	mu    sync.Mutex
	calls int
}

func (c *errorThenListKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	c.mu.Lock()
	c.calls++
	first := c.calls == 1
	c.mu.Unlock()
	if first {
		return nil, c.err
	}
	return c.windows, nil
}

func (c *errorThenListKittyClient) GetText(string, string) (string, error) { return "", nil }

func (c *errorThenListKittyClient) SetTabTitle(string, string) error { return nil }

func (c *errorThenListKittyClient) SetUserVars(string, map[string]string) error { return nil }

func TestRestoreStateFromKittyBounded_ReadyWithResponsiveKitty(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(42, "api")}
	h.useKittyClient(fake)

	ready, reason := h.restoreStateFromKittyBounded(time.Now(), initialSnapshotBudget)
	if !ready || reason != "" {
		t.Fatalf("bounded restore = (%v, %q); want ready with no reason", ready, reason)
	}
	h.MarkReady()

	if _, _, cached := h.cachedSnapshot(); !cached {
		t.Fatal("snapshot cache should be primed after a responsive restore")
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want ready with no degraded reason", resp)
	}
}

func TestRestoreStateFromKittyBounded_DegradedWhenKittyWedged(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	client := &blockingListKittyClient{entered: make(chan struct{}, 1), release: make(chan struct{})}
	h.useKittyClient(client)
	t.Cleanup(func() { close(client.release) })

	ready, reason := h.restoreStateFromKittyBounded(time.Now(), 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)

	// A wedged initial list leaves the cache cold; the daemon still comes up.
	if _, _, cached := h.cachedSnapshot(); cached {
		t.Fatal("snapshot cache should stay cold while the initial list is wedged")
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if resp.Ready || resp.DegradedReason == "" {
		t.Fatalf("health = %+v; want degraded with a reason", resp)
	}

	// Confirm the restore goroutine actually reached the wedged list, proving the
	// timeout (not a missing call) drove the degraded result.
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never attempted the initial list")
	}
}

func TestMaintenanceTick_ClearsStartupDegradedAfterSuccessfulSnapshot(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.useKittyClient(&fakeKittyClient{windows: fakeOSWindowsWithTab(42, "api")})
	h.MarkDegraded("initial kitty snapshot exceeded 400ms")

	h.maintenanceTick(time.Now())

	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if !resp.Ready || resp.DegradedReason != "" {
		t.Fatalf("health = %+v; want recovered readiness after maintenance snapshot", resp)
	}
}
