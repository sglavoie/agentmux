package commands

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// ID is a stable, internal identity for a dashboard command. IDs are
// deliberately independent from titles so dynamic labels and copy edits do not
// disturb selection or tests.
type ID string

const (
	FocusSession       ID = "session.focus"
	PeekScrollback     ID = "session.peek-scrollback"
	RestartSession     ID = "session.restart"
	OverrideProfile    ID = "session.override-profile"
	LaunchAnother      ID = "session.launch-another"
	ClearStatusIcon    ID = "session.clear-status"
	MarkActiveIcon     ID = "session.mark-active"
	CloseSession       ID = "session.close"
	OpenDirectoryID    ID = "session.open-directory"
	OpenEditorID       ID = "session.open-editor"
	CopyTitleID        ID = "session.copy-title"
	ToggleLockID       ID = "session.toggle-lock"
	ToggleMuteID       ID = "session.toggle-mute"
	SearchProject      ID = "session.search-project-history"
	ShowDetails        ID = "session.show-details"
	ShowTimeline       ID = "session.show-timeline"
	SendToSession      ID = "session.send"
	FilterSessions     ID = "explore.filter-sessions"
	CycleProfileFilter ID = "explore.cycle-profile-filter"
	ClearFilters       ID = "explore.clear-filters"
	OpenLauncher       ID = "explore.open-launcher"
	SearchAllSessions  ID = "explore.search-session-history"
	SearchPlans        ID = "explore.search-plans"
	ManageWorktrees    ID = "explore.manage-worktrees"
	ShowRuns           ID = "explore.show-runs"
	ToggleInspector    ID = "explore.toggle-inspector"
	SwitchProject      ID = "explore.switch-project"
	ToggleListView     ID = "explore.toggle-list-view"
	ResortTabs         ID = "app.resort-tabs"
	UndoClose          ID = "app.undo-close"
	DismissDeprecation ID = "app.dismiss-deprecation"
	ShowHealth         ID = "app.show-health"
	ShowKeyboardHelp   ID = "app.show-keyboard-shortcuts"
	Quit               ID = "app.quit"
)

// Group is both the grouping identity and the user-facing group label.
//
// The three are ordered by what an operator opening the palette is most often
// after. Navigate comes first because it holds the destinations — the launcher,
// the two histories, the worktree inventory, the run list — and a destination is
// what a palette opened on a cold dashboard is for; the session block below it
// needs a selected row to mean anything at all, and the application block is the
// one an operator reaches for least.
//
// The labels are the identities. Renaming one moves the heading and nothing
// else: a command's stable identity is its ID, which carries its own historical
// prefix and is deliberately not kept in step with the group it renders under.
type Group string

const (
	GroupNavigate    Group = "Navigate"
	GroupSession     Group = "Session"
	GroupApplication Group = "Application"
)

// Context captures the dashboard row a command should target. The value is
// copied when the context is created so a background refresh or live cursor
// move cannot retarget a command before it executes.
type Context struct {
	Target    session.RunningSession
	HasTarget bool
}

// Availability carries both the controller decision and the inline reason
// rendered for disabled commands.
type Availability struct {
	Available bool
	Reason    string
}

// Command describes one entry in the dashboard command palette. Shortcut is
// display-only: palette input never dispatches the dashboard key directly.
//
// The type parameter is the root program model. Every hook is a closure the
// root supplies, which is what lets the palette evaluate a command against
// live model state without this package importing the root.
type Command[M any] struct {
	ID           ID
	Group        Group
	Title        func(M, Context) string
	Description  string
	Aliases      []string
	Shortcut     string
	Visible      func(M, Context) bool
	Availability func(M, Context) Availability
	Execute      func(M, Context) (M, tea.Cmd)
}

// StaticTitle is the constant-label form of Command.Title.
func StaticTitle[M any](title string) func(M, Context) string {
	return func(M, Context) string { return title }
}

func (c Command[M]) DisplayTitle(m M, ctx Context) string {
	return c.Title(m, ctx)
}

func (c Command[M]) IsVisible(m M, ctx Context) bool {
	return c.Visible == nil || c.Visible(m, ctx)
}

func (c Command[M]) AvailabilityFor(m M, ctx Context) Availability {
	if c.Availability == nil {
		return Availability{Available: true}
	}
	return c.Availability(m, ctx)
}

// Run applies the visibility and availability gates before execution. This is
// the single safe entry point for the palette controller: hidden and disabled
// commands are guaranteed to be no-ops.
func (c Command[M]) Run(m M, ctx Context) (M, tea.Cmd) {
	if !c.IsVisible(m, ctx) || !c.AvailabilityFor(m, ctx).Available {
		return m, nil
	}
	return c.Execute(m, ctx)
}

const (
	ReasonNoSession       = "no selected session"
	ReasonLocked          = "session is locked"
	ReasonPeekInactive    = "session is not active"
	ReasonStatusNotClear  = "session has no clearable status"
	ReasonNoProject       = "project is not configured"
	ReasonPreviewTooSmall = "terminal is too small for preview"
	ReasonNothingToUndo   = "nothing to undo"
	ReasonNoKittyTabs     = "there are no Kitty tabs to reorder"
	ReasonSendNeedsTmux   = "only a tmux session can be sent to; type in its Kitty tab"
	ReasonSendLost        = "the session's tmux server is gone"
	ReasonSendASLQuestion = "answer its agent-session-loop question from Runs"
)

// Available is the affirmative availability verdict.
func Available() Availability {
	return Availability{Available: true}
}

// Unavailable is the negative availability verdict plus its inline reason.
func Unavailable(reason string) Availability {
	return Availability{Reason: reason}
}

// SessionAvailability gates every command that needs a selected dashboard row.
func SessionAvailability(ctx Context) Availability {
	if !ctx.HasTarget {
		return Unavailable(ReasonNoSession)
	}
	return Available()
}

// UnlockedSessionAvailability additionally refuses a locked session, which is
// the guard close, restart, and profile override share.
func UnlockedSessionAvailability(ctx Context) Availability {
	if availability := SessionAvailability(ctx); !availability.Available {
		return availability
	}
	if ctx.Target.Locked {
		return Unavailable(ReasonLocked)
	}
	return Available()
}

// PeekAvailability refuses to page the scrollback of a session that is not
// currently running a harness turn.
func PeekAvailability(ctx Context) Availability {
	if availability := SessionAvailability(ctx); !availability.Available {
		return availability
	}
	if ctx.Target.State != "bolt" {
		return Unavailable(ReasonPeekInactive)
	}
	return Available()
}

// ConfiguredProjectAvailability refuses the directory- and editor-opening
// commands for a live session with no configured project behind it.
func ConfiguredProjectAvailability(cfg *config.Config, ctx Context) Availability {
	if availability := SessionAvailability(ctx); !availability.Available {
		return availability
	}
	if _, ok := FindConfigSession(cfg, ctx.Target.EffectiveProfile(), ctx.Target.EffectiveProject()); !ok {
		return Unavailable(ReasonNoProject)
	}
	return Available()
}
