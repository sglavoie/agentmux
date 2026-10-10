package commands

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// ClearStatus asks the daemon to drop its guard via user_clear. The daemon
// renderer owns the resulting title update, so this path never writes Kitty
// titles directly. Transport errors remain nonfatal while the daemon is
// restarting; the next refresh converges the state.
func ClearStatus(_ kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return ClearStatusWithDaemon(DaemonRequest, s)
}

func ClearStatusWithDaemon(request func(protocol.Request) (protocol.Response, error), s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Daemon: request}, actions.ClearStatus, actions.Request{Session: &s})
		return ActionDoneMsg{Err: err}
	}
}

// MarkActive asks the daemon to show ⚡ via user_active until the session's
// next real hook/ASL event. Like ClearStatus, the daemon renderer owns the
// title write and transport errors stay nonfatal.
func MarkActive(_ kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return MarkActiveWithDaemon(DaemonRequest, s)
}

func MarkActiveWithDaemon(request func(protocol.Request) (protocol.Response, error), s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Daemon: request}, actions.MarkActive, actions.Request{Session: &s})
		return ActionDoneMsg{Err: err}
	}
}

// Focus focuses a tab by ID and returns FocusDoneMsg.
//
// A portable session has no tab to focus: it is shown by attaching this
// terminal to it, and the attached view reports through AttachDoneMsg when the
// operator comes back.
func Focus(client kitty.KittyClient, tabID int) tea.Cmd {
	if rs, ok := PortableSession(tabID); ok {
		return Attach(rs)
	}
	return func() tea.Msg {
		target := session.RunningSession{TabID: tabID}
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Kitty: client}, actions.Focus, actions.Request{Session: &target})
		return FocusDoneMsg{Err: err}
	}
}

// CloseSessionRow closes a session through the backend that hosts it and
// reports exactly as Close does.
func CloseSessionRow(client kitty.KittyClient, cfg *config.Config, rs session.RunningSession) tea.Cmd {
	if !IsPortable(rs) {
		return Close(client, rs.TabID)
	}
	return func() tea.Msg {
		result, err := Registry.Execute(context.Background(), TerminalDependencies(client, cfg), actions.Close, actions.Request{Session: &rs, Confirmed: true})
		if err != nil {
			return ActionDoneMsg{Err: err}
		}
		return ActionDoneMsg{Unconfirmed: !actions.CloseConfirmed(result)}
	}
}

// Close closes a tab by ID and returns ActionDoneMsg.
//
// The completion reports whether the tab was observed to close, not merely
// whether Kitty accepted the request. A close that timed out or could not be
// checked comes back unconfirmed rather than successful, which is what keeps the
// dashboard from offering to undo a close it cannot show happened.
func Close(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		target := session.RunningSession{TabID: tabID}
		result, err := Registry.Execute(context.Background(), actions.Dependencies{Kitty: client}, actions.Close, actions.Request{Session: &target, Confirmed: true})
		if err != nil {
			return ActionDoneMsg{Err: err}
		}
		return ActionDoneMsg{Unconfirmed: !actions.CloseConfirmed(result)}
	}
}

// Restart closes and relaunches a session and returns ActionDoneMsg.
func Restart(client kitty.KittyClient, running session.RunningSession, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(client, cfg), actions.Restart, actions.Request{Session: &running, Confirmed: true})
		return ActionDoneMsg{Err: err}
	}
}

// Resort reorders managed Kitty tabs to match SortRunning's expected order and
// returns ResortDoneMsg.
func Resort(client kitty.KittyClient, sortMode string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			// Tab order is a Kitty presentation; there are no tabs to move.
			return ResortDoneMsg{}
		}
		return ResortDoneMsg{Err: session.Resort(client, sortMode)}
	}
}

// Peek launches a Kitty overlay pager for the given window's scrollback.
//
// A portable session, which Kitty cannot read, is captured through its
// backend and paged in this terminal instead.
func Peek(client kitty.KittyClient, windowID int) tea.Cmd {
	if rs, ok := PortableSession(windowID); ok {
		return peekPortable(rs)
	}
	if client == nil {
		return func() tea.Msg { return PeekDoneMsg{Err: ErrNoTerminal} }
	}
	return func() tea.Msg {
		return PeekDoneMsg{Err: client.LaunchOverlayPager(kitty.MatchWindowByID(windowID))}
	}
}

// ToggleLock flips the lock state on the session's anchor window. Locked
// sessions reject close/restart/override from the dashboard.
func ToggleLock(client kitty.KittyClient, rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(client, nil), actions.ToggleLock, actions.Request{Session: &rs})
		return ActionDoneMsg{Err: err}
	}
}

// ToggleMute flips the mute state on the session's anchor window. Muted
// sessions suppress macOS notifications across every notify path in the daemon.
// After flipping the user var, the tab title is rebuilt with the new muted flag
// so the 🔇 glyph appears immediately on the Kitty tab — without this push every
// downstream title-rewrite path only preserves a 🔇 already present and never
// seeds it.
func ToggleMute(client kitty.KittyClient, rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(client, nil), actions.ToggleMute, actions.Request{Session: &rs})
		return ActionDoneMsg{Err: err}
	}
}
