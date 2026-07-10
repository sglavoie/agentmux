package tui

import (
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

var spawnDaemonDetached = daemon.SpawnDetached

// daemonRequest is a narrow seam around the IPC client so fetch and clear
// behavior can be tested without connecting to an operator's daemon.
var daemonRequest = func(req daemon.Request) (daemon.Response, error) {
	return daemon.NewClient().Send(req)
}

// healthCmd refreshes the daemon and/or hook health cached on the model.
// Each section is only re-queried when the corresponding flag is set so
// the cheap daemon ping doesn't pull in the more expensive hooks scan on
// every tick. Errors are non-fatal — they collapse into the "degraded"
// pip rendering rather than tearing down the dashboard.
func healthCmd(cfg *config.Config, amxBin string, checkDaemon, checkHooks bool) tea.Cmd {
	return func() tea.Msg {
		msg := healthMsg{daemonChecked: checkDaemon, hookChecked: checkHooks}
		if checkDaemon {
			h := daemon.NewClient().Health()
			msg.daemonAlive = h.Alive
			if !h.Alive {
				if err := spawnDaemonDetached(amxBin); err != nil {
					slog.Debug("tui: starting daemon", "err", err)
				}
			}
		}
		if checkHooks {
			summary, err := hooks.OverallHookHealth(cfg, amxBin)
			if err != nil {
				msg.hookErr = err
			} else {
				msg.hookSummary = HookHealth{
					InSync:  summary.InSync,
					Drifted: summary.Drifted,
					Missing: summary.Missing,
				}
			}
		}
		return msg
	}
}

// fetchCmd queries Kitty for session enumeration and the daemon for its
// authoritative per-window state. When the daemon is unreachable, the session
// list retains the state and loop markers parsed from live Kitty titles.
//
// Any "ghost" tabs surfaced by ListWithConfig (managed tabs whose anchor
// window vanished but whose title or CWD still resolves uniquely in cfg) are
// re-stamped with their user vars in the same tick. A failed re-stamp is
// silently retried on the next tick because fallback detection re-detects
// the same tab.
func fetchCmd(client kitty.KittyClient, cfg *config.Config, sortMode string) tea.Cmd {
	return func() tea.Msg {
		sessions, ghosts, err := session.ListWithConfig(client, cfg)
		if err != nil {
			return errMsg{err: err}
		}
		if len(ghosts) > 0 {
			session.ReanchorAll(client, ghosts, cfg.Defaults)
		}
		activity, watchdog, states := fetchDaemonState()
		if states != nil {
			applyDaemonStates(sessions, states)
		}
		return sessionsMsg{
			sessions: session.SortRunning(sessions, sortMode),
			activity: activity,
			watchdog: watchdog,
		}
	}
}

// fetchDaemonState queries the daemon once for activity, watchdog, icon, and
// loop state. All maps are nil when the daemon is unreachable or rejects the
// query, which tells fetchCmd to retain its title-derived fallback values.
func fetchDaemonState() (map[int]time.Time, map[int]daemon.WatchdogState, map[int]daemon.WindowStateSnapshot) {
	resp, err := daemonRequest(daemon.Request{Type: daemon.TypeStateQuery})
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

// applyDaemonStates overlays daemon-owned state onto Kitty-enumerated rows.
// A reachable daemon is authoritative even for a freshly seen window without
// a state entry: its dashboard state is empty until the daemon observes it.
func applyDaemonStates(sessions []session.RunningSession, states map[int]daemon.WindowStateSnapshot) {
	for i := range sessions {
		state, ok := states[sessions[i].WindowID]
		if !ok {
			sessions[i].State = ""
			sessions[i].Looping = false
			continue
		}
		sessions[i].State = state.IconState
		sessions[i].Looping = state.Looping
	}
}

// clearStatusCmd asks the daemon to drop its guard via user_clear. The daemon
// renderer owns the resulting title update, so this path never writes Kitty
// titles directly. Transport errors remain nonfatal while the daemon is
// restarting; the next refresh converges the state.
func clearStatusCmd(_ kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, _ = daemonRequest(daemon.Request{
			Type:         daemon.TypeStateUpdate,
			Action:       "user_clear",
			WindowID:     s.WindowID,
			SessionTitle: s.Base,
		})
		return actionDoneMsg{}
	}
}

// focusCmd focuses a tab by ID and returns focusDoneMsg.
func focusCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{err: client.FocusTab(kitty.MatchTabByID(tabID))}
	}
}

// closeCmd closes a tab by ID and returns actionDoneMsg.
func closeCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Close(client, tabID)}
	}
}

// restartCmd closes and relaunches a session and returns actionDoneMsg.
func restartCmd(client kitty.KittyClient, running session.RunningSession, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Restart(client, running, cfg)}
	}
}

// resortCmd reorders managed Kitty tabs to match SortRunning's expected
// order and returns resortDoneMsg.
func resortCmd(client kitty.KittyClient, sortMode string) tea.Cmd {
	return func() tea.Msg {
		return resortDoneMsg{err: session.Resort(client, sortMode)}
	}
}

// peekCmd launches a Kitty overlay pager for the given window's scrollback.
func peekCmd(client kitty.KittyClient, windowID int) tea.Cmd {
	return func() tea.Msg {
		return peekDoneMsg{err: client.LaunchOverlayPager(kitty.MatchWindowByID(windowID))}
	}
}
