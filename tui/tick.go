package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// tickCmd schedules a single tickMsg after d.
func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
