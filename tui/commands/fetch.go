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

// This file is one live-topology refresh: where the window tree comes from,
// what per-window state is overlaid on it, and what the refresh is allowed to
// write back into Kitty while it is there.
//
// Those are two questions, not one, and the file is split along that seam.
// readLiveSessions answers the first and touches Kitty only to read it;
// Refresh and RefreshOverview are the same read wearing two different write
// budgets, the dashboard's full one and the overview's empty one. Nothing that
// decides which snapshot is admissible, which rows a tree contains, or which
// daemon state may be trusted is written twice, so the two surfaces cannot
// disagree about the session universe they are both describing.
//
// The daemon is asked first. It already runs `kitten @ ls` on its own schedule
// and publishes the result as an immutable snapshot, so a dashboard that reads
// that snapshot costs one Unix-socket round trip instead of a second
// subprocess racing the daemon's. Listing Kitty from here is the fallback, not
// the normal path.

// liveSessions is one read of the live-session universe, and nothing that was
// done about it. It holds the window tree a refresh resolved, how fresh that
// tree is, the daemon's per-window state where the instance was proven, the
// managed rows projected out of the tree with that state already overlaid, the
// ghost tabs whose anchors no longer exist, and the tab containing the pane the
// refresh was launched from.
//
// Ghosts are reported rather than repaired here, and the raw OSWindows are
// carried alongside the rows, because both of the writes a refresh can make are
// derived from them and only one caller is allowed to make those writes. That
// is the whole point of the split: a reader that cannot write is the thing two
// surfaces can share, whereas the repairs are the dashboard's alone.
type liveSessions struct {
	OSWindows    []kitty.OSWindow
	Topology     TopologyFreshness
	Daemon       daemonState
	Sessions     []session.RunningSession
	Ghosts       []session.GhostTab
	SourceTabID  int
	HostWindowID int
	HostTabID    int
	// PortableErr is set when the portable rows are kept from an earlier
	// listing rather than read now.
	PortableErr error
}

// readLiveSessions resolves one refresh's view of the live sessions and touches
// Kitty exactly once, to list it, and only when the daemon could not supply a
// tree. It is the single place the retrieval policy lives, so a second surface
// reading the same universe cannot drift from the dashboard's answer about
// which daemon snapshots are admissible or which rows a tree contains.
//
// The window tree is taken from the daemon's cached snapshot whenever the
// optional block is present, ready, and positively attributed to this process's
// own Kitty instance — including when the daemon has already marked it stale,
// because a stale tree still describes the session list far better than no list
// at all. Direct Kitty I/O happens only when the daemon cannot supply one: the
// IPC call failed or was rejected, the daemon is new but has never completed a
// successful list (a cold block), the daemon predates the block entirely and
// omits it, or the snapshot is not provably this process's — either side
// unidentified, or the two identities known to differ.
//
// The daemon's per-window state travels with its tree rather than separately:
// its icon, loop, busy, activity, and watchdog maps are consumed only when the
// same instance attribution that admits the tree holds. Those maps are keyed by
// bare Kitty window IDs, which name a window only inside the instance they were
// read from, so a daemon that cannot prove its IDs are ours cannot be allowed to
// describe our rows either.
//
// sourceWindowID, when nonzero, is resolved to its containing Kitty tab, so the
// caller can select that tab's managed row including when the source is a
// non-anchor pane in a split tab.
//
// Total failure — no daemon and no listing — is an error rather than an empty
// success, so a caller records it without replacing the sessions it is already
// showing.
//
// Portable sessions join the same universe here. They are discovered through
// their own backend, never through Kitty, and a surface started without a
// Kitty client lists them alone: there, a failed discovery with nothing listed
// before it is the total failure. Once sessions have been listed, a backend
// that fails or disappears keeps them on the list, with or without Kitty, and
// the refresh carries the outage beside them (PortableErr).
func readLiveSessions(client kitty.KittyClient, cfg *config.Config, sourceWindowID int) (liveSessions, error) {
	osWindows, topology, daemon, resp, err := refreshTopology(client)
	if err != nil {
		return liveSessions{}, err
	}
	live := liveSessions{OSWindows: osWindows, Topology: topology, Daemon: daemon}
	live.Sessions, live.Ghosts = session.ListFromOSWindowsWithConfig(osWindows, cfg)
	if daemon.States != nil {
		ApplyDaemonStates(live.Sessions, daemon.States)
	}
	portable, outage := portableSessions(cfg)
	if outage != nil {
		if client == nil && len(portable) == 0 {
			return liveSessions{}, outage.Err
		}
		live.PortableErr = outage
	}
	// A lost session has no agent for the daemon to describe.
	if outage == nil || outage.Lost == 0 {
		overlayPortableState(portable, resp, &live.Daemon)
	}
	live.Sessions = append(live.Sessions, portable...)
	if sourceWindowID > 0 {
		if tab, _, found := kitty.FindTabByWindowID(osWindows, sourceWindowID); found {
			live.SourceTabID = tab.ID
		}
	}
	// The hosting tab is resolved from the same tree, on every refresh, rather
	// than once at startup. The window this process runs in never changes, but
	// the tab containing it does: a pane moved to another tab, or a tab closed
	// out from under a detached window, would leave a value cached at startup
	// naming a tab this surface no longer lives in.
	if live.HostWindowID = LocalWindowID(); live.HostWindowID > 0 {
		if tab, _, found := kitty.FindTabByWindowID(osWindows, live.HostWindowID); found {
			live.HostTabID = tab.ID
		}
	}
	return live, nil
}

// Refresh produces one live-topology refresh carrying seq, which the model's
// refresh controller uses to recognize its own completion and discard an
// obsolete one.
//
// Retrieval, attribution, and projection are readLiveSessions'; what is added
// here is everything the dashboard is uniquely allowed to do with the result.
// It re-anchors the ghost tabs the projection found, and it both honors and
// clears the cross-window selection request a launching pane left on itself —
// writes no other reader of the same tree may make, because they repair state
// the dashboard owns and consume a request addressed to the dashboard alone.
//
// Total failure returns a sequence-bearing ErrMsg rather than a SessionsMsg, so
// the model records the error without replacing the sessions it is showing.
func Refresh(client kitty.KittyClient, cfg *config.Config, sortMode string, seq, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		live, err := readLiveSessions(client, cfg, sourceWindowID)
		if err != nil {
			return ErrMsg{Seq: seq, Err: err}
		}
		// A stale snapshot may be rendered but may not drive writes back into
		// Kitty: every window ID in it is old enough that the window may
		// already be gone, and a re-anchor or a user-var clear aimed at a
		// vanished window is at best wasted and at worst aimed at its
		// replacement. Both writes are idempotent and their triggers are
		// re-detected on the next refresh, so skipping them costs a tick.
		mayWrite := !live.Topology.Stale
		if mayWrite && client != nil && len(live.Ghosts) > 0 {
			session.ReanchorAll(kitty.NewBackend(client), live.Ghosts, cfg.Defaults)
		}
		sourceTabID := live.SourceTabID
		selectionToken := ""
		if requestedTabID, token, dashboardWindowID := dashboardSelectionRequest(live.OSWindows); requestedTabID > 0 {
			sourceTabID = requestedTabID
			selectionToken = token
			if mayWrite && client != nil {
				_ = client.SetUserVars(kitty.MatchWindowByID(dashboardWindowID), map[string]string{
					kitty.DashboardSelectionVar: "",
				})
			}
		}
		return SessionsMsg{
			Seq:            seq,
			Sessions:       session.SortRunning(live.Sessions, sortMode),
			Activity:       live.Daemon.Activity,
			Watchdog:       live.Daemon.Watchdog,
			States:         live.Daemon.States,
			SelectionTabID: sourceTabID,
			SelectionToken: selectionToken,
			Topology:       live.Topology,
			HostWindowID:   live.HostWindowID,
			HostTabID:      live.HostTabID,
			PortableErr:    live.PortableErr,
		}
	}
}

// RefreshOverview produces the same session facts Refresh does and writes
// nothing. It is Refresh's reader half addressed to a surface that observes the
// dashboard's universe without owning it, and it reports through the same
// SessionsMsg and sequence-bearing ErrMsg so a consumer needs no second
// vocabulary for the same answer.
//
// Both writes Refresh can make — the ghost re-anchor and the selection-var
// clear — are absent by construction rather than behind a flag: the client is
// used for at most one ListWindows and never for a mutation, so there is no
// path here that re-anchors a tab, sets a user variable, focuses a window, or
// acknowledges a notification. It likewise starts none of the history or
// agent-session-loop discovery the dashboard's own startup runs, because none
// of that is part of reading the live-session universe.
//
// The cross-window selection request is not merely left unconsumed, it is not
// read at all. That request is addressed to the dashboard — it names the tab the
// dashboard should select and is cleared by whoever acts on it — so an overview
// that honored it would steal a selection intended for another window and then
// leave the request behind to be honored a second time. SelectionTabID therefore
// comes from sourceWindowID alone, and SelectionToken is always empty: there is
// no token because there is no request to acknowledge.
//
// Freshness policy is shared rather than relaxed. A stale but attributed daemon
// snapshot is still displayable here, exactly as on the dashboard, and the
// stale rule costs this path nothing further because it had no writes to defer.
//
// Sorting is deliberately not applied: the rows come back in discovery order and
// the overview's own ordering is its caller's to choose.
func RefreshOverview(client kitty.KittyClient, cfg *config.Config, seq, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		live, err := readLiveSessions(client, cfg, sourceWindowID)
		if err != nil {
			return ErrMsg{Seq: seq, Err: err}
		}
		return SessionsMsg{
			Seq:            seq,
			Sessions:       live.Sessions,
			Activity:       live.Daemon.Activity,
			Watchdog:       live.Daemon.Watchdog,
			States:         live.Daemon.States,
			SelectionTabID: live.SourceTabID,
			Topology:       live.Topology,
			HostWindowID:   live.HostWindowID,
			HostTabID:      live.HostTabID,
			PortableErr:    live.PortableErr,
		}
	}
}

// daemonState is the per-window state one state query returned. All three maps
// are nil when the daemon is unreachable, rejected the query, or could not prove
// that the window IDs keying them belong to this dashboard's Kitty instance,
// which tells the refresh to retain the state and loop markers parsed from live
// Kitty titles.
type daemonState struct {
	Activity map[int]time.Time
	Watchdog map[int]protocol.WatchdogState
	States   map[int]protocol.WindowStateSnapshot
}

// refreshTopology resolves the window tree for one refresh, preferring the
// daemon's published snapshot and falling back to a direct listing.
//
// The daemon's per-window state is collected only behind the same instance
// proof that admits its tree, and is therefore returned empty on every fallback
// a real daemon can produce. Readiness is the only thing the two decisions
// disagree about: a daemon that has proven the instance but has no tree yet
// still owns the state of the windows a direct listing finds, while a daemon
// that has proven nothing owns neither.
//
// The response itself is returned beside them, unfiltered, for the one reader
// that needs no instance proof: portable sessions are matched by runtime
// session, never by window ID. A surface with no Kitty client has no window
// tree to resolve and reports an empty, fresh one.
func refreshTopology(client kitty.KittyClient) ([]kitty.OSWindow, TopologyFreshness, daemonState, *protocol.Response, error) {
	var state daemonState
	var answered *protocol.Response
	if resp, err := StateQuery(); err == nil {
		answered = &resp
		if client != nil && snapshotDescribesLocalKitty(resp.KittySnapshot) {
			state = decodeDaemonState(resp)
			if snapshot := resp.KittySnapshot; snapshot.Ready {
				return snapshot.Windows, daemonTopologyFreshness(snapshot), state, answered, nil
			}
		}
	}
	if client == nil {
		return nil, TopologyFreshness{Source: TopologyFromDirect, CapturedAt: time.Now()}, state, answered, nil
	}
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil, TopologyFreshness{}, state, answered, err
	}
	// A listing this process just completed is as fresh as topology gets, so
	// it is stamped with its own completion time rather than inheriting the
	// daemon's capture time or being reported as ageless.
	return osWindows, TopologyFreshness{Source: TopologyFromDirect, CapturedAt: time.Now()}, state, answered, nil
}

// snapshotDescribesLocalKitty reports whether a daemon snapshot is positively
// attributed to the Kitty instance this dashboard is running inside. It is the
// single gate on everything one state query can contribute: the window tree it
// carries and the per-window state, activity, and watchdog maps beside it.
//
// Kitty window IDs are unique only within one instance, so a tree from another
// instance is not merely out of date — it is a different world whose IDs happen
// to look like ours. Rendering it would list somebody else's sessions, and the
// re-anchor and selection-clear writes derived from it would be aimed at
// whatever windows happen to hold those IDs here. The daemon retargets its
// effective socket the moment a hook arrives from a second Kitty, so a snapshot
// published against the first one can still be returned alongside the second
// one's per-window state; the socket recorded on the block is what makes the two
// distinguishable.
//
// The maps are gated by the very same proof because they are keyed by nothing
// but those window IDs, and the daemon's store spans every instance it has ever
// been hooked from. Rejecting the tree while keeping them would let a foreign
// window's icon, loop marker, busy anchor, and watchdog threshold land on the
// local row that happens to share its number, and be forwarded onward in the
// refresh message — the same cross-instance confusion the tree gate exists to
// stop, arriving through the door beside it. A dashboard that cannot prove the
// instance therefore falls back to the state and loop markers parsed from live
// Kitty titles, which is what it already does when no daemon answers at all.
//
// Agreement therefore has to be proven, not assumed: both identities must be
// present and equal. An empty socket on either side is an ABSENCE of provenance,
// never evidence of a match — an old daemon omits the field, a daemon reading
// through no reusable endpoint has nothing to report, and a dashboard outside
// Kitty or behind an unnormalizable address cannot name its own instance. Each
// of those is a tree that might belong to any instance at all, which is exactly
// the hazard this gate exists to stop. The cost of demanding proof is bounded
// and visible — such a dashboard runs its own `kitten @ ls` per refresh, the
// documented fallback — while the cost of assuming it is a foreign tree
// rendered as ours and written back into our windows.
//
// A nil block is one more shape of the same absence — a daemon too old to carry
// the field says nothing about which Kitty it watches — so it is refused here
// rather than at each call site.
func snapshotDescribesLocalKitty(snapshot *protocol.KittySnapshot) bool {
	if snapshot == nil {
		return false
	}
	local := LocalKittySocket()
	return local != "" && snapshot.Socket == local
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
// Unseen and PresentationChangedAt join State/Looping/BusySince in both
// directions, including the reset in the no-entry branch: an unobserved window
// has no authority for either fact. Their zero values are quiet: they can only
// demote a row out of Needs Attention or leave its recency unknown, never pin
// one there on the strength of a stale overlay.
// Reason joins them on the same argument. A reason only ever explains the icon
// the same entry supplied, so carrying one forward past a window the daemon no
// longer has an entry for would gloss a row with the cause of a prompt that is
// no longer on screen — and empty is what the dashboard already renders for
// every blocked row nothing has labelled.
// Branch and Dirty join them too, and the reset matters more for them than for
// any of the others: they describe a checkout on disk rather than a fact about
// the session, so a branch carried forward past the entry that supplied it would
// keep naming a directory nobody has looked at since — and would go on naming it
// for as long as the row stayed on screen. An empty branch is not reserved as a
// column at all, so dropping the pair costs the row nothing it can see.
func ApplyDaemonStates(sessions []session.RunningSession, states map[int]protocol.WindowStateSnapshot) {
	for i := range sessions {
		state, ok := states[sessions[i].WindowID]
		if !ok {
			sessions[i].State = ""
			sessions[i].AgentTurn = nil
			sessions[i].ASLPhases = nil
			sessions[i].Looping = false
			sessions[i].BusySince = time.Time{}
			sessions[i].PresentationChangedAt = time.Time{}
			sessions[i].Unseen = false
			sessions[i].Reason = ""
			sessions[i].Branch = ""
			sessions[i].Dirty = false
			continue
		}
		sessions[i].State = state.IconState
		sessions[i].AgentTurn = nil
		if state.AgentTurn != nil {
			turn := *state.AgentTurn
			sessions[i].AgentTurn = &turn
		}
		sessions[i].ASLPhases = append([]string(nil), state.ASLPhases...)
		sessions[i].Looping = state.Looping
		sessions[i].Unseen = state.Unseen
		sessions[i].Reason = state.Reason
		sessions[i].Branch = state.Branch
		sessions[i].Dirty = state.Dirty
		sessions[i].BusySince = time.Time{}
		if state.BusySinceAt > 0 {
			sessions[i].BusySince = time.Unix(state.BusySinceAt, 0)
		}
		sessions[i].PresentationChangedAt = time.Time{}
		if state.PresentationChangedAtMS > 0 {
			sessions[i].PresentationChangedAt = time.UnixMilli(state.PresentationChangedAtMS)
		}
	}
}
