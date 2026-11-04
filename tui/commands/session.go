package commands

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// ClearStatus asks the daemon to drop its guard via user_clear. The daemon
// renderer owns the resulting presentation update. Transport errors remain nonfatal while the daemon is
// restarting; the next refresh converges the state.
func ClearStatus(s session.RunningSession) tea.Cmd {
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
func MarkActive(s session.RunningSession) tea.Cmd {
	return MarkActiveWithDaemon(DaemonRequest, s)
}

func MarkActiveWithDaemon(request func(protocol.Request) (protocol.Response, error), s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Daemon: request}, actions.MarkActive, actions.Request{Session: &s})
		return ActionDoneMsg{Err: err}
	}
}

// Focus attaches to a tmux session by its local row ID.
func Focus(tabID int) tea.Cmd {
	if rs, ok := PortableSession(tabID); ok {
		return Attach(rs)
	}
	return func() tea.Msg { return AttachDoneMsg{Err: ErrNoTerminal} }
}

// CloseSessionRow closes a session through the backend that hosts it and
// reports exactly as Close does.
func CloseSessionRow(cfg *config.Config, rs session.RunningSession) tea.Cmd {
	if !IsPortable(rs) {
		return func() tea.Msg { return ActionDoneMsg{Err: ErrNoTerminal} }
	}
	if PortableLost(rs) {
		// Its server is gone: there is no agent to end, only a row to drop.
		return func() tea.Msg {
			dismissPortable(rs.WindowID)
			return ActionDoneMsg{}
		}
	}
	return func() tea.Msg {
		result, err := Registry.Execute(context.Background(), TerminalDependencies(cfg), actions.Close, actions.Request{Session: &rs, Confirmed: true})
		if err != nil {
			return ActionDoneMsg{Err: err}
		}
		return ActionDoneMsg{Unconfirmed: !actions.CloseConfirmed(result)}
	}
}

// Close closes a tmux session by its local row ID.
func Close(tabID int) tea.Cmd {
	if rs, ok := PortableSession(tabID); ok {
		return CloseSessionRow(nil, rs)
	}
	return func() tea.Msg { return ActionDoneMsg{Err: ErrNoTerminal} }
}

// Restart closes and relaunches a session and returns ActionDoneMsg.
func Restart(running session.RunningSession, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(cfg), actions.Restart, actions.Request{Session: &running, Confirmed: true})
		return ActionDoneMsg{Err: err}
	}
}

// Peek captures a tmux pane and shows its scrollback in this terminal.
func Peek(windowID int) tea.Cmd {
	if rs, ok := PortableSession(windowID); ok {
		return peekPortable(rs)
	}
	return func() tea.Msg { return PeekDoneMsg{Err: ErrNoTerminal} }
}

// ToggleLock flips the lock state on the session's anchor window. Locked
// sessions reject close/restart/override from the dashboard.
func ToggleLock(rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(nil), actions.ToggleLock, actions.Request{Session: &rs})
		return ActionDoneMsg{Err: err}
	}
}

// TogglePin pins the session above every other, or unpins it. The pin is the
// ★ in front of the title, which the dashboard, the workspace sidebar, and the
// session order all read.
func TogglePin(rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(nil), actions.TogglePin, actions.Request{Session: &rs})
		return ActionDoneMsg{Err: err}
	}
}

// ToggleMute flips the mute state on the session's anchor window. Muted
// sessions suppress macOS notifications across every notify path in the daemon.
// The daemon projects the new muted flag on the next refresh.
func ToggleMute(rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), TerminalDependencies(nil), actions.ToggleMute, actions.Request{Session: &rs})
		return ActionDoneMsg{Err: err}
	}
}
