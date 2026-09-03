package commands

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// This file is one dashboard topology refresh: where the window tree comes
// from, what per-window state is overlaid on it, and what the refresh is
// allowed to write back into Kitty while it is there.
//
// The daemon is asked first. It already runs `kitten @ ls` on its own schedule
// and publishes the result as an immutable snapshot, so a dashboard that reads
// that snapshot costs one Unix-socket round trip instead of a second
// subprocess racing the daemon's. Listing Kitty from here is the fallback, not
// the normal path.

// Refresh produces one live-topology refresh carrying seq, which the model's
// refresh controller uses to recognize its own completion and discard an
// obsolete one.
//
// The window tree is taken from the daemon's cached snapshot whenever the
// optional block is present and ready — including when the daemon has already
// marked it stale, because a stale tree still describes the session list far
// better than no list at all. Direct Kitty I/O happens only when the daemon
// cannot supply one: the IPC call failed or was rejected, the daemon is new
// but has never completed a successful list (a cold block), or the daemon
// predates the block entirely and omits it.
//
// Per-window daemon state is applied to whichever topology won. A daemon that
// answered but had no usable snapshot still owns the icon, loop, and busy
// state of the windows a direct listing found, so its maps are overlaid on the
// fallback topology exactly as they would be on its own.
//
// sourceWindowID, when nonzero, is resolved to its containing Kitty tab so the
// startup update can select that tab's managed row, including when the source
// is a non-anchor pane in a split tab.
//
// Total failure — no daemon and no listing — returns a sequence-bearing ErrMsg
// rather than a SessionsMsg, so the model records the error without replacing
// the sessions it is already showing.
func Refresh(client kitty.KittyClient, cfg *config.Config, sortMode string, seq, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		osWindows, topology, daemon, err := refreshTopology(client)
		if err != nil {
			return ErrMsg{Seq: seq, Err: err}
		}
		// A stale snapshot may be rendered but may not drive writes back into
		// Kitty: every window ID in it is old enough that the window may
		// already be gone, and a re-anchor or a user-var clear aimed at a
		// vanished window is at best wasted and at worst aimed at its
		// replacement. Both writes are idempotent and their triggers are
		// re-detected on the next refresh, so skipping them costs a tick.
		mayWrite := !topology.Stale
		sessions, ghosts := session.ListFromOSWindowsWithConfig(osWindows, cfg)
		if mayWrite && len(ghosts) > 0 {
			session.ReanchorAll(client, ghosts, cfg.Defaults)
		}
		var sourceTabID int
		if sourceWindowID > 0 {
			if tab, _, found := kitty.FindTabByWindowID(osWindows, sourceWindowID); found {
				sourceTabID = tab.ID
			}
		}
		selectionToken := ""
		if requestedTabID, token, dashboardWindowID := dashboardSelectionRequest(osWindows); requestedTabID > 0 {
			sourceTabID = requestedTabID
			selectionToken = token
			if mayWrite {
				_ = client.SetUserVars(kitty.MatchWindowByID(dashboardWindowID), map[string]string{
					kitty.DashboardSelectionVar: "",
				})
			}
		}
		if daemon.States != nil {
			ApplyDaemonStates(sessions, daemon.States)
		}
		return SessionsMsg{
			Seq:            seq,
			Sessions:       session.SortRunning(sessions, sortMode),
			Activity:       daemon.Activity,
			Watchdog:       daemon.Watchdog,
			States:         daemon.States,
			SelectionTabID: sourceTabID,
			SelectionToken: selectionToken,
			Topology:       topology,
		}
	}
}

// daemonState is the per-window state one state query returned. All three maps
// are nil when the daemon is unreachable or rejected the query, which tells the
// refresh to retain the state and loop markers parsed from live Kitty titles.
type daemonState struct {
	Activity map[int]time.Time
	Watchdog map[int]protocol.WatchdogState
	States   map[int]protocol.WindowStateSnapshot
}

// refreshTopology resolves the window tree for one refresh, preferring the
// daemon's published snapshot and falling back to a direct listing. The daemon
// state it collected is returned either way: a reachable daemon with an
// unusable snapshot is still authoritative about per-window state.
func refreshTopology(client kitty.KittyClient) ([]kitty.OSWindow, TopologyFreshness, daemonState, error) {
	var state daemonState
	if resp, err := StateQuery(); err == nil {
		state = decodeDaemonState(resp)
		if snapshot := resp.KittySnapshot; snapshot != nil && snapshot.Ready {
			return snapshot.Windows, daemonTopologyFreshness(snapshot), state, nil
		}
	}
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil, TopologyFreshness{}, state, err
	}
	// A listing this process just completed is as fresh as topology gets, so
	// it is stamped with its own completion time rather than inheriting the
	// daemon's capture time or being reported as ageless.
	return osWindows, TopologyFreshness{Source: TopologyFromDirect, CapturedAt: time.Now()}, state, nil
}

// daemonTopologyFreshness projects the wire block onto the freshness the model
// renders from. Stale is carried across rather than recomputed from AgeMS so
// one deployment cannot hold two opinions about the same snapshot.
func daemonTopologyFreshness(snapshot *protocol.KittySnapshot) TopologyFreshness {
	freshness := TopologyFreshness{
		Source:     TopologyFromDaemon,
		Stale:      snapshot.Stale,
		Generation: snapshot.Generation,
		Age:        time.Duration(snapshot.AgeMS) * time.Millisecond,
	}
	if snapshot.CapturedAt > 0 {
		freshness.CapturedAt = time.Unix(snapshot.CapturedAt, 0)
	}
	return freshness
}

// dashboardSelectionRequest finds the pending cross-window selection request a
// launching pane left on itself, returning the tab it asks the dashboard to
// select plus the window that must have the request cleared.
func dashboardSelectionRequest(osWindows []kitty.OSWindow) (tabID int, token string, dashboardWindowID int) {
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			for _, window := range tab.Windows {
				request := window.UserVars.DashboardSelect
				if request == "" {
					continue
				}
				parts := strings.SplitN(request, ":", 2)
				if len(parts) != 2 || parts[1] == "" {
					continue
				}
				id, err := strconv.Atoi(parts[0])
				if err != nil || id <= 0 {
					continue
				}
				return id, parts[1], window.ID
			}
		}
	}
	return 0, "", 0
}

// decodeDaemonState converts one state-query response into the maps the
// dashboard overlays. The response has already been checked for OK by the
// client, so a value here is genuine state rather than a refusal.
func decodeDaemonState(resp protocol.Response) daemonState {
	state := daemonState{Watchdog: resp.Watchdog, States: resp.States}
	if resp.Activity != nil {
		state.Activity = make(map[int]time.Time, len(resp.Activity))
		for id, secs := range resp.Activity {
			state.Activity[id] = time.Unix(secs, 0)
		}
	}
	return state
}

// ApplyDaemonStates overlays daemon-owned state onto Kitty-enumerated rows.
// A reachable daemon is authoritative even for a freshly seen window without
// a state entry: its dashboard state is empty until the daemon observes it.
func ApplyDaemonStates(sessions []session.RunningSession, states map[int]protocol.WindowStateSnapshot) {
	for i := range sessions {
		state, ok := states[sessions[i].WindowID]
		if !ok {
			sessions[i].State = ""
			sessions[i].Looping = false
			sessions[i].BusySince = time.Time{}
			continue
		}
		sessions[i].State = state.IconState
		sessions[i].Looping = state.Looping
		sessions[i].BusySince = time.Time{}
		if state.BusySinceAt > 0 {
			sessions[i].BusySince = time.Unix(state.BusySinceAt, 0)
		}
	}
}
