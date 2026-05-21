package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

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

// fetchCmd queries Kitty for running sessions and the daemon for per-window
// last-activity timestamps. Daemon errors are non-fatal — when unreachable
// the activity map is nil and stateGroup treats every session as Active.
//
// Any "ghost" tabs surfaced by ListWithConfig (managed tabs whose anchor
// window vanished but whose title still resolves uniquely in cfg) are
// re-stamped with their user vars in the same tick. A failed re-stamp is
// silently retried on the next tick because the title fallback re-detects
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
		activity, watchdog := fetchDaemonState()
		return sessionsMsg{
			sessions: session.SortRunning(sessions, sortMode),
			activity: activity,
			watchdog: watchdog,
		}
	}
}

// fetchDaemonState queries the daemon once for both last-activity timestamps
// and the watchdog snapshot. Both maps are nil when the daemon is unreachable
// or the query fails — callers must tolerate.
func fetchDaemonState() (map[int]time.Time, map[int]daemon.WatchdogState) {
	resp, err := daemon.NewClient().Send(daemon.Request{Type: daemon.TypeStateQuery})
	if err != nil {
		return nil, nil
	}
	var activity map[int]time.Time
	if resp.Activity != nil {
		activity = make(map[int]time.Time, len(resp.Activity))
		for id, secs := range resp.Activity {
			activity[id] = time.Unix(secs, 0)
		}
	}
	return activity, resp.Watchdog
}

// clearStatusCmd wipes a session's tab-title icon (via Kitty) and asks the
// daemon to drop its guard via the user_clear action. The Kitty title write
// is the user-visible result; daemon errors are swallowed because the
// dashboard already updated the title directly.
func clearStatusCmd(client kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		newTitle := palette.BuildTitle(s.Base, "", s.Starred)
		err := client.SetTabTitle(kitty.MatchTabByWindowID(s.WindowID), newTitle)
		_, _ = daemon.NewClient().Send(daemon.Request{
			Type:         daemon.TypeStateUpdate,
			Action:       "user_clear",
			WindowID:     s.WindowID,
			SessionTitle: s.Base,
		})
		return actionDoneMsg{err: err}
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
