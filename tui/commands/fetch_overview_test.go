package commands

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// These tests are about RefreshOverview, the read-only half of the same
// retrieval the dashboard runs. Two properties are asserted throughout and are
// the reason the command exists at all: it reaches the same answer as Refresh
// about which tree to use, which daemon state to trust, and which rows a tree
// contains; and it reaches that answer having written nothing.
//
// Every fixture is shared with fetch_test.go on purpose. Asserting the overview
// against a tree the dashboard is separately proven to write from is what makes
// "it did not write" evidence rather than an accident of a quiet fixture: the
// ghost tree used below carries both a recoverable anchor and a pending
// dashboard selection request, and TestRefreshAcceptsDaemonSnapshotFromTheSameKittySocket
// pins that the dashboard performs exactly two writes from it.
//
// Like the dashboard tests, these replace the StateQuery and LocalKittySocket
// package seams, so none of them may run in parallel and none of them can reach
// the operator's live daemon or name the operator's own Kitty instance.

func overviewOnce(t *testing.T, client kitty.KittyClient, seq, sourceWindowID int) any {
	t.Helper()
	return RefreshOverview(client, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, seq, sourceWindowID)()
}

func overviewOnceWith(t *testing.T, client kitty.KittyClient, cfg *config.Config, seq, sourceWindowID int) any {
	t.Helper()
	return RefreshOverview(client, cfg, seq, sourceWindowID)()
}

// TestRefreshOverviewPrefersDaemonSnapshotOverListingKitty is the daemon-first
// path seen from the overview: the same snapshot preference, the same rows, the
// same enrichment, and the same absence of a second `kitten @ ls` racing the
// daemon's.
func TestRefreshOverviewPrefersDaemonSnapshotOverListingKitty(t *testing.T) {
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
	// only pass if the daemon's copy is what was projected.
	client := &recordingKittyClient{windows: managedWindows(9, 99, "stale-direct")}

	msg, ok := overviewOnce(t, client, 3, 0).(SessionsMsg)
	if !ok {
		t.Fatalf("overview refresh produced %T, want SessionsMsg", msg)
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
	if client.userVarWrites() != 0 {
		t.Fatalf("the overview drove %d user-var writes, want 0", client.userVarWrites())
	}
}

// TestRefreshOverviewStaysReadOnlyOnAFreshSnapshot is the point of the command.
// The tree is the ghost tree, fresh and provably this Kitty's, which is the
// exact condition under which the dashboard performs both of its writes: it
// re-anchors the ghost tab and clears the pending cross-window selection
// request. The overview does neither, and it does not honor the request either —
// selecting a tab another window asked the DASHBOARD to select would steal that
// selection, and leaving the request behind while acting on it would let it be
// honored twice.
func TestRefreshOverviewStaysReadOnlyOnAFreshSnapshot(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg, ok := overviewOnceWith(t, client, ghostConfig(), 1, 0).(SessionsMsg)
	if !ok {
		t.Fatalf("overview refresh produced %T, want SessionsMsg", msg)
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].WindowID != 42 {
		t.Fatalf("sessions = %+v, want the ghost tab still rendered as window 42", msg.Sessions)
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("the overview drove %d user-var writes, want 0 — a recoverable anchor and a pending "+
			"selection request are the dashboard's to act on", client.userVarWrites())
	}
	if msg.SelectionTabID != 0 || msg.SelectionToken != "" {
		t.Fatalf("selection = tab %d token %q, want none: the pending request is addressed to the dashboard",
			msg.SelectionTabID, msg.SelectionToken)
	}
}

// TestRepeatedOverviewRefreshesStayReadOnly extends the rule from one refresh to
// a running overlay. Nothing accumulates across refreshes that would eventually
// let a write through, and a ready snapshot keeps costing zero Kitty
// subprocesses however long the surface stays open.
func TestRepeatedOverviewRefreshesStayReadOnly(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}
	cfg := ghostConfig()

	for seq := 1; seq <= 5; seq++ {
		if _, ok := overviewOnceWith(t, client, cfg, seq, 0).(SessionsMsg); !ok {
			t.Fatalf("overview refresh %d did not produce a session list", seq)
		}
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("five fresh overview refreshes drove %d user-var writes, want 0", client.userVarWrites())
	}
	if client.listCalls() != 0 {
		t.Fatalf("five overview refreshes ran %d Kitty listings, want 0", client.listCalls())
	}
}

// TestRefreshOverviewRendersStaleDaemonSnapshot pins that the shared freshness
// policy is shared rather than relaxed: a stale but attributed tree is still
// the better answer than no list, and it still costs no subprocess. The stale
// write deferral is invisible from here because this path had no writes to
// defer, which is exactly the intended shape.
func TestRefreshOverviewRendersStaleDaemonSnapshot(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(ghostTree(), true)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := overviewOnceWith(t, client, ghostConfig(), 1, 0).(SessionsMsg)
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0 for a stale-but-ready snapshot", client.listCalls())
	}
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 7 {
		t.Fatalf("sessions = %+v, want the stale snapshot still rendered", msg.Sessions)
	}
	if !msg.Topology.Stale || msg.Topology.Source != TopologyFromDaemon {
		t.Fatalf("topology = %+v, want a stale daemon source", msg.Topology)
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("stale overview refresh drove %d user-var writes, want 0", client.userVarWrites())
	}
	if msg.SelectionTabID != 0 || msg.SelectionToken != "" {
		t.Fatalf("selection = tab %d token %q, want none", msg.SelectionTabID, msg.SelectionToken)
	}
}

// TestRefreshOverviewFallsBackToDirectListing enumerates every reason the daemon
// cannot supply a topology. The overview takes the same four fallbacks the
// dashboard does — and takes them at the same cost, one listing, because the
// decision is made once in the shared reader rather than twice.
func TestRefreshOverviewFallsBackToDirectListing(t *testing.T) {
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

			msg, ok := overviewOnce(t, client, 5, 0).(SessionsMsg)
			if !ok {
				t.Fatalf("overview refresh produced %T, want SessionsMsg", msg)
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

// TestRefreshOverviewRejectsUnprovenInstanceIdentity is the attribution gate
// seen from the overview. A foreign tree and an unattributed one are refused
// alike — agreement has to be proven, not assumed — and the per-window state
// beside them is refused by the same proof, because bare window IDs name a
// window only inside the instance they came from and this fixture's foreign
// tree numbers a window 99 exactly as the local listing does.
//
// The overview cannot misdirect a write, having none to misdirect, but it can
// still paint another instance's icon onto a local row and forward that
// instance's maps to everything downstream. That is what is refused here.
func TestRefreshOverviewRejectsUnprovenInstanceIdentity(t *testing.T) {
	cases := []struct {
		name           string
		snapshotSocket string
		localSocket    string
	}{
		{
			name:           "snapshot belongs to another Kitty",
			snapshotSocket: foreignKittySocket,
			localSocket:    localKittySocket,
		},
		{
			name:           "daemon predates the socket field",
			snapshotSocket: "",
			localSocket:    localKittySocket,
		},
		{
			name:           "overview cannot name its own instance",
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
					// The unattributable tree numbers its own window 99 too,
					// which is why a bare window ID cannot carry state across
					// instances.
					KittySnapshot: snapshotFrom(tc.snapshotSocket, managedWindows(3, 99, "someone-elses-api")),
				}, nil
			})
			stubLocalKittySocket(t, tc.localSocket)
			client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

			msg, ok := overviewOnce(t, client, 1, 0).(SessionsMsg)
			if !ok {
				t.Fatalf("overview refresh produced %T, want SessionsMsg", msg)
			}
			if client.listCalls() != 1 {
				t.Fatalf("direct Kitty listings = %d, want exactly 1 for an unproven snapshot", client.listCalls())
			}
			if msg.Sessions[0].TabID != 9 {
				t.Fatalf("sessions = %+v, want this Kitty's directly listed tab 9", msg.Sessions)
			}
			assertNoDaemonMetadata(t, msg, 99)
			if client.userVarWrites() != 0 {
				t.Fatalf("the overview drove %d user-var writes, want 0", client.userVarWrites())
			}
		})
	}
}

// TestRefreshOverviewStampsDirectFallbackWithItsOwnCompletionTime pins that a
// listing this process just finished is reported as exactly that, rather than
// inheriting a capture time from a snapshot the reader declined.
func TestRefreshOverviewStampsDirectFallbackWithItsOwnCompletionTime(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	})
	client := &recordingKittyClient{windows: managedWindows(9, 99, "api")}

	before := time.Now()
	msg := overviewOnce(t, client, 1, 0).(SessionsMsg)
	after := time.Now()

	if msg.Topology.CapturedAt.Before(before) || msg.Topology.CapturedAt.After(after) {
		t.Fatalf("capture time = %v, want it inside [%v, %v]", msg.Topology.CapturedAt, before, after)
	}
	if msg.Topology.Age != 0 || msg.Topology.Generation != 0 {
		t.Fatalf("direct topology = %+v, want zero age and no generation", msg.Topology)
	}
}

// TestRefreshOverviewReportsASequenceBearingErrorWhenBothSourcesFail is the
// total failure case. It is an error rather than an empty successful list, so a
// consumer keeps whatever it was already showing, and it carries the sequence so
// the consumer's controller can still settle.
func TestRefreshOverviewReportsASequenceBearingErrorWhenBothSourcesFail(t *testing.T) {
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	})
	client := &recordingKittyClient{listErr: errors.New("kitten @ ls: signal: killed")}

	msg, ok := overviewOnce(t, client, 4, 0).(ErrMsg)
	if !ok {
		t.Fatalf("overview refresh produced %T, want ErrMsg", msg)
	}
	if msg.Seq != 4 {
		t.Fatalf("error sequence = %d, want the requested 4", msg.Seq)
	}
	if msg.Err == nil || msg.Error() == "" {
		t.Fatalf("error message = %+v, want the listing failure", msg)
	}
}

// TestRefreshOverviewResolvesASourcePaneInsideAManagedTab covers the one
// selection input the overview does accept: the pane it was launched from,
// resolved to its containing tab even when that pane is a non-anchor split
// rather than the managed window itself. The token stays empty because nothing
// was consumed to learn this.
func TestRefreshOverviewResolvesASourcePaneInsideAManagedTab(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	tree[0].Tabs[0].Windows = append(tree[0].Tabs[0].Windows, kitty.Window{ID: 99})
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(tree, false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := overviewOnce(t, client, 1, 99).(SessionsMsg)
	if msg.SelectionTabID != 7 {
		t.Fatalf("source tab = %d, want the pane's containing tab 7", msg.SelectionTabID)
	}
	if msg.SelectionToken != "" {
		t.Fatalf("selection token = %q, want empty: a source pane is not a request", msg.SelectionToken)
	}
	if client.listCalls() != 0 {
		t.Fatalf("direct Kitty listings = %d, want 0", client.listCalls())
	}
}

// TestRefreshOverviewSourcePaneOutranksAPendingDashboardRequest is the sharp
// edge of the two rules meeting. The dashboard, given this tree, would discard
// the source pane's tab and select tab 7 instead, because a pending request
// outranks it. The overview does the opposite: it reports the tab it was
// launched from and never sees the request at all, which is what keeps a
// selection meant for the dashboard from being answered by the wrong surface.
func TestRefreshOverviewSourcePaneOutranksAPendingDashboardRequest(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	tree[0].Tabs = append(tree[0].Tabs, kitty.Tab{
		ID:      70,
		Title:   "launcher",
		Windows: []kitty.Window{{ID: 700, UserVars: kitty.UserVars{DashboardSelect: "7:request-1"}}},
	})
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{OK: true, KittySnapshot: readySnapshot(tree, false)}, nil
	})
	stubLocalKittySocket(t, localKittySocket)
	client := &recordingKittyClient{}

	msg := overviewOnce(t, client, 1, 700).(SessionsMsg)
	if msg.SelectionTabID != 70 {
		t.Fatalf("selection tab = %d, want the source pane's own tab 70 rather than the requested 7",
			msg.SelectionTabID)
	}
	if msg.SelectionToken != "" {
		t.Fatalf("selection token = %q, want empty", msg.SelectionToken)
	}
	if client.userVarWrites() != 0 {
		t.Fatalf("the overview drove %d user-var writes, want 0 — the request stays pending for the dashboard",
			client.userVarWrites())
	}

	// The control: the very same tree, read by the dashboard, resolves the
	// request instead and clears it. The overview's answer is a different
	// policy, not a tree that happened to carry no request.
	dashboard := &recordingKittyClient{}
	dashboardMsg := Refresh(dashboard, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, "alpha", 1, 700)().(SessionsMsg)
	if dashboardMsg.SelectionTabID != 7 || dashboardMsg.SelectionToken != "request-1" {
		t.Fatalf("dashboard selection = tab %d token %q, want the requested tab 7 and its token",
			dashboardMsg.SelectionTabID, dashboardMsg.SelectionToken)
	}
	if dashboard.userVarWrites() != 1 {
		t.Fatalf("dashboard drove %d user-var writes, want the selection clear", dashboard.userVarWrites())
	}
}

// TestRefreshOverviewMatchesTheDashboardsSessionFacts is the sharing claim
// itself, asserted rather than assumed. Given one tree and one daemon answer,
// the two commands report the same rows with the same daemon-derived state and
// the same freshness — which is what lets a later surface consume the overview's
// message without re-deriving discovery policy.
func TestRefreshOverviewMatchesTheDashboardsSessionFacts(t *testing.T) {
	tree := managedWindows(7, 42, "api")
	response := func() (protocol.Response, error) {
		return protocol.Response{
			OK:            true,
			Activity:      map[int]int64{42: 1_700_000_500},
			Watchdog:      map[int]protocol.WatchdogState{42: {LastAction: "active", ThresholdSec: 600}},
			States:        map[int]protocol.WindowStateSnapshot{42: {IconState: "key", Looping: true}},
			KittySnapshot: readySnapshot(tree, false),
		}, nil
	}
	stubStateQuery(t, response)
	stubLocalKittySocket(t, localKittySocket)
	cfg := &config.Config{Defaults: config.Defaults{Sort: "alpha"}}

	dashboardMsg := Refresh(&recordingKittyClient{}, cfg, "alpha", 1, 0)().(SessionsMsg)
	overviewMsg := RefreshOverview(&recordingKittyClient{}, cfg, 1, 0)().(SessionsMsg)

	if len(overviewMsg.Sessions) != len(dashboardMsg.Sessions) {
		t.Fatalf("overview listed %d sessions, dashboard listed %d",
			len(overviewMsg.Sessions), len(dashboardMsg.Sessions))
	}
	for i := range dashboardMsg.Sessions {
		if !reflect.DeepEqual(overviewMsg.Sessions[i], dashboardMsg.Sessions[i]) {
			t.Fatalf("session %d differs:\n overview  = %+v\n dashboard = %+v",
				i, overviewMsg.Sessions[i], dashboardMsg.Sessions[i])
		}
	}
	if overviewMsg.Topology != dashboardMsg.Topology {
		t.Fatalf("topology differs:\n overview  = %+v\n dashboard = %+v",
			overviewMsg.Topology, dashboardMsg.Topology)
	}
	if overviewMsg.States[42] != dashboardMsg.States[42] ||
		overviewMsg.Watchdog[42] != dashboardMsg.Watchdog[42] ||
		!overviewMsg.Activity[42].Equal(dashboardMsg.Activity[42]) {
		t.Fatalf("forwarded daemon maps differ:\n overview  = %+v / %+v / %+v\n dashboard = %+v / %+v / %+v",
			overviewMsg.States, overviewMsg.Watchdog, overviewMsg.Activity,
			dashboardMsg.States, dashboardMsg.Watchdog, dashboardMsg.Activity)
	}
}
