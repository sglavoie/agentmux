package commands

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// recordingKittyClient is a KittyClient that answers ListWindows from a fixed
// tree and records the calls a refresh made, so a test can assert not only what
// a refresh produced but what it touched. Every method a refresh does not use
// fails loudly rather than silently succeeding.
type recordingKittyClient struct {
	mu       sync.Mutex
	windows  []kitty.OSWindow
	listErr  error
	lists    int
	userVars []map[string]string
}

func (c *recordingKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.windows, nil
}

func (c *recordingKittyClient) SetUserVars(_ string, vars map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userVars = append(c.userVars, vars)
	return nil
}

func (c *recordingKittyClient) listCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists
}

func (c *recordingKittyClient) userVarWrites() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.userVars)
}

func (c *recordingKittyClient) LaunchTab(kitty.LaunchOpts) error { panic("unexpected LaunchTab") }
func (c *recordingKittyClient) LaunchOverlay(kitty.LaunchOpts) error {
	panic("unexpected LaunchOverlay")
}
func (c *recordingKittyClient) CloseTab(string) error     { panic("unexpected CloseTab") }
func (c *recordingKittyClient) MoveTab(string, int) error { panic("unexpected MoveTab") }
func (c *recordingKittyClient) SetTabColor(string, string, string, string, string) error {
	panic("unexpected SetTabColor")
}
func (c *recordingKittyClient) SetTabTitle(string, string) error { panic("unexpected SetTabTitle") }
func (c *recordingKittyClient) FocusTab(string) error            { panic("unexpected FocusTab") }
func (c *recordingKittyClient) FocusWindow(string) error         { panic("unexpected FocusWindow") }
func (c *recordingKittyClient) GetText(string, string) (string, error) {
	panic("unexpected GetText")
}
func (c *recordingKittyClient) LaunchOverlayPager(string) error {
	panic("unexpected LaunchOverlayPager")
}

// stubStateQuery installs the refresh's daemon seam for one test. Without it a
// refresh would dial the operator's live daemon socket.
func stubStateQuery(t *testing.T, query func() (protocol.Response, error)) {
	t.Helper()
	previous := StateQuery
	StateQuery = query
	t.Cleanup(func() { StateQuery = previous })
}

// stubLocalKittySocket pins the Kitty instance the refresh believes it is
// running inside. Without it the answer comes from the operator's own
// environment, so a test asserting on socket identity would pass or fail
// depending on whether `go test` was launched from inside Kitty.
func stubLocalKittySocket(t *testing.T, socket string) {
	t.Helper()
	previous := LocalKittySocket
	LocalKittySocket = func() string { return socket }
	t.Cleanup(func() { LocalKittySocket = previous })
}

// managedWindows is one managed session inside one tab, which is the smallest
// tree ListFromOSWindowsWithConfig turns into a dashboard row.
func managedWindows(tabID, windowID int, title string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    tabID,
			Title: title,
			Windows: []kitty.Window{{
				ID:       windowID,
				IsActive: true,
				UserVars: kitty.UserVars{
					Profile: "work", Harness: "claude",
					AMXDirectory: "/work/api", LaunchID: "api-launch",
				},
			}},
		}},
	}}
}

// localKittySocket and foreignKittySocket are the two instance identities every
// socket-attribution test in this file is written against: the one this
// dashboard runs inside, and one belonging to somebody else's Kitty.
const (
	localKittySocket   = "unix:/tmp/kitty-1234"
	foreignKittySocket = "unix:/tmp/kitty-9999"
)

// readySnapshot is a published daemon snapshot attributed to THIS dashboard's
// Kitty. The attribution is part of the default because a snapshot the daemon
// path is expected to consume must be provably local: a tree with no socket is
// unattributed and takes the direct-listing fallback, which is what
// TestRefreshFallsBackWhenDaemonSnapshotIdentityIsMissing pins. Every test using
// this helper therefore pairs it with stubLocalKittySocket(t, localKittySocket).
func readySnapshot(windows []kitty.OSWindow, stale bool) *protocol.KittySnapshot {
	return &protocol.KittySnapshot{
		Ready:      true,
		Stale:      stale,
		Generation: 12,
		CapturedAt: time.Unix(1_700_000_000, 0).Unix(),
		AgeMS:      1500,
		Socket:     localKittySocket,
		Windows:    windows,
	}
}

func refreshOnce(t *testing.T, client kitty.KittyClient, seq, sourceWindowID int) any {
	t.Helper()
	return Refresh(client, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, "alpha", seq, sourceWindowID)()
}

// TestRefreshPrefersDaemonSnapshotOverListingKitty is the whole point of the
// daemon-first path: the daemon already ran `kitten @ ls`, so the dashboard runs
// none of its own.
func TestRefreshPrefersDaemonSnapshotOverListingKitty(t *testing.T) {
	daemonTree := managedWindows(7, 42, "api")
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{
			OK:            true,
			Activity:      map[int]int64{42: 1_700_000_500},
			Watchdog:      map[int]protocol.WatchdogState{42: {LastAction: "active", ThresholdSec: 600}},
			States:        map[int]protocol.WindowStateSnapshot{42: {IconState: "key", Looping: true}},
			KittySnapshot: readySnapshot(daemonTree, false),
		}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	// A tree the daemon's snapshot disagrees with, so the assertion below can
	// only pass if the daemon's copy is what was rendered.
	client := &recordingKittyClient{windows: managedWindows(9, 99, "stale-direct")}

	msg, ok := refreshOnce(t, client, 3, 0).(SessionsMsg)
	if !ok {
		t.Fatalf("refresh produced %T, want SessionsMsg", msg)
	}
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0 with a ready daemon snapshot", client.listCalls())
	}
	if msg.Seq != 3 {
		t.Fatalf("message sequence = %d, want the requested 3", msg.Seq)
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 7 {
		t.Fatalf("sessions = %+v, want the daemon snapshot's tab 7", msg.Sessions)
	}
	if got := msg.Sessions[0]; got.State != "key" || !got.Looping {
		t.Fatalf("session state = %+v, want the daemon's key + looping", got)
	}
	// The maps travel with the tree they were proven alongside: a refusal that
	// dropped them here would be indistinguishable from one that never had them.
	if !msg.Activity[42].Equal(time.Unix(1_700_000_500, 0)) {
		t.Fatalf("activity = %v, want the daemon's timestamp", msg.Activity[42])
	}
	if msg.Watchdog[42].LastAction != "active" || msg.States[42].IconState != "key" {
		t.Fatalf("daemon maps did not reach an attributed message: %+v / %+v", msg.Watchdog, msg.States)
	}
	if msg.Topology.Source != TopologyFromDaemon || msg.Topology.Stale {
		t.Fatalf("topology = %+v, want a fresh daemon source", msg.Topology)
	}
	if msg.Topology.Generation != 12 || msg.Topology.Age != 1500*time.Millisecond {
		t.Fatalf("topology freshness = %+v, want generation 12 and a 1.5s age", msg.Topology)
	}
}

// TestRefreshReusesStaleDaemonSnapshotWithoutListingKitty pins both halves of
// the stale rule: a stale tree is still rendered rather than discarded, and it
// still costs no Kitty subprocess — but it drives no write back into Kitty,
// because every window ID in it may already be gone.
func TestRefreshReusesStaleDaemonSnapshotWithoutListingKitty(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	tree[0].Tabs = append(tree[0].Tabs, kitty.Tab{
		ID:      70,
		Windows: []kitty.Window{{ID: 700, UserVars: kitty.UserVars{DashboardSelect: "7:request-1"}}},
	})
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(tree, true)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := refreshOnce(t, client, 1, 0).(SessionsMsg)
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0 for a stale-but-ready snapshot", client.listCalls())
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 7 {
		t.Fatalf("sessions = %+v, want the stale snapshot still rendered", msg.Sessions)
	}
	if !msg.Topology.Stale || msg.Topology.Source != TopologyFromDaemon {
		t.Fatalf("topology = %+v, want a stale daemon source", msg.Topology)
	}
	if msg.SelectionTabID != 7 || msg.SelectionToken != "request-1" {
		t.Fatalf("selection = tab %d token %q, want tab 7 token request-1", msg.SelectionTabID, msg.SelectionToken)
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("stale snapshot drove %d user-var writes, want 0", client.userVarWrites())
	}
}

// TestRefreshWritesBackFromAFreshDaemonSnapshot is the control for the test
// above: the write is skipped because the snapshot was stale, not because the
// daemon path never writes.
func TestRefreshWritesBackFromAFreshDaemonSnapshot(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	tree[0].Tabs = append(tree[0].Tabs, kitty.Tab{
		ID:      70,
		Windows: []kitty.Window{{ID: 700, UserVars: kitty.UserVars{DashboardSelect: "7:request-1"}}},
	})
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(tree, false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	if msg := refreshOnce(t, client, 1, 0).(SessionsMsg); msg.SelectionTabID != 7 {
		t.Fatalf("selection tab = %d, want 7", msg.SelectionTabID)
	}
	if client.userVarWrites() != 1 {
		t.Fatalf("fresh snapshot drove %d user-var writes, want 1", client.userVarWrites())
	}
}

// ghostConfig describes a project the dashboard can recognize from a tab title
// alone. That is what lets a tab whose anchor window was closed still be matched,
// and therefore what makes it a ghost worth re-stamping.
func ghostConfig() *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "api", Account: "work", Directory: "/work/api"}},
	}
}

// ghostTree is one such tab: the title still names the configured project and a
// harness is still running in it, but no window carries the amx user vars any
// more. Tab 70 alongside it holds a pending cross-window selection request, so a
// single tree exercises both topology-derived Kitty writes at once.
func ghostTree() []kitty.OSWindow {
	return []kitty.OSWindow{{
		Tabs: []kitty.Tab{
			{
				ID:    7,
				Title: "api",
				Windows: []kitty.Window{{
					ID:                  42,
					IsActive:            true,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"claude"}}},
				}},
			},
			{
				ID:      70,
				Windows: []kitty.Window{{ID: 700, UserVars: kitty.UserVars{DashboardSelect: "7:request-1"}}},
			},
		},
	}}
}

func refreshOnceWith(t *testing.T, client kitty.KittyClient, cfg *config.Config, seq int) any {
	t.Helper()
	return Refresh(client, cfg, cfg.Defaults.Sort, seq, 0)()
}

// TestStaleSnapshotDefersBothTopologyDerivedWrites is the safety half of the
// stale rule. A ghost re-stamp and a selection clear both aim at a window ID
// read out of a tree old enough that the window may already be gone — and a
// write aimed at a vanished window is at best wasted and at worst aimed at its
// replacement. Both are dropped, and the row they were about is rendered anyway.
func TestStaleSnapshotDefersBothTopologyDerivedWrites(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), true)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := refreshOnceWith(t, client, ghostConfig(), 1).(SessionsMsg)
	if len(msg.Sessions) != 1 || msg.Sessions[0].WindowID != 42 {
		t.Fatalf("sessions = %+v, want the ghost tab still rendered as window 42", msg.Sessions)
	}
	if msg.SelectionTabID != 7 || msg.SelectionToken != "request-1" {
		t.Fatalf("selection = tab %d token %q, want tab 7 token request-1", msg.SelectionTabID, msg.SelectionToken)
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("stale snapshot drove %d user-var writes, want 0", client.userVarWrites())
	}
}

// TestRepeatedStaleRefreshesStayReadOnly extends the rule from one refresh to an
// outage. Nothing accumulates across stale refreshes that would eventually let a
// write through, and none of them falls back to a Kitty subprocess either.
func TestRepeatedStaleRefreshesStayReadOnly(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), true)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}
	cfg := ghostConfig()

	for seq := 1; seq <= 5; seq++ {
		if _, ok := refreshOnceWith(t, client, cfg, seq).(SessionsMsg); !ok {
			t.Fatalf("refresh %d did not produce a session list", seq)
		}
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("five stale refreshes drove %d user-var writes, want 0", client.userVarWrites())
	}
	if client.listCalls() != 0 {
		t.Fatalf("five stale refreshes ran %d Kitty listings, want 0", client.listCalls())
	}
}

// TestFreshRecoveryPerformsBothDeferredWrites closes the deferral. Neither write
// is lost by being skipped: both triggers are re-detected from the next fresh
// tree, because a stale refresh never cleared the evidence that raised them.
func TestFreshRecoveryPerformsBothDeferredWrites(t *testing.T) {
	stale := true
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), stale)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}
	cfg := ghostConfig()

	refreshOnceWith(t, client, cfg, 1)
	if client.userVarWrites() != 0 {
		t.Fatalf("the stale refresh wrote %d times, want 0", client.userVarWrites())
	}

	stale = false
	refreshOnceWith(t, client, cfg, 2)

	client.mu.Lock()
	writes := append([]map[string]string(nil), client.userVars...)
	client.mu.Unlock()
	if len(writes) != 2 {
		t.Fatalf("fresh recovery wrote %d times, want the re-anchor and the selection clear", len(writes))
	}
	clears, reanchors := 0, 0
	for _, vars := range writes {
		if value, ok := vars[kitty.DashboardSelectionVar]; ok && len(vars) == 1 && value == "" {
			clears++
			continue
		}
		if vars["amx_project"] == "api" {
			reanchors++
		}
	}
	if clears != 1 || reanchors != 1 {
		t.Fatalf("recovery writes = %+v, want exactly one selection clear and one ghost re-anchor", writes)
	}
}

// TestRefreshFallsBackToDirectListing enumerates every reason the daemon cannot
// supply a topology. All four end in the same place — one direct listing — but
// they are genuinely different daemon states and each has to be handled.
func TestRefreshFallsBackToDirectListing(t *testing.T) {
	cases := []struct {
		name  string
		query func() (protocol.Response, error)
	}{
		{
			// An older daemon that predates the block omits it entirely.
			name: "legacy daemon omits the block",
			query: func() (protocol.Response, error) {
				return protocol.Response{OK: true}, nil
			},
		},
		{
			// A new daemon that has not completed a successful list yet.
			name: "cold snapshot",
			query: func() (protocol.Response, error) {
				return protocol.Response{OK: true, KittySnapshot: &protocol.KittySnapshot{Ready: false}}, nil
			},
		},
		{
			// The client turns resp.OK == false into an error, so a refusal
			// arrives here as one.
			name: "daemon rejected the query",
			query: func() (protocol.Response, error) {
				return protocol.Response{}, errors.New("daemon error: unknown request type")
			},
		},
		{
			name: "daemon unreachable",
			query: func() (protocol.Response, error) {
				return protocol.Response{}, errors.New("dial unix: connect: no such file or directory")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubStateQuery(t, tc.query)
			client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

			msg, ok := refreshOnce(t, client, 5, 0).(SessionsMsg)
			if !ok {
				t.Fatalf("refresh produced %T, want SessionsMsg", msg)
			}
			if client.listCalls() != 1 {
				t.Fatalf("direct Kitty listings = %d, want exactly 1", client.listCalls())
			}
			if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 9 {
				t.Fatalf("sessions = %+v, want the directly listed tab 9", msg.Sessions)
			}
			if msg.Topology.Source != TopologyFromDirect || msg.Topology.Stale {
				t.Fatalf("topology = %+v, want a fresh direct source", msg.Topology)
			}
		})
	}
}

// snapshotFrom is a fresh, ready snapshot re-attributed to one named Kitty
// instance, or — for the empty socket — to none at all. Window IDs are only
// meaningful inside the instance they were read from, so the socket is the field
// that says whether this tree is about this dashboard's Kitty at all.
func snapshotFrom(socket string, windows []kitty.OSWindow) *protocol.KittySnapshot {
	snapshot := readySnapshot(windows, false)
	snapshot.Socket = socket
	return snapshot
}

// TestRefreshRejectsDaemonSnapshotFromDifferentKittySocket is the fifth reason
// the daemon cannot supply a topology, and the only one where the daemon is
// perfectly healthy: it is watching a different Kitty instance than this
// dashboard runs in. Its window IDs name windows in that other instance, so
// rendering them would show foreign rows and — worse — every topology-derived
// write would land on whatever window happens to hold the same ID here. The
// tree is dropped for the safe fallback, and no write is attempted from it.
func TestRefreshRejectsDaemonSnapshotFromDifferentKittySocket(t *testing.T) {
	// The daemon's tree is the ghost tree, which drives both topology-derived
	// writes when it is consumed fresh. If either write is observed, the
	// mismatched tree was acted on.
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{
			OK:            true,
			States:        map[int]protocol.WindowStateSnapshot{42: {IconState: "key", Looping: true}},
			KittySnapshot: snapshotFrom(foreignKittySocket, ghostTree()),
		}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

	msg, ok := refreshOnceWith(t, client, ghostConfig(), 4).(SessionsMsg)
	if !ok {
		t.Fatalf("refresh produced %T, want SessionsMsg", msg)
	}
	if client.listCalls() != 1 {
		t.Fatalf("direct Kitty listings = %d, want exactly 1 for a foreign snapshot", client.listCalls())
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 9 {
		t.Fatalf("sessions = %+v, want this Kitty's directly listed tab 9", msg.Sessions)
	}
	if msg.Topology.Source != TopologyFromDirect || msg.Topology.Stale {
		t.Fatalf("topology = %+v, want a fresh direct source", msg.Topology)
	}
	// The foreign tree carried a pending selection request in tab 70 and a ghost
	// in tab 7. Neither may reach Kitty: both name IDs from the other instance.
	if client.userVarWrites() != 0 {
		t.Fatalf("a foreign snapshot drove %d user-var writes, want 0", client.userVarWrites())
	}
	if msg.SelectionTabID != 0 || msg.SelectionToken != "" {
		t.Fatalf("selection = tab %d token %q, want none from a foreign tree",
			msg.SelectionTabID, msg.SelectionToken)
	}
}

// TestRefreshFallsBackWhenDaemonSnapshotIdentityIsMissing is the mismatch test's
// other half: agreement has to be PROVEN, so an absent identity on either side
// is refused exactly as a known mismatch is. An unattributed tree may belong to
// any Kitty at all — an old daemon omits the socket, a daemon reading through no
// reusable endpoint has none to report, and a dashboard outside Kitty or behind
// an unnormalizable address cannot name its own — and reading that silence as
// agreement is what would let a foreign tree be rendered as ours and written
// back into our windows. The price is one `kitten @ ls` per refresh, which is
// the documented fallback rather than a failure.
func TestRefreshFallsBackWhenDaemonSnapshotIdentityIsMissing(t *testing.T) {
	cases := []struct {
		name           string
		snapshotSocket string
		localSocket    string
	}{
		{
			name:           "daemon predates the socket field",
			snapshotSocket: "",
			localSocket:    localKittySocket,
		},
		{
			name:           "dashboard cannot name its own instance",
			snapshotSocket: localKittySocket,
			localSocket:    "",
		},
		{
			name:           "neither side is identified",
			snapshotSocket: "",
			localSocket:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The ghost tree drives both topology-derived writes when it is
			// consumed fresh, so either write observed here means the
			// unattributed tree was acted on.
			stubStateQuery(t, func() (protocol.Response, error) {
				return protocol.Response{
					OK:            true,
					States:        map[int]protocol.WindowStateSnapshot{42: {IconState: "key", Looping: true}},
					KittySnapshot: snapshotFrom(tc.snapshotSocket, ghostTree()),
				}, nil
			})
			stubLocalKittySocket(t, tc.localSocket)
			client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

			msg, ok := refreshOnceWith(t, client, ghostConfig(), 4).(SessionsMsg)
			if !ok {
				t.Fatalf("refresh produced %T, want SessionsMsg", msg)
			}
			if client.listCalls() != 1 {
				t.Fatalf("direct Kitty listings = %d, want exactly 1 for an unattributed snapshot",
					client.listCalls())
			}
			if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 9 {
				t.Fatalf("sessions = %+v, want this Kitty's directly listed tab 9", msg.Sessions)
			}
			if msg.Topology.Source != TopologyFromDirect || msg.Topology.Stale {
				t.Fatalf("topology = %+v, want a fresh direct source", msg.Topology)
			}
			if client.userVarWrites() != 0 {
				t.Fatalf("an unattributed snapshot drove %d user-var writes, want 0", client.userVarWrites())
			}
			if msg.SelectionTabID != 0 || msg.SelectionToken != "" {
				t.Fatalf("selection = tab %d token %q, want none from an unattributed tree",
					msg.SelectionTabID, msg.SelectionToken)
			}
		})
	}
}

// TestRefreshAcceptsDaemonSnapshotFromTheSameKittySocket is the control for both
// refusals above: the tree they dropped is dropped for want of proven identity,
// not because the daemon path never consumes a tree or never writes from one.
func TestRefreshAcceptsDaemonSnapshotFromTheSameKittySocket(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{
			OK:            true,
			KittySnapshot: snapshotFrom(localKittySocket, ghostTree()),
		}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

	msg := refreshOnceWith(t, client, ghostConfig(), 4).(SessionsMsg)
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0 for a matching snapshot", client.listCalls())
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 7 {
		t.Fatalf("sessions = %+v, want the daemon snapshot's tab 7", msg.Sessions)
	}
	if msg.Topology.Source != TopologyFromDaemon {
		t.Fatalf("topology = %+v, want the daemon source", msg.Topology)
	}
	// The same fresh tree the refusals above would not act on: here it is
	// provably this dashboard's own, so both topology-derived writes go through.
	if client.userVarWrites() != 2 {
		t.Fatalf("accepted snapshot drove %d user-var writes, want the re-anchor and the selection clear",
			client.userVarWrites())
	}
}

// assertNoDaemonMetadata fails unless one refresh contributed nothing of the
// daemon's per-window bookkeeping: not to the row it would have described, and
// not to the message that carries the maps onward to the detail and activity
// surfaces. Both halves matter — a row is what the operator sees, and the
// forwarded maps are what everything downstream of this refresh reads.
func assertNoDaemonMetadata(t *testing.T, msg SessionsMsg, windowID int) {
	t.Helper()
	if len(msg.Sessions) != 1 || msg.Sessions[0].WindowID != windowID {
		t.Fatalf("sessions = %+v, want this Kitty's own window %d", msg.Sessions, windowID)
	}
	if got := msg.Sessions[0]; got.State != "" || got.Looping || !got.BusySince.IsZero() {
		t.Fatalf("row = %+v, want no icon state, no loop marker, and no busy anchor", got)
	}
	if msg.States != nil || msg.Watchdog != nil || msg.Activity != nil {
		t.Fatalf("forwarded daemon maps = %+v / %+v / %+v, want all three dropped",
			msg.States, msg.Watchdog, msg.Activity)
	}
}

// TestRefreshDoesNotApplyForeignDaemonStateToDirectTopology is the collision
// itself. The daemon is watching another Kitty instance and answers with state
// for ITS window 99, while this dashboard's own direct listing also has a window
// 99 — a number, not a name, and the two windows share nothing else. Rejecting
// the foreign tree while keeping the maps beside it would paint that other
// instance's icon, loop marker, and busy anchor onto a local row and forward its
// watchdog entry to every surface reading the refresh, so the maps are refused
// by the same proof the tree is.
func TestRefreshDoesNotApplyForeignDaemonStateToDirectTopology(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{
			OK:       true,
			Activity: map[int]int64{99: 1_700_000_500},
			Watchdog: map[int]protocol.WatchdogState{99: {LastAction: "active", ThresholdSec: 600}},
			States: map[int]protocol.WindowStateSnapshot{99: {
				IconState:   "key",
				Looping:     true,
				BusySinceAt: 1_700_000_000,
			}},
			// The foreign tree numbers its own window 99 too, which is exactly
			// why a bare window ID cannot carry state across instances.
			KittySnapshot: snapshotFrom(foreignKittySocket, managedWindows(3, 99, "someone-elses-api")),
		}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

	msg, ok := refreshOnce(t, client, 1, 0).(SessionsMsg)
	if !ok {
		t.Fatalf("refresh produced %T, want SessionsMsg", msg)
	}
	if client.listCalls() != 1 {
		t.Fatalf("direct Kitty listings = %d, want exactly 1 for a foreign snapshot", client.listCalls())
	}
	if msg.Sessions[0].TabID != 9 {
		t.Fatalf("sessions = %+v, want this Kitty's directly listed tab 9", msg.Sessions)
	}
	assertNoDaemonMetadata(t, msg, 99)
}

// TestRefreshDropsDaemonStateWhenTheInstanceIsUnproven extends the refusal from
// a known mismatch to every shape of absent provenance, for the same reason the
// tree gate does: an unattributed map is one whose window IDs might belong to
// any Kitty at all, and a numeric collision with a local window is what makes
// that dangerous rather than merely useless. Each case here is a daemon that
// answered and had per-window state to offer, and none of it is applied.
func TestRefreshDropsDaemonStateWhenTheInstanceIsUnproven(t *testing.T) {
	cases := []struct {
		name        string
		snapshot    *protocol.KittySnapshot
		localSocket string
	}{
		{
			// An older daemon that predates the block says nothing about which
			// Kitty its window IDs came from.
			name:        "legacy daemon omits the block",
			snapshot:    nil,
			localSocket: localKittySocket,
		},
		{
			// A new daemon that has not completed a successful list yet: its
			// state is real, but nothing attributes it to an instance.
			name:        "cold snapshot",
			snapshot:    &protocol.KittySnapshot{Ready: false},
			localSocket: localKittySocket,
		},
		{
			name:        "dashboard cannot name its own instance",
			snapshot:    snapshotFrom(localKittySocket, managedWindows(3, 99, "api")),
			localSocket: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubStateQuery(t, func() (protocol.Response, error) {
				return protocol.Response{
					OK:       true,
					Activity: map[int]int64{99: 1_700_000_500},
					Watchdog: map[int]protocol.WatchdogState{99: {LastAction: "active", ThresholdSec: 600}},
					States: map[int]protocol.WindowStateSnapshot{99: {
						IconState:   "key",
						Looping:     true,
						BusySinceAt: 1_700_000_000,
					}},
					KittySnapshot: tc.snapshot,
				}, nil
			})
			stubLocalKittySocket(t, tc.localSocket)
			client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

			msg, ok := refreshOnce(t, client, 1, 0).(SessionsMsg)
			if !ok {
				t.Fatalf("refresh produced %T, want SessionsMsg", msg)
			}
			if client.listCalls() != 1 {
				t.Fatalf("direct Kitty listings = %d, want exactly 1", client.listCalls())
			}
			assertNoDaemonMetadata(t, msg, 99)
		})
	}
}

// TestRefreshStampsDirectFallbackWithItsOwnCompletionTime pins that a listing
// this process just finished is reported as exactly that — fresh, ageless, and
// not carrying some other capture's timestamp.
func TestRefreshStampsDirectFallbackWithItsOwnCompletionTime(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	})
	client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

	before := time.Now()
	msg := refreshOnce(t, client, 1, 0).(SessionsMsg)
	after := time.Now()

	if msg.Topology.CapturedAt.Before(before) || msg.Topology.CapturedAt.After(after) {
		t.Fatalf("capture time = %v, want it inside [%v, %v]", msg.Topology.CapturedAt, before, after)
	}
	if msg.Topology.Age != 0 || msg.Topology.Generation != 0 {
		t.Fatalf("direct topology = %+v, want zero age and no generation", msg.Topology)
	}
}

// TestRefreshReportsASequenceBearingErrorWhenBothSourcesFail is the total
// failure case. The message carries the sequence so the controller can settle,
// and carries no sessions so the model keeps the list it already has.
func TestRefreshReportsASequenceBearingErrorWhenBothSourcesFail(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	})
	client := &recordingKittyClient{listErr: errors.New("kitten @ ls: signal: killed")}

	msg, ok := refreshOnce(t, client, 4, 0).(ErrMsg)
	if !ok {
		t.Fatalf("refresh produced %T, want ErrMsg", msg)
	}
	if msg.Seq != 4 {
		t.Fatalf("error sequence = %d, want the requested 4", msg.Seq)
	}
	if msg.Err == nil || msg.Error() == "" {
		t.Fatalf("error message = %+v, want the listing failure", msg)
	}
}

// TestRefreshResolvesTheSourcePaneAgainstTheDaemonSnapshot pins that the
// startup selection works off whichever tree won, not only off a direct
// listing.
func TestRefreshResolvesTheSourcePaneAgainstTheDaemonSnapshot(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	tree[0].Tabs[0].Windows = append(tree[0].Tabs[0].Windows, kitty.Window{ID: 99})
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(tree, false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := refreshOnce(t, client, 1, 99).(SessionsMsg)
	if msg.SelectionTabID != 7 {
		t.Fatalf("source tab = %d, want the pane's containing tab 7", msg.SelectionTabID)
	}
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0", client.listCalls())
	}
}

// TestStateQuerySeamBoundsItsWaitAtOneSecond pins the budget the refresh
// actually runs under. The seam is wired to the client's dedicated state-query
// deadline rather than the 250ms acknowledgement budget every other request
// shares, and a daemon that accepts the connection and then says nothing must
// cost one refresh, not an unbounded stall.
func TestStateQuerySeamBoundsItsWaitAtOneSecond(t *testing.T) {
	dir, err := os.MkdirTemp("", "amxq")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// Accept and hold every connection open without ever replying.
	accepted := make(chan net.Conn, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		close(accepted)
		for conn := range accepted {
			_ = conn.Close()
		}
	})

	start := time.Now()
	if _, err := ipc.NewClientWithSocket(socket).StateQuery(); err == nil {
		t.Fatal("silent daemon returned no error, want a deadline failure")
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("state query gave up after %v, want it to hold the full one-second budget", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("state query waited %v, want it bounded near one second", elapsed)
	}
}
