// This file is the dashboard's one binding declaration: which key runs which
// catalog command, how the binding is spelled, what the footer calls it, and
// what a keypress does when the command refuses.
//
// Three consumers read it and none of them restates it. `handleListKey`
// dispatches through it, so a dashboard key and its palette entry run the same
// closure. `commandCatalog` takes every `Shortcut` label from it, so a palette
// row cannot advertise a key that nothing is bound to. `listFooterPalette`
// takes each command-backed entry's key, label, and availability from it, so
// the footer and the generated help follow a rebinding without being edited.

package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// dashboardBindings is the complete ModeList binding table.
//
// The table keeps frequent navigation directly accessible. Every other catalog
// command is reached through the actions overlay (`a`) for row-targeted actions
// and through the palette (`:`) for the rest, without a key of its own here.
// `docs/dashboard-keymap-inventory.md` is the row-by-row
// record of which capability went to which surface; a binding may only be
// removed once that table names its successor.
//
// The retirement needs no other edit to take effect, which is the reason the
// table is the single declaration. A key absent from here is not dispatched,
// because `dispatchDashboardBinding` reports an unbound key as unclaimed; it is
// not advertised, because `commandCatalog` reads every `Shortcut` from here and
// a command with no binding gets no shortcut label; and it is not in the footer
// or the generated help, because both read this table too.
//
// Order is dispatch precedence, not catalog order. Nothing shares a key now that
// `d` has stopped being contested, so the two agree throughout.
func dashboardBindings() commands.BindingSet {
	return commands.BindingSet{
		// Session. Enter resolves its target from the captured context, so a
		// cursor parked on a family summary or on nothing yields no target and the
		// key refuses. That refusal is the summary guard: it is a property of the
		// context, not a check the key performs. `handleListKey` answers Enter on a
		// summary before it ever reaches here, by folding.
		{Keys: []string{keyEnter}, Display: keyEnter, Hint: "focus", Command: commands.FocusSession},
		{Keys: []string{keyProjectSessionSearch}, Display: keyProjectSessionSearch, Hint: "project history", Command: commands.SearchProject},

		// Navigate.
		{Keys: []string{keyAllSessions}, Display: keyAllSessions, Hint: "past sessions", Command: commands.SearchAllSessions},
		{Keys: []string{keySlash}, Display: keySlash, Hint: "filter", Command: commands.FilterSessions},
		{Keys: []string{keyEsc}, Display: keyEsc, Hint: "clear filters", Command: commands.ClearFilters},
		{Keys: []string{keyL}, Display: keyL, Hint: "launch", Command: commands.OpenLauncher},
		// One of the two unchecked bindings. The inspector never refuses: a
		// terminal too narrow for a split gets the pane full-screen instead, so
		// there is no state in which the key should read as unavailable and a
		// gate would only be a chance to get that wrong.
		{Keys: []string{keyInspector}, Display: keyInspector, Hint: "inspector", Command: commands.ToggleInspector, Gate: commands.GateUnchecked},
		// The second. The switcher always has something to offer — All is a scope
		// like any other and the aggregation always produces it — so there is no
		// empty session list at which the key should read as dead.
		{Keys: []string{keyProjectSwitcher}, Display: keyProjectSwitcher, Hint: "projects", Command: commands.SwitchProject, Gate: commands.GateUnchecked},

		// Application. Undo is checked, and the gate is the point: it reads the recovery
		// stack, so the key is live for exactly the seconds a close is still
		// recoverable and dead — with "nothing to undo" beside it — the rest of
		// the time. It is the one key here whose availability changes on a timer
		// rather than with the cursor.
		{Keys: []string{keyUndoClose}, Display: keyUndoClose, Hint: "undo close", Command: commands.UndoClose},
		{Keys: []string{keyQuestion}, Display: keyQuestion, Hint: "help", Command: commands.ShowKeyboardHelp},
		{Keys: []string{keyQ, keyCtrlC}, Display: keyQ + " / " + keyCtrlC, Hint: "quit", Command: commands.Quit},
	}
}

// dashboardCommand resolves a catalog entry by its stable ID.
//
// The catalog is a constant: every entry closes over nothing but the model and
// context handed to it at call time, so one index serves every lookup. It is
// built once because the footer asks it a question per binding per rendered
// frame, and the frame is measured more than once.
//
// The index is built lazily rather than declared as an initialized package
// variable, and that is load-bearing. A command's availability may measure the
// footer, the footer reads the bindings, and the bindings resolve commands —
// which is an ordinary recursion at render time but an initialization cycle if
// the index is a variable with an initializer. Nothing inside the builder calls
// back into this function, so the once is not reentered.
var (
	dashboardCommandOnce  sync.Once
	dashboardCommandIndex map[commands.ID]commands.Command[Model]
)

func dashboardCommand(id commands.ID) (commands.Command[Model], bool) {
	dashboardCommandOnce.Do(func() {
		catalog := commandCatalog()
		dashboardCommandIndex = make(map[commands.ID]commands.Command[Model], len(catalog))
		for _, candidate := range catalog {
			dashboardCommandIndex[candidate.ID] = candidate
		}
	})
	candidate, ok := dashboardCommandIndex[id]
	return candidate, ok
}

// dashboardBindingContext is the target a dashboard *binding* acts on: the
// captured selection, unconditionally. It used to have a second answer for the
// state where the project rail held the keyboard, because a scope row is not a
// session and every row-targeted binding had to refuse against it. The switcher
// is an overlay with its own key handler, so no dashboard binding is dispatched
// while a scope is being chosen and there is no such state left to answer for.
func dashboardBindingContext(m Model) commands.Context {
	return captureCommandContext(m)
}

// dashboardCommandAvailable is the footer's availability question answered by
// the command itself: a key is live when the command behind it is neither
// hidden nor unavailable for the current selection.
func dashboardCommandAvailable(id commands.ID) func(Model) bool {
	return func(m Model) bool {
		candidate, ok := dashboardCommand(id)
		if !ok {
			return false
		}
		ctx := dashboardBindingContext(m)
		return candidate.IsVisible(m, ctx) && candidate.AvailabilityFor(m, ctx).Available
	}
}

// dispatchDashboardBinding runs the command bound to key and reports whether
// the dashboard binding table claimed the keypress at all. An unclaimed key is
// left to the caller; a claimed one always ends here, refused or not.
//
// The target is captured once, before anything is decided, and every binding
// that claims the key is offered that same snapshot. That is what stops a
// refusal from re-reading a cursor a background refresh has moved underneath it.
// No key is claimed by two bindings today, but the loop and the single capture
// stay: the alternative is a table whose correctness depends on nobody ever
// declaring a second claimant.
// Every refusal is silent. A refused key leaves the model exactly as it was and
// says nothing, because what it would have said is already on the row the cursor
// is on: no selection, a summary rather than a session, an empty list. The lock
// denial that used to speak here went with the three destructive shortcuts it
// answered for — the actions overlay and the palette draw those commands dimmed
// with their reason, which says it before the keystroke rather than after.
func (m Model) dispatchDashboardBinding(key string) (tea.Model, tea.Cmd, bool) {
	ctx := dashboardBindingContext(m)
	// A direct key is chosen from the list itself, so there is no surface to
	// return to and the origin is empty. It is cleared rather than left alone
	// because the field is consumed by the next dispatch: a palette query
	// abandoned by a command that never reached the controller must not become
	// the return context of the next keystroke.
	m.pendingActionOrigin = sessionactions.Origin{}
	claimed := false
	for _, binding := range dashboardBindings().For(key) {
		candidate, ok := dashboardCommand(binding.Command)
		if !ok {
			continue
		}
		claimed = true
		if binding.Gate == commands.GateUnchecked {
			updated, cmd := candidate.Execute(m, ctx)
			return updated, cmd, true
		}
		if !candidate.IsVisible(m, ctx) {
			continue
		}
		if availability := candidate.AvailabilityFor(m, ctx); !availability.Available {
			continue
		}
		updated, cmd := candidate.Execute(m, ctx)
		return updated, cmd, true
	}
	return m, nil, claimed
}

// runDashboardBinding is dispatchDashboardBinding for a caller that has already
// established the key is bound — the key handler's own cases, which branch on
// model state first and then hand the keypress to the shared path.
func (m Model) runDashboardBinding(key string) (tea.Model, tea.Cmd) {
	updated, cmd, _ := m.dispatchDashboardBinding(key)
	return updated, cmd
}
