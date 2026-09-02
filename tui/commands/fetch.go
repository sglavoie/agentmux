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

// Fetch queries Kitty for session enumeration and the daemon for its
// authoritative per-window state. When the daemon is unreachable, the session
// list retains the state and loop markers parsed from live Kitty titles.
//
// Any "ghost" tabs surfaced by ListWithConfig (managed tabs whose anchor
// window vanished but whose title or CWD still resolves uniquely in cfg) are
// re-stamped with their user vars in the same tick. A failed re-stamp is
// silently retried on the next tick because fallback detection re-detects
// the same tab.
func Fetch(client kitty.KittyClient, cfg *config.Config, sortMode string) tea.Cmd {
	return InitialFetch(client, cfg, sortMode, 0)
}

// InitialFetch additionally resolves sourceWindowID to its containing Kitty
// tab. The resulting tab ID is consumed by the startup sessions update to
// select that tab's managed dashboard row, including when the source is a
// non-anchor pane in a split tab.
func InitialFetch(client kitty.KittyClient, cfg *config.Config, sortMode string, sourceWindowID int) tea.Cmd {
	return func() tea.Msg {
		osWindows, err := client.ListWindows()
		if err != nil {
			return ErrMsg{Err: err}
		}
		sessions, ghosts := session.ListFromOSWindowsWithConfig(osWindows, cfg)
		if len(ghosts) > 0 {
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
			_ = client.SetUserVars(kitty.MatchWindowByID(dashboardWindowID), map[string]string{
				kitty.DashboardSelectionVar: "",
			})
		}
		activity, watchdog, states := fetchDaemonState()
		if states != nil {
			ApplyDaemonStates(sessions, states)
		}
		return SessionsMsg{
			Sessions:       session.SortRunning(sessions, sortMode),
			Activity:       activity,
			Watchdog:       watchdog,
			States:         states,
			SelectionTabID: sourceTabID,
			SelectionToken: selectionToken,
		}
	}
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

// fetchDaemonState queries the daemon once for activity, watchdog, icon, and
// loop state. All maps are nil when the daemon is unreachable or rejects the
// query, which tells InitialFetch to retain its title-derived fallback values.
func fetchDaemonState() (map[int]time.Time, map[int]protocol.WatchdogState, map[int]protocol.WindowStateSnapshot) {
	resp, err := DaemonRequest(protocol.Request{Type: protocol.TypeStateQuery})
	if err != nil || !resp.OK {
		return nil, nil, nil
	}
	var activity map[int]time.Time
	if resp.Activity != nil {
		activity = make(map[int]time.Time, len(resp.Activity))
		for id, secs := range resp.Activity {
			activity[id] = time.Unix(secs, 0)
		}
	}
	return activity, resp.Watchdog, resp.States
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
