package commands

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/session"
)

// CopyTitle writes the given title to the system clipboard. Errors are
// reported via ActionDoneMsg so the dashboard surfaces them in the header.
func CopyTitle(title string) tea.Cmd {
	return func() tea.Msg {
		target := session.RunningSession{Project: title, Base: title}
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Clipboard: CopyToClipboard}, actions.CopyTitle, actions.Request{Session: &target})
		return ActionDoneMsg{Err: err}
	}
}

// CopyText writes arbitrary text to the system clipboard. It reports via
// CopyDoneMsg so full-layout viewers do not trigger a live-session refresh.
func CopyText(text string) tea.Cmd {
	return func() tea.Msg {
		return CopyDoneMsg{Err: CopyToClipboard(text)}
	}
}
