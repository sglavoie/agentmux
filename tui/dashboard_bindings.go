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
)

// dashboardBindings is the complete ModeList binding table.
//
// Order is dispatch precedence, not catalog order. The two are the same
// everywhere except `d`, whose two claimants are declared adjacent below so the
// precedence between them is visible at the only place it matters.
func dashboardBindings() commands.BindingSet {
	return commands.BindingSet{
		// Session — every one of these resolves its target from the captured
		// context, so a cursor parked on a family summary or on nothing yields no
		// target and the key refuses. That refusal is the summary guard: it is a
		// property of the context, not a check any of these keys performs.
		{Keys: []string{keyEnter, keySpace}, Display: keyEnter + " / " + keySpace, Hint: "focus", Command: commands.FocusSession},
		{Keys: []string{keyE}, Display: keyE, Hint: "peek", Command: commands.PeekScrollback},
		{Keys: []string{keyR}, Display: keyR, Hint: "restart", Command: commands.RestartSession},
		{Keys: []string{keyC}, Display: keyC, Hint: "profile", Command: commands.OverrideProfile},
		{Keys: []string{keyDashboardLaunch}, Display: keyDashboardLaunch, Hint: "launch", Command: commands.LaunchAnother},
		{Keys: []string{keyClearStatus}, Display: keyClearStatus, Hint: "clear", Command: commands.ClearStatusIcon},
		{Keys: []string{keyClose}, Display: keyClose, Hint: "close", Command: commands.CloseSession},
		{Keys: []string{keyO}, Display: keyO, Hint: "open dir", Command: commands.OpenDirectoryID},
		{Keys: []string{keyOpenEditor}, Display: keyOpenEditor, Hint: "open in editor", Command: commands.OpenEditorID},
		{Keys: []string{keyY}, Display: keyY, Command: commands.CopyTitleID},
		{Keys: []string{keyLock}, Display: keyLock, Command: commands.ToggleLockID},
		{Keys: []string{keyM}, Display: keyM, Command: commands.ToggleMuteID},
		{Keys: []string{keyProjectSessionSearch}, Display: keyProjectSessionSearch, Hint: "project sessions", Command: commands.SearchProject},

		// `d` is shared, and the banner wins it. A visible deprecation banner is
		// the one thing on the dashboard the operator cannot dismiss any other
		// way, so it claims the key until it is gone and the detail pane opens on
		// the next press. Reversing these two lines reverses that.
		{Keys: []string{keyD}, Display: keyD, Command: commands.DismissDeprecation},
		{Keys: []string{keyD}, Display: keyD, Command: commands.ShowDetails},

		{Keys: []string{keyOpenTimeline}, Display: keyOpenTimeline, Hint: "timeline", Command: commands.ShowTimeline},

		// Explore.
		{Keys: []string{keySlash}, Display: keySlash, Hint: "filter", Command: commands.FilterSessions},
		{Keys: []string{keyF}, Display: keyF, Hint: "profile", Command: commands.CycleProfileFilter},
		{Keys: []string{keyEsc}, Display: keyEsc, Hint: "clear filters", Command: commands.ClearFilters},
		{Keys: []string{keyL}, Display: keyL, Hint: "launch", Command: commands.OpenLauncher},
		{Keys: []string{keySessionSearch}, Display: keySessionSearch, Hint: "sessions", Command: commands.SearchAllSessions},
		{Keys: []string{keyPlanSearch}, Display: keyPlanSearch, Hint: "plans", Command: commands.SearchPlans},
		{Keys: []string{keyWorktrees}, Display: keyWorktrees, Command: commands.ManageWorktrees},
		{Keys: []string{keyASLRuns}, Display: keyASLRuns, Command: commands.ShowRuns},
		// One of the three unchecked bindings. The inspector never refuses: a
		// terminal too narrow for a split gets the pane full-screen instead, so
		// there is no state in which the key should read as unavailable and a
		// gate would only be a chance to get that wrong.
		{Keys: []string{keyInspector}, Display: keyInspector, Hint: "inspector", Command: commands.ToggleInspector, Gate: commands.GateUnchecked},
		// The second. The switcher always has something to offer — All is a scope
		// like any other and the aggregation always produces it — so there is no
		// empty session list at which the key should read as dead.
		{Keys: []string{keyProjectSwitcher}, Display: keyProjectSwitcher, Hint: "projects", Command: commands.SwitchProject, Gate: commands.GateUnchecked},
		{Keys: []string{keyToggleView}, Display: keyToggleView, Hint: "group view", Command: commands.ToggleListView},

		// App.
		{Keys: []string{keyResort}, Display: keyResort, Hint: "resort", Command: commands.ResortTabs},
		// The third unchecked binding. `undoLastCmd` owns the undo stack: asked
		// for an entry past its deadline it discards it and does nothing, which
		// is both the refusal and the only thing that prunes a stale entry on
		// demand. Gating the key would leave the expired entry sitting there
		// until the next toast tick swept it.
		{Keys: []string{keyU}, Display: keyU, Command: commands.UndoClose, Gate: commands.GateUnchecked},
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
// refusal from re-reading a cursor a background refresh has moved underneath it,
// and it is why `d` cannot dismiss a banner against one row and open the detail
// pane of another.
func (m Model) dispatchDashboardBinding(key string) (tea.Model, tea.Cmd, bool) {
	ctx := dashboardBindingContext(m)
	claimed := false
	refusal := ""
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
			if refusal == "" {
				refusal = availability.Reason
			}
			continue
		}
		updated, cmd := candidate.Execute(m, ctx)
		return updated, cmd, true
	}
	if !claimed {
		return m, nil, false
	}
	// One refusal speaks. A locked session is a decision the operator made and
	// can undo, so a key that hits it says so rather than appearing dead; every
	// other refusal — no selection, an inactive pane, an unconfigured project —
	// is already legible from the row the cursor is on.
	if refusal == commands.ReasonLocked {
		return m, m.lockedDenyToast(), true
	}
	return m, nil, true
}

// runDashboardBinding is dispatchDashboardBinding for a caller that has already
// established the key is bound — the key handler's own cases, which branch on
// model state first and then hand the keypress to the shared path.
func (m Model) runDashboardBinding(key string) (tea.Model, tea.Cmd) {
	updated, cmd, _ := m.dispatchDashboardBinding(key)
	return updated, cmd
}
