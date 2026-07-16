package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
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
	commandClearFilters       commandID = "explore.clear-filters"
	commandOpenLauncher       commandID = "explore.open-launcher"
	commandSearchAllSessions  commandID = "explore.search-session-history"
	commandSearchPlans        commandID = "explore.search-plans"
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
	if stateGroup(ctx.target, m.lastActivity, m.idleThreshold) != 0 {
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
func commandCatalog() []command {
	return []command{
		{
			id: commandFocusSession, group: commandGroupSession,
			title:       staticCommandTitle("Focus Selected Session"),
			description: "Jump to the selected session's Kitty tab.",
			aliases:     []string{"jump", "tab"}, shortcut: "enter / space",
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, focusCmd(m.client, ctx.target.TabID)
			},
		},
		{
			id: commandPeekScrollback, group: commandGroupSession,
			title:       staticCommandTitle("Peek Scrollback"),
			description: "Open the active session's scrollback in an overlay pager.",
			aliases:     []string{"pager", "scrollback"}, shortcut: keyE,
			availability: peekCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, peekCmd(m.client, ctx.target.WindowID)
			},
		},
		{
			id: commandRestartSession, group: commandGroupSession,
			title:       staticCommandTitle("Restart Session"),
			description: "Confirm before closing and relaunching the selected session.",
			aliases:     []string{"relaunch"}, shortcut: keyR,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				m.confirmAction = "restart"
				m.confirmConsequence = restartConsequence
				m.confirmReturnMode = ModeList
				m.mode = ModeConfirm
				return m, nil
			},
		},
		{
			id: commandOverrideProfile, group: commandGroupSession,
			title:       staticCommandTitle("Override Profile"),
			description: "Restart the selected session under a different harness profile.",
			aliases:     []string{"account", "profile"}, shortcut: keyC,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.target.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerRestartProfile
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			id: commandLaunchAnother, group: commandGroupSession,
			title:       staticCommandTitle("Launch Another Session"),
			description: "Pick a profile and launch another session from the selected project.",
			aliases:     []string{"duplicate", "new"}, shortcut: keyDashboardLaunch,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = ctx.target.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, ctx.target.EffectiveProfile())
				m.configPickerPurpose = ConfigPickerDashboardLaunch
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			id: commandClearStatus, group: commandGroupSession,
			title:       staticCommandTitle("Clear Status Icon"),
			description: "Drop the attention, key, or stop badge from the selected tab.",
			aliases:     []string{"attention", "badge", "icon"}, shortcut: keyClearStatus,
			availability: clearStatusCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, m.clearStatusCmd(ctx.target)
			},
		},
		{
			id: commandCloseSession, group: commandGroupSession,
			title:       staticCommandTitle("Close Session"),
			description: "Exit the harness process while preserving its transcript.",
			aliases:     []string{"exit", "kill"}, shortcut: keyClose,
			availability: unlockedSessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.targetSession = ctx.target
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.confirmReturnMode = ModeList
					m.mode = ModeConfirm
					return m, nil
				}
				return m, m.closeWithToast(ctx.target)
			},
		},
		{
			id: commandOpenDirectory, group: commandGroupSession,
			title:       staticCommandTitle("Open Directory in Shell"),
			description: "Open a plain shell tab in the selected project's directory.",
			aliases:     []string{"directory", "shell", "terminal"}, shortcut: keyO,
			availability: configuredProjectCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, ctx.target.EffectiveProfile(), ctx.target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, openDirActionCmd(m, cs)
			},
		},
		{
			id: commandOpenEditor, group: commandGroupSession,
			title:       staticCommandTitle("Open in Editor"),
			description: "Open the selected project in the configured editor.",
			aliases:     []string{"directory", "edit"}, shortcut: keyOpenEditor,
			availability: configuredProjectCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, ctx.target.EffectiveProfile(), ctx.target.EffectiveProject())
				if !ok {
					return m, nil
				}
				m.targetSession = ctx.target
				m.actionErr = nil
				return m, openInEditorActionCmd(m, cs)
			},
		},
		{
			id: commandCopyTitle, group: commandGroupSession,
			title:       staticCommandTitle("Copy Session Title"),
			description: "Copy the selected session's display title to the clipboard.",
			aliases:     []string{"clipboard", "yank"}, shortcut: keyY,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, copyTitleCmd(runningSessionDisplayTitle(ctx.target))
			},
		},
		{
			id: commandToggleLock, group: commandGroupSession,
			title: func(_ Model, ctx commandContext) string {
				if ctx.hasTarget && ctx.target.Locked {
					return "Unlock Session"
				}
				return "Lock Session"
			},
			description: "Toggle protection from close, restart, and profile override.",
			aliases:     []string{"protect", "unlock"}, shortcut: keyLock,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, toggleLockCmd(m.client, ctx.target)
			},
		},
		{
			id: commandToggleMute, group: commandGroupSession,
			title: func(_ Model, ctx commandContext) string {
				if ctx.hasTarget && ctx.target.Muted {
					return "Unmute Session"
				}
				return "Mute Session"
			},
			description: "Toggle macOS notifications for the selected session.",
			aliases:     []string{"notifications", "unmute"}, shortcut: keyM,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				return m, toggleMuteCmd(m.client, ctx.target)
			},
		},
		{
			id: commandSearchProject, group: commandGroupSession,
			title:       staticCommandTitle("Search This Project's Past Sessions"),
			description: "Search transcript history filtered to the selected project.",
			aliases:     []string{"history", "project", "transcripts"}, shortcut: keyProjectSessionSearch,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearchForProject(ctx.target.EffectiveProject())
				return updated.(Model), cmd
			},
		},
		{
			id: commandShowDetails, group: commandGroupSession,
			title:       staticCommandTitle("Show Session Details"),
			description: "Open the selected session's hook event log.",
			aliases:     []string{"events", "hooks"}, shortcut: keyD,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.target.WindowID, false)
				return updated.(Model), cmd
			},
		},
		{
			id: commandShowTimeline, group: commandGroupSession,
			title:       staticCommandTitle("Show Session Timeline"),
			description: "Open the selected session's events grouped into timeline phases.",
			aliases:     []string{"events", "phases"}, shortcut: keyOpenTimeline,
			availability: sessionCommandAvailability,
			execute: func(m Model, ctx commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterDetailMode(ctx.target.WindowID, true)
				return updated.(Model), cmd
			},
		},

		{
			id: commandFilterSessions, group: commandGroupExplore,
			title:       staticCommandTitle("Filter Sessions"),
			description: "Filter running sessions with the dashboard query language.",
			aliases:     []string{"find", "search"}, shortcut: keySlash,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.enterFilter("")
				return m, nil
			},
		},
		{
			id: commandClearFilters, group: commandGroupExplore,
			title:       staticCommandTitle("Clear Filters"),
			description: "Remove every active dashboard filter chip.",
			aliases:     []string{"chips", "reset"}, shortcut: keyEsc,
			visible: func(m Model, _ commandContext) bool { return !m.activeFilter.IsEmpty() },
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.applyFilterChange(Filter{})
				return m, nil
			},
		},
		{
			id: commandOpenLauncher, group: commandGroupExplore,
			title:       staticCommandTitle("Open Launcher"),
			description: "Choose configured projects to launch.",
			aliases:     []string{"launch", "projects"}, shortcut: keyL,
			execute: openLauncherCommand,
		},
		{
			id: commandSearchAllSessions, group: commandGroupExplore,
			title:       staticCommandTitle("Search All Past Sessions"),
			description: "Search transcript history across every project and harness.",
			aliases:     []string{"history", "transcripts"}, shortcut: keySessionSearch,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterSessionSearch()
				return updated.(Model), cmd
			},
		},
		{
			id: commandSearchPlans, group: commandGroupExplore,
			title:       staticCommandTitle("Search Plans"),
			description: "Search indexed agent-session-loop plans.",
			aliases:     []string{"agent plans", "history"}, shortcut: keyPlanSearch,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.enterPlanSearch()
				return updated.(Model), cmd
			},
		},
		{
			id: commandTogglePreview, group: commandGroupExplore,
			title: func(m Model, _ commandContext) string {
				if m.previewEnabled {
					return "Hide Preview"
				}
				return "Show Preview"
			},
			description: "Toggle the selected session's live scrollback pane.",
			aliases:     []string{"hide", "pane", "scrollback"}, shortcut: keyTogglePreview,
			availability: previewCommandAvailability,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				updated, cmd := m.togglePreview()
				return updated.(Model), cmd
			},
		},

		{
			id: commandResortTabs, group: commandGroupApp,
			title:       staticCommandTitle("Resort Tabs"),
			description: "Reorder managed Kitty tabs to match the dashboard sort order.",
			aliases:     []string{"reorder", "tabs"}, shortcut: keyResort,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.actionErr = nil
				cmds := []tea.Cmd{resortCmd(m.client, m.sortMode)}
				if cmd := m.pushToast(Toast{
					Severity: toastInfo,
					Text:     "Resorting tabs…",
					Expires:  time.Now().Add(progressToastTTL),
				}); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			},
		},
		{
			id: commandUndoClose, group: commandGroupApp,
			title:       staticCommandTitle("Undo Last Close"),
			description: "Relaunch the most recently closed session before undo expires.",
			aliases:     []string{"restore", "reopen"}, shortcut: keyU,
			availability: undoCommandAvailability,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				return m, m.undoLastCmd()
			},
		},
		{
			id: commandDismissDeprecation, group: commandGroupApp,
			title:       staticCommandTitle("Dismiss Deprecation Warning"),
			description: "Hide the visible configuration deprecation banner.",
			aliases:     []string{"banner", "warning"}, shortcut: keyD,
			visible: func(m Model, _ commandContext) bool {
				return !m.deprecationDismissed && len(m.deprecations) > 0
			},
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.deprecationDismissed = true
				return m, nil
			},
		},
		{
			id: commandShowKeyboardHelp, group: commandGroupApp,
			title:       staticCommandTitle("Show Keyboard Shortcuts"),
			description: "Open the complete dashboard keyboard reference.",
			aliases:     []string{"help", "keys"}, shortcut: keyQuestion,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				m.helpOffset = 0
				m.mode = ModeHelp
				return m, nil
			},
		},
		{
			id: commandQuit, group: commandGroupApp,
			title:       staticCommandTitle("Quit"),
			description: "Exit the agentmux dashboard.",
			aliases:     []string{"close app", "exit"}, shortcut: keyQ + " / " + keyCtrlC,
			execute: func(m Model, _ commandContext) (Model, tea.Cmd) {
				return m, tea.Quit
			},
		},
	}
}

func openLauncherCommand(m Model, _ commandContext) (Model, tea.Cmd) {
	var sessions []config.Session
	if m.cfg != nil {
		sessions = m.cfg.Sessions
	}
	m.pickerSessions = sortPickerSessions(sessions)
	m.pickerRunning = buildPickerRunning(m.sessions)
	m.pickerSelected = make(map[int]bool)
	m.pickerProfileOverride = loadPickerOverrides(m.pickerSessions)
	m.pickerCursor = 0
	m.pickerFilter = ""
	m.pickerSearchMode = false
	m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
	m.mode = ModePicker
	return m, m.refreshPreviewForPickerCursor()
}
