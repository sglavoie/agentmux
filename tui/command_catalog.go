package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// commandID is a stable, internal identity for a dashboard command. IDs are
// deliberately independent from titles so dynamic labels and copy edits do not
// disturb selection or tests.
type commandID string

const (
	commandFocusSession       commandID = "session.focus"
	commandPeekScrollback     commandID = "session.peek-scrollback"
	commandRestartSession     commandID = "session.restart"
	commandOverrideProfile    commandID = "session.override-profile"
	commandLaunchAnother      commandID = "session.launch-another"
	commandClearStatus        commandID = "session.clear-status"
	commandCloseSession       commandID = "session.close"
	commandOpenDirectory      commandID = "session.open-directory"
	commandOpenEditor         commandID = "session.open-editor"
	commandCopyTitle          commandID = "session.copy-title"
	commandToggleLock         commandID = "session.toggle-lock"
	commandToggleMute         commandID = "session.toggle-mute"
	commandSearchProject      commandID = "session.search-project-history"
	commandShowDetails        commandID = "session.show-details"
	commandShowTimeline       commandID = "session.show-timeline"
	commandFilterSessions     commandID = "explore.filter-sessions"
	commandCycleProfileFilter commandID = "explore.cycle-profile-filter"
	commandClearFilters       commandID = "explore.clear-filters"
	commandOpenLauncher       commandID = "explore.open-launcher"
	commandSearchAllSessions  commandID = "explore.search-session-history"
	commandSearchPlans        commandID = "explore.search-plans"
	commandManageWorktrees    commandID = "explore.manage-worktrees"
	commandShowRuns           commandID = "explore.show-runs"
	commandTogglePreview      commandID = "explore.toggle-preview"
	commandResortTabs         commandID = "app.resort-tabs"
	commandUndoClose          commandID = "app.undo-close"
	commandDismissDeprecation commandID = "app.dismiss-deprecation"
	commandShowKeyboardHelp   commandID = "app.show-keyboard-shortcuts"
	commandQuit               commandID = "app.quit"
)

// commandGroup is both the grouping identity and the user-facing group label.
type commandGroup string

const (
	commandGroupSession commandGroup = "Session"
	commandGroupExplore commandGroup = "Explore"
	commandGroupApp     commandGroup = "App"
)

// commandContext captures the dashboard row a command should target. The
// value is copied when the context is created so a background refresh or live
// cursor move cannot retarget a command before it executes.
type commandContext struct {
	target    session.RunningSession
	hasTarget bool
}

// captureCommandContext snapshots the current dashboard selection. It is safe
// for an empty list and for transiently invalid cursor values.
func captureCommandContext(m Model) commandContext {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return commandContext{}
	}
	return commandContext{target: m.filtered[m.cursor], hasTarget: true}
}

// commandAvailability carries both the controller decision and the inline
// reason rendered for disabled commands.
type commandAvailability struct {
	available bool
	reason    string
}

type commandTitle func(Model, commandContext) string
type commandVisibility func(Model, commandContext) bool
type commandAvailabilityFunc func(Model, commandContext) commandAvailability
type commandExecution func(Model, commandContext) (Model, tea.Cmd)

// command describes one entry in the dashboard command palette. shortcut is
// display-only: palette input never dispatches the dashboard key directly.
type command struct {
	id           commandID
	group        commandGroup
	title        commandTitle
	description  string
	aliases      []string
	shortcut     string
	visible      commandVisibility
	availability commandAvailabilityFunc
	execute      commandExecution
}

func staticCommandTitle(title string) commandTitle {
	return func(Model, commandContext) string { return title }
}

func (c command) displayTitle(m Model, ctx commandContext) string {
	return c.title(m, ctx)
}

func (c command) isVisible(m Model, ctx commandContext) bool {
	return c.visible == nil || c.visible(m, ctx)
}

func (c command) availabilityFor(m Model, ctx commandContext) commandAvailability {
	if c.availability == nil {
		return commandAvailability{available: true}
	}
	return c.availability(m, ctx)
}

// run applies the visibility and availability gates before execution. This is
// the single safe entry point for the palette controller: hidden and disabled
// commands are guaranteed to be no-ops.
func (c command) run(m Model, ctx commandContext) (Model, tea.Cmd) {
	if !c.isVisible(m, ctx) || !c.availabilityFor(m, ctx).available {
		return m, nil
	}
	return c.execute(m, ctx)
}

const (
	commandReasonNoSession       = "no selected session"
	commandReasonLocked          = "session is locked"
	commandReasonPeekInactive    = "session is not active"
	commandReasonStatusNotClear  = "session has no clearable status"
	commandReasonNoProject       = "project is not configured"
	commandReasonPreviewTooSmall = "terminal is too small for preview"
	commandReasonNothingToUndo   = "nothing to undo"
)

func availableCommand() commandAvailability {
	return commandAvailability{available: true}
}

func unavailableCommand(reason string) commandAvailability {
	return commandAvailability{reason: reason}
}

func sessionCommandAvailability(_ Model, ctx commandContext) commandAvailability {
	if !ctx.hasTarget {
		return unavailableCommand(commandReasonNoSession)
	}
	return availableCommand()
}

func unlockedSessionCommandAvailability(m Model, ctx commandContext) commandAvailability {
	if availability := sessionCommandAvailability(m, ctx); !availability.available {
		return availability
	}
	if ctx.target.Locked {
		return unavailableCommand(commandReasonLocked)
	}
	return availableCommand()
}

func peekCommandAvailability(m Model, ctx commandContext) commandAvailability {
	if availability := sessionCommandAvailability(m, ctx); !availability.available {
		return availability
	}
	if ctx.target.State != "bolt" {
		return unavailableCommand(commandReasonPeekInactive)
	}
	return availableCommand()
}

func clearStatusCommandAvailability(m Model, ctx commandContext) commandAvailability {
	if availability := sessionCommandAvailability(m, ctx); !availability.available {
		return availability
	}
	if dashboard.StateGroup(ctx.target, m.lastActivity, m.idleThreshold) != 0 {
		return unavailableCommand(commandReasonStatusNotClear)
	}
	return availableCommand()
}

func configuredProjectCommandAvailability(m Model, ctx commandContext) commandAvailability {
	if availability := sessionCommandAvailability(m, ctx); !availability.available {
		return availability
	}
	if _, ok := findConfigSession(m.cfg, ctx.target.EffectiveProfile(), ctx.target.EffectiveProject()); !ok {
		return unavailableCommand(commandReasonNoProject)
	}
	return availableCommand()
}

func previewCommandAvailability(m Model, _ commandContext) commandAvailability {
	// Hiding an already-enabled preview must remain possible even after a
	// terminal resize makes the viewport too small to enable it again.
	if m.previewEnabled {
		return availableCommand()
	}
	if m.cfg == nil {
		return unavailableCommand(commandReasonPreviewTooSmall)
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, dashboardPreviewEnableFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return unavailableCommand(commandReasonPreviewTooSmall)
	}
	return availableCommand()
}

func undoCommandAvailability(m Model, _ commandContext) commandAvailability {
	if len(m.activeUndoEntries()) == 0 {
		return unavailableCommand(commandReasonNothingToUndo)
	}
	return availableCommand()
}

// commandCatalog returns the complete dashboard command inventory in display
// order. It intentionally has no key-handler or renderer integration yet.
func openLauncherCommand(m Model, _ commandContext) (Model, tea.Cmd) {
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
