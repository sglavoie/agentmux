package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/commands"
)

// The dashboard half of the cross-layer fault acceptance. Everything else in
// this package tests the refresh controller against a recorded seam; this file
// runs the REAL commands.Refresh against a Kitty that will not answer, because
// the property at stake — that a wedged fallback listing is not multiplied by
// the ticks that arrive while it is parked — only exists when the model's
// in-flight gate and the command's direct-listing fallback are wired together.
//
// The daemon is unreachable throughout, which is exactly the condition that
// puts the refresh on the fallback path in the first place. Nothing sleeps: the
// listing parks on a gate and the clock is a skew the test sets.

// blockingKittyClient parks ListWindows until it is released, and counts the
// calls it received. It is the dashboard's own `kitten @ ls`, which is what a
// multiplied fallback would spawn more of.
type blockingKittyClient struct {
	mu      sync.Mutex
	windows []kitty.OSWindow
	err     error
	calls   int
	gate    chan struct{}
	entered chan struct{}
}

func newBlockingKittyClient(windows []kitty.OSWindow) *blockingKittyClient {
	return &blockingKittyClient{windows: windows, entered: make(chan struct{}, 8)}
}

// park makes every subsequent listing block until the returned release runs.
func (c *blockingKittyClient) park() (release func()) {
	gate := make(chan struct{})
	c.mu.Lock()
	c.gate = gate
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.gate = nil
			c.mu.Unlock()
			close(gate)
		})
	}
}

func (c *blockingKittyClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func (c *blockingKittyClient) heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = nil
}

func (c *blockingKittyClient) listCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *blockingKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	c.mu.Lock()
	c.calls++
	gate := c.gate
	c.mu.Unlock()
	select {
	case c.entered <- struct{}{}:
	default:
	}
	if gate != nil {
		<-gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return c.windows, nil
}

func (c *blockingKittyClient) SetUserVars(string, map[string]string) error { return nil }
func (c *blockingKittyClient) LaunchTab(kitty.LaunchOpts) error            { return nil }
func (c *blockingKittyClient) LaunchOverlay(kitty.LaunchOpts) error        { return nil }
func (c *blockingKittyClient) CloseTab(string) error                       { return nil }
func (c *blockingKittyClient) MoveTab(string, int) error                   { return nil }
func (c *blockingKittyClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (c *blockingKittyClient) SetTabTitle(string, string) error       { return nil }
func (c *blockingKittyClient) FocusTab(string) error                  { return nil }
func (c *blockingKittyClient) FocusWindow(string) error               { return nil }
func (c *blockingKittyClient) GetText(string, string) (string, error) { return "", nil }
func (c *blockingKittyClient) LaunchOverlayPager(string) error        { return nil }

// managedTree is one managed session in one tab: the smallest tree that becomes
// a dashboard row.
func managedTree(title string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID: 7, Title: title,
			Windows: []kitty.Window{{
				ID: 42, IsActive: true,
				UserVars: kitty.UserVars{
					Profile: "work", Harness: "claude",
					AMXDirectory: "/work/api", LaunchID: "api-launch",
				},
			}},
		}},
	}}
}

// realRefreshDispatcher replaces the model's live-topology seam with the REAL
// commands.Refresh, and records every command the controller built. Recording
// at build time is what separates the two things this file measures: how many
// refreshes the gate let through, and how many listings the ones that ran cost.
type realRefreshDispatcher struct{ cmds []tea.Cmd }

func dispatchRealRefreshes(m *Model) *realRefreshDispatcher {
	dispatcher := &realRefreshDispatcher{}
	m.backgroundCommands.topologyRefresh = func(model Model, seq, sourceWindowID int) tea.Cmd {
		cmd := commands.Refresh(model.client, model.cfg, model.sortMode, seq, sourceWindowID)
		dispatcher.cmds = append(dispatcher.cmds, cmd)
		return cmd
	}
	return dispatcher
}

func (d *realRefreshDispatcher) count() int { return len(d.cmds) }

// run executes the most recently dispatched refresh off the update loop, as the
// Bubble Tea runtime would, and returns the message it reported back.
func (d *realRefreshDispatcher) run(t *testing.T) tea.Msg {
	t.Helper()
	if len(d.cmds) == 0 {
		t.Fatal("no refresh was dispatched")
	}
	done := make(chan tea.Msg, 1)
	cmd := d.cmds[len(d.cmds)-1]
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatched refresh did not complete")
		return nil
	}
}

// TestWedgedFallbackListingIsNotMultipliedByTicks is the dashboard's own
// subprocess bound. With no daemon to read a snapshot from, every refresh runs
// `kitten @ ls` here; a dashboard that dispatched one per tick would pile
// subprocesses onto exactly the Kitty that is already failing to answer.
func TestWedgedFallbackListingIsNotMultipliedByTicks(t *testing.T) {
	stubUnreachableDaemon(t)
	client := newBlockingKittyClient(managedTree("api"))
	release := client.park()
	defer release()

	cfg := &config.Config{Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2}}
	m := NewModel(cfg, client)
	m.width = 100
	dispatcher := dispatchRealRefreshes(&m)
	if !m.topologyRefreshInFlight {
		t.Fatal("constructor left no refresh in flight; there would be nothing to coalesce against")
	}

	// The startup refresh runs off the update loop and parks inside Kitty.
	startup := make(chan tea.Msg, 1)
	startupCmd := m.runTopologyRefreshCmd(m.topologyRefreshActiveSeq, 0)
	go func() { startup <- startupCmd() }()
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the startup refresh never reached Kitty")
	}

	// A burst of ticks arrives while the listing is wedged.
	const ticks = 5
	for range ticks {
		m = send(m, commands.TickMsg{})
	}
	if got := dispatcher.count(); got != 1 {
		t.Fatalf("%d refreshes dispatched during a wedged one; want only the startup refresh", got)
	}
	if got := client.listCalls(); got != 1 {
		t.Fatalf("%d listings ran while one was wedged and %d ticks arrived; want the single parked read",
			got, ticks)
	}
	if !m.topologyRefreshPending {
		t.Fatal("the coalesced ticks left no pending request; the catch-up would be lost")
	}

	// Kitty answers, and the whole burst costs exactly one catch-up.
	release()
	m = send(m, <-startup)
	if got := dispatcher.count(); got != 2 {
		t.Fatalf("%d refreshes after the completion; want the startup plus exactly one catch-up", got)
	}
	if len(m.sessions) != 1 {
		t.Fatalf("the recovered listing produced %d rows, want the one managed session", len(m.sessions))
	}
	if msg, ok := dispatcher.run(t).(commands.SessionsMsg); !ok {
		t.Fatalf("catch-up produced %T, want a SessionsMsg", msg)
	}
	if got := client.listCalls(); got != 2 {
		t.Fatalf("%d listings for a startup plus %d coalesced ticks; want 2", got, ticks)
	}
}

// TestFallbackOutageDegradesThenRecoversWithoutMultiplying is the same gate seen
// from the operator's side: a Kitty that refuses every listing leaves the rows
// on screen and eventually warns about their age, retries only on the tick
// rather than on the failure, and clears the warning on the first listing that
// works again.
func TestFallbackOutageDegradesThenRecoversWithoutMultiplying(t *testing.T) {
	stubUnreachableDaemon(t)
	client := newBlockingKittyClient(managedTree("api"))

	var skew time.Duration
	cfg := &config.Config{Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2}}
	m := NewModel(cfg, client)
	m.width = 100
	m.now = func() time.Time { return time.Now().Add(skew) }
	dispatcher := dispatchRealRefreshes(&m)

	// Warm the dashboard through the real fallback path, so its capture instant
	// is the one commands.Refresh stamped rather than one a test invented.
	m.backgroundCommands.topologyRefresh(m, m.topologyRefreshActiveSeq, 0)
	m = send(m, dispatcher.run(t))
	if len(m.sessions) != 1 {
		t.Fatalf("warm-up produced %d rows, want 1", len(m.sessions))
	}
	assertStatusBarLacks(t, m, staleWarningPrefix)

	// Kitty starts refusing. Each tick costs exactly one listing attempt, and a
	// failure is never followed by an immediate retry.
	client.fail(errors.New("kitten @ ls: connection refused"))
	before := client.listCalls()
	dispatched := dispatcher.count()
	for range 3 {
		m = send(m, commands.TickMsg{})
		if got := dispatcher.count() - dispatched; got != 1 {
			t.Fatalf("a tick with nothing in flight dispatched %d refreshes; want 1", got)
		}
		dispatched = dispatcher.count()
		m = send(m, dispatcher.run(t))
		if dispatcher.count() != dispatched {
			t.Fatal("a failed refresh dispatched an immediate retry")
		}
	}
	if got := client.listCalls() - before; got != 3 {
		t.Fatalf("%d listings for three ticks during the outage; want one per tick", got)
	}

	// The rows survive, and once the tree is old enough the operator is told —
	// as a warning over a working list, not as a red error.
	if len(m.sessions) != 1 {
		t.Fatalf("the outage emptied the dashboard: %+v", m.sessions)
	}
	skew = protocol.KittySnapshotStaleAfter + 4*time.Second
	assertStatusBarContains(t, m, staleWarningPrefix)
	assertStatusBarLacks(t, m, "Kitty error")
	if msg := currentStatusMessage(m, m.now()); msg.Severity != statusWarning {
		t.Fatalf("outage severity = %v, want %v", msg.Severity, statusWarning)
	}

	// Kitty recovers on the next tick, and the warning goes with it.
	client.heal()
	skew = 0
	m = send(m, commands.TickMsg{})
	m = send(m, dispatcher.run(t))
	assertStatusBarLacks(t, m, staleWarningPrefix)
	assertStatusBarLacks(t, m, "Kitty error")
	if m.err != nil {
		t.Fatalf("recovery left err = %v, want it cleared", m.err)
	}
	if m.topology.Source != commands.TopologyFromDirect {
		t.Fatalf("topology source = %v, want the direct fallback", m.topology.Source)
	}
}
