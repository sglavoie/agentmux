// Package sessionactions is the vocabulary of the actions a dashboard row
// offers: what each action is called, what it explains about itself, which
// immediate key the per-row menu answers it with, which band of the menu it
// sits in, and whether the row captured for it can run it right now.
//
// It is one declaration read by two surfaces. The per-row actions overlay and
// the command palette each used to state the same twelve actions in full, in
// their own file, with their own availability rule. Two spellings of one fact
// is what this package removes: an entry added to one surface and forgotten in
// the other was a silent divergence, and an availability rule corrected in one
// was a bug left standing in the other.
//
// What stays with each host is what a choice DOES. The menu keeps its runners
// and the palette keeps its executors, both keyed by the identity declared
// here, because an effect closes over the host model and this package names no
// host model at all: availability reads an Env value and a captured Target
// rather than a Bubble Tea model, which is what lets the dashboard, the palette,
// and the live session overview consume the same definitions without this
// package importing any of them.
//
// Two facts that look like they belong here deliberately do not. The dashboard's
// own key table is a separate declaration (the root's dashboardBindings), so
// Mnemonic is the key the per-row MENU answers and nothing else — an action with
// no mnemonic may still carry a dashboard shortcut. And the palette's title,
// description, and aliases are kept beside the menu's label and description
// rather than merged into one spelling: the two surfaces word themselves
// differently today, and one declaration per action is the duplication worth
// removing, not one wording per action.
package sessionactions

import (
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// PaletteGroup is the command-palette group every session action is filed
// under. It is stated once rather than per definition because the grouping a
// session action varies by is MenuGroup; its palette group is the same for all
// of them, and a per-entry field would be a fact to keep in agreement instead
// of one to read.
const PaletteGroup = commands.GroupSession

// MenuGroup is the band a per-row menu entry sits in. The bands separate what
// opens something from what changes a session's status and from what ends or
// replaces the harness process, so a destructive entry is never adjacent to the
// one an operator reaches for by reflex.
type MenuGroup int

const (
	// MenuGroupNone is the band of an action the per-row menu does not offer at
	// all. It is the zero value, so a definition that forgets to declare a band
	// stays out of the menu rather than landing in the first one.
	MenuGroupNone MenuGroup = iota
	// MenuGroupOpen opens, inspects, or copies: nothing it does is felt by the
	// harness process.
	MenuGroupOpen
	// MenuGroupStatus changes what the session reports or suppresses — its
	// badge, its lock, its notifications — and nothing else.
	MenuGroupStatus
	// MenuGroupLifecycle ends or replaces the harness process.
	MenuGroupLifecycle
)

func (g MenuGroup) String() string {
	switch g {
	case MenuGroupOpen:
		return "Open"
	case MenuGroupStatus:
		return "Status"
	case MenuGroupLifecycle:
		return "Lifecycle"
	default:
		return ""
	}
}

// Mnemonic is the immediate key the per-row menu answers an action with. Key is
// the literal key-message spelling a handler compares against; Display is what
// the menu prints for it, which differs wherever the spelling is not a glyph an
// operator would recognize on a key cap.
//
// Exactly one mnemonic is also the key that runs whatever is SELECTED:
// commands.MenuExecuteKey, which focus-session declares. The two readings agree
// where it matters, because focus-session is the entry the menu opens on — enter
// on an untouched menu focuses the session, as it did before the menu had a
// cursor — and the selection layer takes the key first, so moving the cursor and
// pressing enter runs the entry under it rather than focusing. The menu does not
// PRINT that mnemonic beside its entry (see Action.ImmediateKey): a key whose
// meaning depends on where the cursor is, drawn against one row, would be a
// claim that stops being true as soon as the operator moves.
type Mnemonic struct {
	Display string
	Key     string
}

// Bound reports whether the menu answers any key for this action.
func (m Mnemonic) Bound() bool { return m.Key != "" }

// Env is the live dashboard state an availability rule reads. It is a value
// rather than a host model, which is what keeps this package free of both
// hosts: each one projects its own state onto an Env at the moment it asks.
//
// The zero Env is meaningful: no configuration, no recorded activity, and no
// idle threshold, which is the reading a host that has not loaded anything yet
// should get.
type Env struct {
	Config        *config.Config
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
}

// Definition is everything both surfaces need to know about one session action
// except what it does.
type Definition struct {
	// ID is the stable identity every surface, binding, and test names the
	// action by. It is deliberately independent of every label below so a copy
	// edit cannot move a selection or break a dispatch.
	ID commands.ID

	// MenuGroup is the band the per-row menu files this action under, or
	// MenuGroupNone for an action the menu does not offer.
	MenuGroup MenuGroup
	// Mnemonic is the menu's immediate key. It is bound exactly when the action
	// is in the menu.
	Mnemonic Mnemonic
	// MenuLabel and MenuDescription are the menu's own wording: a short label
	// beside the key, and one line explaining what running it would do.
	MenuLabel       string
	MenuDescription string

	// PaletteTitle is a function rather than a string because two actions read
	// their own target to name themselves — a locked row offers Unlock, a muted
	// row offers Unmute — and a title that could not see the target would have
	// to name both directions at once.
	PaletteTitle func(Target) string
	// PaletteDescription and PaletteAliases are the palette's wording and its
	// extra search terms.
	PaletteDescription string
	PaletteAliases     []string

	// Availability answers whether the captured target can run this action now,
	// and when it cannot, why. Both surfaces read the same verdict; what they do
	// with the reason is theirs — the palette prints it beside a dimmed row.
	Availability func(Env, Target) commands.Availability
}

// InMenu reports whether the per-row actions menu offers this action.
func (d Definition) InMenu() bool { return d.MenuGroup != MenuGroupNone }

// AvailabilityFor is the safe way to ask: a definition with no rule is
// available, so a new action is offered rather than silently dimmed.
func (d Definition) AvailabilityFor(env Env, target Target) commands.Availability {
	if d.Availability == nil {
		return commands.Available()
	}
	return d.Availability(env, target)
}

// Title is the safe way to read the palette title of a definition that declares
// none.
func (d Definition) Title(target Target) string {
	if d.PaletteTitle == nil {
		return ""
	}
	return d.PaletteTitle(target)
}

// static is the constant-label form of Definition.PaletteTitle.
func static(title string) func(Target) string {
	return func(Target) string { return title }
}

// requiresSession is the gate every session action shares: without a captured
// row there is nothing to act on.
func requiresSession(_ Env, target Target) commands.Availability {
	return commands.SessionAvailability(target.Context())
}

// requiresUnlocked additionally refuses a locked row, which is the guard close,
// restart, and profile override share.
func requiresUnlocked(_ Env, target Target) commands.Availability {
	return commands.UnlockedSessionAvailability(target.Context())
}

// requiresActive refuses to page the scrollback of a row that is not running a
// harness turn.
func requiresActive(_ Env, target Target) commands.Availability {
	return commands.PeekAvailability(target.Context())
}

// requiresConfiguredProject refuses the directory- and editor-opening actions
// for a live session with no configured project behind it.
func requiresConfiguredProject(env Env, target Target) commands.Availability {
	return commands.ConfiguredProjectAvailability(env.Config, target.Context())
}

// requiresClearableStatus refuses a row whose badge is not the kind an operator
// can acknowledge away.
func requiresClearableStatus(env Env, target Target) commands.Availability {
	if availability := commands.SessionAvailability(target.Context()); !availability.Available {
		return availability
	}
	if dashboard.StateGroup(target.Session, env.LastActivity, env.IdleThreshold) != 0 {
		return commands.Unavailable(commands.ReasonStatusNotClear)
	}
	return commands.Available()
}

// Definitions returns every session action in PALETTE order: the first twelve
// are the ones the per-row menu also offers, the rest are palette-only.
//
// This is not menu order. The menu bands its entries, and MenuDefinitions
// derives that order from MenuGroup rather than reading a second list, so the
// band an action declares and the position it appears at cannot disagree.
//
// A fresh slice is returned per call, and every closure in it reads only its
// arguments, so this package holds no state two surfaces could race over.
func Definitions() []Definition {
	return []Definition{
		{
			ID:                 commands.FocusSession,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "⏎", Key: "enter"},
			MenuLabel:          "Open session",
			MenuDescription:    "jump to the session's Kitty tab",
			PaletteTitle:       static("Focus Selected Session"),
			PaletteDescription: "Jump to the selected session's Kitty tab.",
			PaletteAliases:     []string{"jump", "tab"},
			Availability:       requiresSession,
		},
		{
			ID:                 commands.PeekScrollback,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "e", Key: "e"},
			MenuLabel:          "Peek scrollback",
			MenuDescription:    "open the session's scrollback in an overlay pager",
			PaletteTitle:       static("Peek Scrollback"),
			PaletteDescription: "Open the active session's scrollback in an overlay pager.",
			PaletteAliases:     []string{"pager", "scrollback"},
			Availability:       requiresActive,
		},
		{
			ID:                 commands.RestartSession,
			MenuGroup:          MenuGroupLifecycle,
			Mnemonic:           Mnemonic{Display: "r", Key: "r"},
			MenuLabel:          "Restart",
			MenuDescription:    "close and relaunch the session",
			PaletteTitle:       static("Restart Session"),
			PaletteDescription: "Confirm before closing and relaunching the selected session.",
			PaletteAliases:     []string{"relaunch"},
			Availability:       requiresUnlocked,
		},
		{
			ID:                 commands.OverrideProfile,
			MenuGroup:          MenuGroupLifecycle,
			Mnemonic:           Mnemonic{Display: "c", Key: "c"},
			MenuLabel:          "Restart with another profile",
			MenuDescription:    "restart under a different harness profile",
			PaletteTitle:       static("Override Profile"),
			PaletteDescription: "Restart the selected session under a different harness profile.",
			PaletteAliases:     []string{"account", "profile"},
			Availability:       requiresUnlocked,
		},
		{
			ID:                 commands.LaunchAnother,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "C", Key: "C"},
			MenuLabel:          "Launch another session",
			MenuDescription:    "pick a profile and launch another session from this row",
			PaletteTitle:       static("Launch Another Session"),
			PaletteDescription: "Pick a profile and launch another session from the selected project.",
			PaletteAliases:     []string{"duplicate", "new"},
			Availability:       requiresSession,
		},
		{
			ID:                 commands.ClearStatusIcon,
			MenuGroup:          MenuGroupStatus,
			Mnemonic:           Mnemonic{Display: "x", Key: "x"},
			MenuLabel:          "Clear status",
			MenuDescription:    "drop the attention/key/stop badge on the tab title",
			PaletteTitle:       static("Clear Status Icon"),
			PaletteDescription: "Drop the attention, key, or stop badge from the selected tab.",
			PaletteAliases:     []string{"attention", "badge", "icon"},
			Availability:       requiresClearableStatus,
		},
		{
			ID:                 commands.CloseSession,
			MenuGroup:          MenuGroupLifecycle,
			Mnemonic:           Mnemonic{Display: "X", Key: "X"},
			MenuLabel:          "Close",
			MenuDescription:    "exit the harness process; transcript persists",
			PaletteTitle:       static("Close Session"),
			PaletteDescription: "Exit the harness process while preserving its transcript.",
			PaletteAliases:     []string{"exit", "kill"},
			Availability:       requiresUnlocked,
		},
		{
			ID:                 commands.OpenDirectoryID,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "o", Key: "o"},
			MenuLabel:          "Open shell",
			MenuDescription:    "spawn a plain shell tab at the session's directory",
			PaletteTitle:       static("Open Directory in Shell"),
			PaletteDescription: "Open a plain shell tab in the selected project's directory.",
			PaletteAliases:     []string{"directory", "shell", "terminal"},
			Availability:       requiresConfiguredProject,
		},
		{
			ID:                 commands.OpenEditorID,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "O", Key: "O"},
			MenuLabel:          "Open editor",
			MenuDescription:    "open the project directory in $VISUAL/$EDITOR (or defaults.editor)",
			PaletteTitle:       static("Open in Editor"),
			PaletteDescription: "Open the selected project in the configured editor.",
			PaletteAliases:     []string{"directory", "edit"},
			Availability:       requiresConfiguredProject,
		},
		{
			ID:                 commands.CopyTitleID,
			MenuGroup:          MenuGroupOpen,
			Mnemonic:           Mnemonic{Display: "y", Key: "y"},
			MenuLabel:          "Copy title",
			MenuDescription:    "copy the session title to the system clipboard",
			PaletteTitle:       static("Copy Session Title"),
			PaletteDescription: "Copy the selected session's display title to the clipboard.",
			PaletteAliases:     []string{"clipboard", "yank"},
			Availability:       requiresSession,
		},
		{
			ID:              commands.ToggleLockID,
			MenuGroup:       MenuGroupStatus,
			Mnemonic:        Mnemonic{Display: "L", Key: "L"},
			MenuLabel:       "Lock/Unlock",
			MenuDescription: "toggle session lock (prevents close/restart/override)",
			PaletteTitle: func(target Target) string {
				if target.HasSession && target.Session.Locked {
					return "Unlock Session"
				}
				return "Lock Session"
			},
			PaletteDescription: "Toggle protection from close, restart, and profile override.",
			PaletteAliases:     []string{"protect", "unlock"},
			Availability:       requiresSession,
		},
		{
			ID:              commands.ToggleMuteID,
			MenuGroup:       MenuGroupStatus,
			Mnemonic:        Mnemonic{Display: "m", Key: "m"},
			MenuLabel:       "Mute/Unmute",
			MenuDescription: "toggle session mute (suppresses macOS notifications)",
			PaletteTitle: func(target Target) string {
				if target.HasSession && target.Session.Muted {
					return "Unmute Session"
				}
				return "Mute Session"
			},
			PaletteDescription: "Toggle macOS notifications for the selected session.",
			PaletteAliases:     []string{"notifications", "unmute"},
			Availability:       requiresSession,
		},

		// Palette-only from here down. Each opens a surface of its own rather
		// than acting on the row, which is why the per-row menu — a list an
		// operator runs one entry from and closes — does not carry them.
		{
			ID:                 commands.SearchProject,
			PaletteTitle:       static("Search This Project's Past Sessions"),
			PaletteDescription: "Search transcript history filtered to the selected project.",
			PaletteAliases:     []string{"history", "project", "transcripts"},
			Availability:       requiresSession,
		},
		{
			ID:                 commands.ShowDetails,
			PaletteTitle:       static("Show Session Details"),
			PaletteDescription: "Open the selected session's hook event log.",
			PaletteAliases:     []string{"events", "hooks"},
			Availability:       requiresSession,
		},
		{
			ID:                 commands.ShowTimeline,
			PaletteTitle:       static("Show Session Timeline"),
			PaletteDescription: "Open the selected session's events grouped into timeline phases.",
			PaletteAliases:     []string{"events", "phases"},
			Availability:       requiresSession,
		},
	}
}

// MenuDefinitions returns the actions the per-row menu offers, in menu order:
// the Open band, then Status, then Lifecycle, and inside each band the order
// Definitions declares.
//
// The band order is derived by a STABLE sort over MenuGroup rather than written
// out as a second list. A hand-kept menu order would be a third place an action
// is named — after the definition and the runner table — and the one most likely
// to be forgotten, since a definition added without touching it lands wherever
// the palette happens to put it. Sorting means a new entry's band decides where
// it appears and nothing else has to be edited.
//
// Reading the bands in that order is also the safety argument for the menu:
// nothing in Open is felt by the harness process, so the entries an operator
// reaches for by reflex are never adjacent to the ones that end a session.
func MenuDefinitions() []Definition {
	all := Definitions()
	menu := make([]Definition, 0, len(all))
	for _, definition := range all {
		if definition.InMenu() {
			menu = append(menu, definition)
		}
	}
	sort.SliceStable(menu, func(i, j int) bool {
		return menu[i].MenuGroup < menu[j].MenuGroup
	})
	return menu
}

// InitialSelection is the entry a freshly opened menu selects.
//
// Focusing the session is both the most-run action on the list and the one that
// does the least, so a menu that opens on it is one an operator can dismiss with
// enter by reflex without consequence. It is also what enter did before the menu
// had a cursor at all, which is what makes the selectable menu a superset of the
// old dispatch rather than a change to it.
func InitialSelection() commands.ID { return commands.FocusSession }

// MenuIndexOf is the position an identity occupies in menu order, or 0 when the
// menu does not offer it. Zero is the harmless answer: it selects the first
// entry, which is InitialSelection, rather than an index no entry has.
func MenuIndexOf(id commands.ID) int {
	for i, definition := range MenuDefinitions() {
		if definition.ID == id {
			return i
		}
	}
	return 0
}

// Lookup resolves one definition by its identity.
func Lookup(id commands.ID) (Definition, bool) {
	for _, definition := range Definitions() {
		if definition.ID == id {
			return definition, true
		}
	}
	return Definition{}, false
}
