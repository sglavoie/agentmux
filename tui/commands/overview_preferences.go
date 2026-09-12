package commands

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// OverviewPreferencesMsg completes one read or write of the daemon's view setting.
type OverviewPreferencesMsg struct {
	Read    bool
	Compact bool
	Err     error
}

// A nil value reads; a non-nil value sets the preference. Send is bounded and
// never starts a daemon: this setting belongs to the currently running daemon.
func OverviewPreferences(compact *bool) tea.Cmd {
	req := protocol.Request{Type: protocol.TypeOverviewPreferences}
	if compact != nil {
		value := *compact
		req.OverviewCompact = &value
	}
	return func() tea.Msg {
		resp, err := ipc.NewClient().Send(req)
		if err == nil && !resp.OK {
			err = errors.New(resp.Error)
		}
		if err == nil && resp.OverviewCompact == nil {
			err = errors.New("daemon does not support overview preferences")
		}
		msg := OverviewPreferencesMsg{Read: compact == nil, Err: err}
		if resp.OverviewCompact != nil {
			msg.Compact = *resp.OverviewCompact
		}
		return msg
	}
}
