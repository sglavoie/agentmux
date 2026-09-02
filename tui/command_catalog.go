package tui

// This file is the seam between the root Bubble Tea model and the command
// vocabulary in tui/commands. The command type, its identities, its groups, its
// availability verdicts, and the model-free gates all live in the leaf; what
// remains here is what a verdict rests on in *this* model — the selection
// snapshot, the configured-project lookup, the notification guard, the preview
// fit, and the undo stack — plus the one entry whose execution is pure model
// mutation.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// captureCommandContext snapshots the current dashboard selection. It is safe
// for an empty list and for transiently invalid cursor values.
func captureCommandContext(m Model) commands.Context {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return commands.Context{}
	}
	return commands.Context{Target: m.filtered[m.cursor], HasTarget: true}
}

func sessionCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.SessionAvailability(ctx)
}

func unlockedSessionCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.UnlockedSessionAvailability(ctx)
}

func peekCommandAvailability(_ Model, ctx commands.Context) commands.Availability {
	return commands.PeekAvailability(ctx)
}

func configuredProjectCommandAvailability(m Model, ctx commands.Context) commands.Availability {
	return commands.ConfiguredProjectAvailability(m.cfg, ctx)
}

func clearStatusCommandAvailability(m Model, ctx commands.Context) commands.Availability {
	if availability := commands.SessionAvailability(ctx); !availability.Available {
		return availability
	}
	if dashboard.StateGroup(ctx.Target, m.lastActivity, m.idleThreshold) != 0 {
		return commands.Unavailable(commands.ReasonStatusNotClear)
	}
	return commands.Available()
}

func previewCommandAvailability(m Model, _ commands.Context) commands.Availability {
	// Hiding an already-enabled preview must remain possible even after a
	// terminal resize makes the viewport too small to enable it again.
	if m.previewEnabled {
		return commands.Available()
	}
	if m.cfg == nil {
		return commands.Unavailable(commands.ReasonPreviewTooSmall)
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, dashboardPreviewEnableFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return commands.Unavailable(commands.ReasonPreviewTooSmall)
	}
	return commands.Available()
}

func undoCommandAvailability(m Model, _ commands.Context) commands.Availability {
	if len(m.activeUndoEntries()) == 0 {
		return commands.Unavailable(commands.ReasonNothingToUndo)
	}
	return commands.Available()
}

// openLauncherCommand is pure model mutation, so it stays with the root rather
// than moving into the leaf alongside the vocabulary it is registered under.
func openLauncherCommand(m Model, _ commands.Context) (Model, tea.Cmd) {
	var sessions []config.Session
	if m.cfg != nil {
		sessions = m.cfg.Sessions
	}
	m.pickerSessions = sortPickerSessions(sessions)
	m.pickerRunning = buildPickerRunning(m.sessions)
	m.pickerSelected = make(map[int]bool)
	m.pickerProfileOverride, m.pickerPinned = loadPickerState(m.pickerSessions)
	m.pickerCursor = 0
	m.pickerFilter = ""
	m.pickerProfileFilter = ""
	m.pickerSearchMode = false
	m.pickerFilteredIdx = m.pickerFilteredIndices("")
	m.mode = ModePicker
	return m, m.refreshPreviewForPickerCursor()
}
