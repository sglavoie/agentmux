package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// dashboardKeyPress builds the KeyPressMsg for a binding's canonical key.
// keyPress covers the printable keys the older tests press by hand; the two the
// binding table names that it does not spell are added here rather than there,
// so a table entry is always pressable from a test.
func dashboardKeyPress(key string) tea.KeyPressMsg {
	switch key {
	case keySpace:
		return keyPress(" ")
	case keyCtrlC:
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	}
	return keyPress(key)
}

// bindingOutcome is everything a dashboard binding can be observed to do to the
// model, flattened so two execution paths can be compared as values. It is
// deliberately wider than any single command touches: a field nothing reads is
// free, and a field nobody thought to compare is how a divergence hides.
type bindingOutcome struct {
	mode                   Mode
	targetTab              int
	confirmAction          string
	confirmConsequence     string
	confirmReturnMode      Mode
	configPickerPurpose    launch.ConfigPickerPurpose
	configPickerReturnMode Mode
	configPickerCurrent    string
	detailWindow           int
	inspectorDisplay       inspectorDisplay
	paneFocus              paneFocus
	previewWindowID        int
	projectSwitcherKey     string
	listView               dashboard.ViewMode
	deprecationDismissed   bool
	filterRaw              string
	toastTexts             string
	issuedCmd              bool
}

func observeBinding(m Model, cmd tea.Cmd) bindingOutcome {
	texts := make([]string, 0, len(m.toasts))
	for _, t := range m.toasts {
		texts = append(texts, t.Text)
	}
	return bindingOutcome{
		mode:                   m.mode,
		targetTab:              m.targetSession.TabID,
		confirmAction:          m.confirmAction,
		confirmConsequence:     m.confirmConsequence,
		confirmReturnMode:      m.confirmReturnMode,
		configPickerPurpose:    m.configPickerPurpose,
		configPickerReturnMode: m.configPickerReturnMode,
		configPickerCurrent:    m.configPickerCurrent,
		detailWindow:           m.detailWindowID,
		inspectorDisplay:       m.inspectorDisplay,
		paneFocus:              m.paneFocus,
		previewWindowID:        m.previewWindowID,
		projectSwitcherKey:     m.projectSwitcherKey,
		listView:               m.listView,
		deprecationDismissed:   m.deprecationDismissed,
		filterRaw:              m.activeFilter.Raw,
		toastTexts:             strings.Join(texts, "|"),
		issuedCmd:              cmd != nil,
	}
}

// runViaPalette executes id the way the command palette does: consult the
// command's own gates, and run it only if they pass. It is the reference the
// direct-key path is measured against.
func runViaPalette(m Model, id commands.ID) bindingOutcome {
	candidate, ok := dashboardCommand(id)
	if !ok {
		return observeBinding(m, nil)
	}
	ctx := captureCommandContext(m)
	if !candidate.IsVisible(m, ctx) || !candidate.AvailabilityFor(m, ctx).Available {
		return observeBinding(m, nil)
	}
	updated, cmd := candidate.Execute(m, ctx)
	return observeBinding(updated, cmd)
}

func runViaKey(m Model, key string) bindingOutcome {
	updated, cmd := m.Update(dashboardKeyPress(key))
	return observeBinding(updated.(Model), cmd)
}

// dashboardBindingStates are the model states a binding is exercised against.
// Between them they cover every gate any dashboard command consults: no
// selection at all, a session that is and is not active, a locked one, a
// configured project and an unconfigured one, a synthetic summary row, a
// visible banner, and an active filter.
func dashboardBindingStates(t *testing.T) []struct {
	name string
	m    Model
} {
	t.Helper()

	empty := testModelWithSessions()

	active := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})

	attention := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 2, WindowID: 12, Base: "backend", Account: "work", Directory: "/home/user/backend", State: "key",
	})

	locked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt", Locked: true,
	})

	unconfigured := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 4, WindowID: 14, Base: "stray", Account: "nowhere", Directory: "/tmp/stray", State: "bolt",
	})

	banner := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 5, WindowID: 15, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	banner.deprecations = []config.Deprecation{{Key: "accounts", Replacement: "profiles"}}

	filtered := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 6, WindowID: 16, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	filtered.activeFilter = dashboard.Parse("api")

	return []struct {
		name string
		m    Model
	}{
		{"empty list", empty},
		{"active row", active},
		{"attention row", attention},
		{"locked row", locked},
		{"unconfigured project", unconfigured},
		{"deprecation banner visible", banner},
		{"filter active", filtered},
	}
}

// TestEveryDashboardBindingNamesACatalogCommand is the totality check that
// makes the table safe to derive labels from: a binding whose command does not
// exist would advertise a key in the footer and the palette that nothing runs.
func TestEveryDashboardBindingNamesACatalogCommand(t *testing.T) {
	for _, binding := range dashboardBindings() {
		if len(binding.Keys) == 0 {
			t.Errorf("binding for %q declares no key", binding.Command)
			continue
		}
		if binding.Display == "" {
			t.Errorf("binding for %q declares no display spelling", binding.Command)
		}
		if _, ok := dashboardCommand(binding.Command); !ok {
			t.Errorf("binding %q names command %q, which is not in the catalog", binding.Key(), binding.Command)
		}
	}
}

// TestPaletteShortcutsComeFromTheBindingTable pins the derivation: no catalog
// entry carries a shortcut of its own, so a rebinding cannot leave the palette
// advertising the old key.
func TestPaletteShortcutsComeFromTheBindingTable(t *testing.T) {
	bindings := dashboardBindings()
	for _, candidate := range commandCatalog() {
		want := bindings.Display(candidate.ID)
		if candidate.Shortcut != want {
			t.Errorf("%s: shortcut %q, binding table says %q", candidate.ID, candidate.Shortcut, want)
		}
	}
}

// TestDirectKeyAndPaletteAgree is the review item stated as a test: for every
// checked binding, in every state, pressing the key does exactly what running
// the command from the palette does. The two unchecked bindings are excluded
// here and covered by name below — their difference is the declaration, not an
// accident.
func TestDirectKeyAndPaletteAgree(t *testing.T) {
	for _, state := range dashboardBindingStates(t) {
		for _, binding := range dashboardBindings() {
			if binding.Gate != commands.GateChecked {
				continue
			}
			// enter branches on list structure before it reaches the table, so it
			// is asserted on its own terms below.
			if binding.Binds(keyEnter) {
				continue
			}
			key := binding.Key()
			t.Run(state.name+"/"+key, func(t *testing.T) {
				viaKey := runViaKey(state.m, key)
				viaPalette := runViaPalette(state.m, binding.Command)
				if viaKey != viaPalette {
					t.Errorf("%s diverges:\n  key:     %+v\n  palette: %+v", binding.Command, viaKey, viaPalette)
				}
			})
		}
	}
}

// TestUncheckedBindingsRunTheirCommandRegardless states what the declared
// exceptions buy: each has no state in which it refuses at all, so a gate could
// only ever be a chance to get that wrong.
//
// UndoClose used to be the third. It kept its unchecked declaration for as long
// as it had a key, because a refused undo had to say "nothing to undo" rather
// than look dead; it has no dashboard key now, and the palette says the same
// thing by dimming the row.
func TestUncheckedBindingsRunTheirCommandRegardless(t *testing.T) {
	unchecked := map[commands.ID]bool{}
	for _, binding := range dashboardBindings() {
		if binding.Gate == commands.GateUnchecked {
			unchecked[binding.Command] = true
		}
	}
	want := map[commands.ID]bool{commands.ToggleInspector: true, commands.SwitchProject: true}
	if len(unchecked) != len(want) {
		t.Fatalf("unchecked bindings: got %v, want %v", unchecked, want)
	}
	for id := range want {
		if !unchecked[id] {
			t.Errorf("%s is no longer declared unchecked; the key now drops its own refusal", id)
		}
	}
}

// TestEveryRefusedBindingIsSilentAndInert is what replaced the lock toast. No
// dashboard key announces a refusal any more: the three destructive commands
// that used to need one are reached through the actions overlay and the palette,
// which draw an unavailable entry dimmed with its reason beside it — before the
// keystroke rather than after it. So a key whose command refuses must leave the
// model untouched and say nothing at all.
func TestEveryRefusedBindingIsSilentAndInert(t *testing.T) {
	for _, state := range dashboardBindingStates(t) {
		for _, binding := range dashboardBindings() {
			if binding.Gate != commands.GateChecked || binding.Binds(keyEnter) {
				continue
			}
			candidate, ok := dashboardCommand(binding.Command)
			if !ok {
				continue
			}
			ctx := dashboardBindingContext(state.m)
			if candidate.IsVisible(state.m, ctx) && candidate.AvailabilityFor(state.m, ctx).Available {
				continue
			}
			t.Run(state.name+"/"+binding.Key(), func(t *testing.T) {
				updated, cmd := state.m.Update(dashboardKeyPress(binding.Key()))
				out := updated.(Model)
				if len(out.toasts) != 0 {
					t.Errorf("refused key %q raised toasts %+v", binding.Key(), out.toasts)
				}
				if cmd != nil {
					t.Errorf("refused key %q issued a command", binding.Key())
				}
				if out.mode != ModeList {
					t.Errorf("refused key %q moved to mode %v", binding.Key(), out.mode)
				}
			})
		}
	}
}

// TestLockedRowRefusesThroughTheActionsOverlay is the lock claim restated on the
// surface that now owns it. The destructive three have no dashboard key, so the
// question is no longer "does the key say so" but "does the row the operator is
// choosing from say so" — and it must say so with the lock named.
func TestLockedRowRefusesThroughTheActionsOverlay(t *testing.T) {
	locked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt", Locked: true,
	})
	ctx := dashboardBindingContext(locked)
	for _, id := range []commands.ID{commands.RestartSession, commands.CloseSession, commands.OverrideProfile} {
		t.Run(string(id), func(t *testing.T) {
			candidate, ok := dashboardCommand(id)
			if !ok {
				t.Fatalf("%s left the catalog; a retired key must not retire its command", id)
			}
			if dashboardBindings().Display(id) != "" {
				t.Errorf("%s still advertises a dashboard key", id)
			}
			availability := candidate.AvailabilityFor(locked, ctx)
			if availability.Available {
				t.Fatalf("%s is available on a locked row", id)
			}
			if availability.Reason != commands.ReasonLocked {
				t.Errorf("%s refused with %q, want the lock named", id, availability.Reason)
			}
		})
	}
}

// TestRestartStillConfirmsFromThePalette pins the confirmation half of the
// review item across the move. Restart lost its key, not its confirmation: a
// command reached by name must still stop at the prompt.
func TestRestartStillConfirmsFromThePalette(t *testing.T) {
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 7, WindowID: 17, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	// A stale return mode from an overlay raised somewhere else must not
	// survive into this confirmation: answering it has to come back to the
	// dashboard the command was run from.
	m.confirmReturnMode = ModeWorktrees

	out, _ := runDashboardCommand(t, m, commands.RestartSession)
	if out.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm, got %v", out.mode)
	}
	if out.confirmAction != "restart" {
		t.Errorf("confirmAction = %q, want restart", out.confirmAction)
	}
	if out.confirmConsequence != restartConsequence {
		t.Errorf("confirmConsequence = %q, want the restart wording", out.confirmConsequence)
	}
	if out.confirmReturnMode != ModeList {
		t.Errorf("confirmReturnMode = %v, want ModeList", out.confirmReturnMode)
	}
	if out.targetSession.TabID != 7 {
		t.Errorf("targetSession = %d, want the row under the cursor", out.targetSession.TabID)
	}
}

// TestProfileOverlaysReturnToTheDashboard is the overlay-return-context review
// item for the two config-picker commands, which is where a wrong answer strands
// the operator in a mode they never opened. Both are palette-only now, and the
// palette is raised from the dashboard, so the answer must still be ModeList.
func TestProfileOverlaysReturnToTheDashboard(t *testing.T) {
	for _, id := range []commands.ID{commands.OverrideProfile, commands.LaunchAnother} {
		t.Run(string(id), func(t *testing.T) {
			m := withListRows(testModelWithSessions(), session.RunningSession{
				TabID: 8, WindowID: 18, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
			})
			m.configPickerReturnMode = ModeWorktrees

			out, _ := runDashboardCommand(t, m, id)
			if out.mode != ModeConfigPicker {
				t.Fatalf("expected ModeConfigPicker, got %v", out.mode)
			}
			if out.configPickerReturnMode != ModeList {
				t.Errorf("configPickerReturnMode = %v, want ModeList", out.configPickerReturnMode)
			}
			if out.configPickerProjectIdx != -1 {
				t.Errorf("configPickerProjectIdx = %d, want -1 for a dashboard-raised picker", out.configPickerProjectIdx)
			}
			if out.targetSession.TabID != 8 {
				t.Errorf("targetSession = %d, want the row under the cursor", out.targetSession.TabID)
			}
		})
	}
}

// TestFamilySummaryRefusesEverySessionBinding is the family-summary review
// item. A summary row stands for several sessions, so no session command may
// take it as a target — and the refusal comes from the captured context being
// empty rather than from a check each binding remembers to perform.
func TestFamilySummaryRefusesEverySessionBinding(t *testing.T) {
	m := testModelWithSessions()
	m.listView = dashboard.ViewProjects
	m = withListRows(m,
		session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/repo/api", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		session.RunningSession{TabID: 2, WindowID: 12, Base: "web", Account: "work", Directory: "/repo/web", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	)
	m.cursor = 0
	if !m.summarySelected() {
		t.Fatalf("fixture does not put the cursor on a family summary")
	}
	if ctx := captureCommandContext(m); ctx.HasTarget {
		t.Fatalf("a family summary produced a command target")
	}

	// Every session command, not only the bound ones. Enter is the last session
	// binding left, and it never reaches the table on a summary, so iterating the
	// table alone would make this vacuous. The palette is where the rest are
	// reached from now, and the same captured context gates both.
	for _, candidate := range commandCatalog() {
		if candidate.Group != commands.GroupSession {
			continue
		}
		t.Run(string(candidate.ID), func(t *testing.T) {
			if candidate.AvailabilityFor(m, captureCommandContext(m)).Available {
				t.Errorf("%s is available with the cursor on a family summary", candidate.ID)
			}
			if key := dashboardBindings().Display(candidate.ID); key != "" && key != keyEnter {
				updated, cmd := m.Update(dashboardKeyPress(key))
				out := updated.(Model)
				if out.targetSession.TabID != 0 {
					t.Errorf("%s captured %d as a target from a summary row", candidate.ID, out.targetSession.TabID)
				}
				if out.mode != ModeList {
					t.Errorf("%s left the dashboard for %v from a summary row", candidate.ID, out.mode)
				}
				if cmd != nil {
					t.Errorf("%s issued a command from a summary row", candidate.ID)
				}
			}
		})
	}
}

// TestEnterFoldsASummaryBeforeReachingTheTable pins the pre-branch that keeps
// the previous test honest: enter is bound to FocusSession, but on a summary it
// never gets there.
func TestEnterFoldsASummaryBeforeReachingTheTable(t *testing.T) {
	m := testModelWithSessions()
	m.listView = dashboard.ViewProjects
	m = withListRows(m,
		session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/repo/api", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		session.RunningSession{TabID: 2, WindowID: 12, Base: "web", Account: "work", Directory: "/repo/web", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	)
	m.cursor = 0
	if !m.summarySelected() {
		t.Fatalf("fixture does not put the cursor on a family summary")
	}
	before := m.listItemCount()
	updated, _ := m.Update(dashboardKeyPress(keyEnter))
	if got := updated.(Model).listItemCount(); got == before {
		t.Errorf("enter did not fold the family: item count stayed at %d", got)
	}
}

// TestNoDashboardKeyIsSharedIsTheDeclarationOrderInvariant replaced
// TestSharedKeyDismissesTheBannerFirst. Declaration order in the table used to
// be a behavior — the deprecation banner claimed `d` while it was visible and
// yielded it to the detail pane once dismissed — and it is not one any more,
// because no key is claimed twice. That is worth a test rather than a comment:
// the dispatcher still loops over every claimant, so the property is enforced
// here rather than by the loop being impossible to enter twice.
func TestNoDashboardKeyIsSharedIsTheDeclarationOrderInvariant(t *testing.T) {
	claimants := map[string][]commands.ID{}
	for _, binding := range dashboardBindings() {
		for _, key := range binding.Keys {
			claimants[key] = append(claimants[key], binding.Command)
		}
	}
	for key, ids := range claimants {
		if len(ids) > 1 {
			t.Errorf("key %q is claimed by %v; dispatch order is a behavior again", key, ids)
		}
	}
}

// TestDeprecationBannerIsDismissedByNameNotByAKey is the capability half of that
// retirement. The banner is not on the dashboard at all any more, and its
// dismissal is a palette command; what must not have happened is the command
// going with the key.
func TestDeprecationBannerIsDismissedByNameNotByAKey(t *testing.T) {
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 9, WindowID: 19, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	m.deprecations = []config.Deprecation{{Key: "accounts", Replacement: "profiles"}}

	if key := dashboardBindings().Display(commands.DismissDeprecation); key != "" {
		t.Errorf("the dismissal still advertises key %q", key)
	}
	out := pressDashboardCommand(t, m, commands.DismissDeprecation)
	if !out.deprecationDismissed {
		t.Error("running the dismissal by name did not dismiss the warning")
	}
	if out.mode != ModeList {
		t.Errorf("the dismissal left mode %v", out.mode)
	}
}

// TestBindingsShareOneCapturedTarget is the captured-target review item: a
// keypress is gated and executed against one snapshot, so it cannot be checked
// against one row and run against another.
func TestBindingsShareOneCapturedTarget(t *testing.T) {
	m := withListRows(testModelWithSessions(),
		session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt"},
		session.RunningSession{TabID: 2, WindowID: 12, Base: "backend", Account: "work", Directory: "/home/user/backend", State: "bolt"},
	)
	m.cursor = 1

	ctx := dashboardBindingContext(m)
	if !ctx.HasTarget || ctx.Target.TabID != 2 {
		t.Fatalf("captured context = %+v, want the second row", ctx)
	}

	// Enter is the one row-targeted key left, so it is the one that can carry
	// this claim. The command it reaches focuses the captured tab; running the
	// returned cmd against a recording client is what shows which row the
	// execution actually got.
	client := newUITestKittyClient(nil)
	m.client = client

	updated, cmd := m.Update(dashboardKeyPress(keyEnter))
	if out := updated.(Model); out.mode != ModeList {
		t.Errorf("enter left mode %v", out.mode)
	}
	if cmd == nil {
		t.Fatal("enter issued no command on a session row")
	}
	cmd()

	calls := client.CallsFor("FocusTab")
	if len(calls) != 1 {
		t.Fatalf("FocusTab calls = %v, want exactly one", calls)
	}
	if want := kitty.MatchTabByID(2); calls[0].Match != want {
		t.Errorf("enter focused %q, want the captured row's %q", calls[0].Match, want)
	}
}

// TestProjectSwitcherIsNotADashboardBindingState is what replaced the rail's
// "a scope row is not a session" guard. The rail was a pane inside ModeList, so
// the binding context had to answer for a state in which the keyboard was on a
// project scope rather than a session. The switcher is its own mode, so no
// dashboard binding is dispatched while a scope is being chosen at all — and the
// binding context has exactly one answer again.
func TestProjectSwitcherIsNotADashboardBindingState(t *testing.T) {
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	if ctx := dashboardBindingContext(m); !ctx.HasTarget || ctx.Target.TabID != 1 {
		t.Fatalf("binding context = %+v", ctx)
	}
	updated, _ := m.openProjectSwitcher()
	open := updated.(Model)
	if open.mode != ModeProjectSwitcher {
		t.Fatalf("open left mode %v", open.mode)
	}
	// The dashboard keymap is not consulted in this mode: the key handler
	// dispatches on the mode before it reaches the binding table. The keys named
	// here are surviving dashboard bindings that the switcher declares nothing
	// for, so an answer could only have come from the binding table. The other
	// five — `enter`, `/`, `esc`, `p`, `q` — are the switcher's own keys, and
	// pressing them here would prove nothing about the guard.
	for _, key := range []string{keyL, keyInspector, keyQuestion} {
		after := pressKey(open, key)
		if after.mode != ModeProjectSwitcher || after.targetSession.TabID != 0 {
			t.Errorf("%q reached a dashboard binding: mode=%v target=%d", key, after.mode, after.targetSession.TabID)
		}
	}
}

// TestFooterDimsWhatTheCursorCannotDo is the footer half of "preservation of
// locks", restated for a footer that no longer draws a destructive key. The row
// band is one entry now — `a actions` — and the claim it carries is the same
// one: the footer says what the cursor can reach before the operator commits to
// the keystroke, and the overlay behind it is where a locked row's refusal is
// drawn.
func TestFooterDimsWhatTheCursorCannotDo(t *testing.T) {
	locked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt", Locked: true,
	})
	empty := testModelWithSessions()

	byKey := map[string]footerEntry{}
	for _, e := range listFooterBindings() {
		byKey[e.key] = e
	}
	actions, ok := byKey["a"]
	if !ok {
		t.Fatal("the footer no longer declares the actions overlay")
	}
	// A locked row is still a row: the overlay opens and refuses inside itself,
	// which is what TestLockedRowRefusesThroughTheActionsOverlay asserts.
	if !actions.availFn(locked) {
		t.Error("footer dims `a` on a locked row; the overlay is where the lock is answered")
	}
	if actions.availFn(empty) {
		t.Error("footer draws `a` live with no row under the cursor")
	}
	for _, e := range listFooterBindings() {
		for _, retired := range []string{keyR, keyC, keyClose, keyE, keyClearStatus, keyLock, keyM} {
			if e.key == retired {
				t.Errorf("footer still declares the retired key %q (%s)", e.key, e.label)
			}
		}
	}
}

// TestFooterKeysAreTheKeysThatDispatch closes the loop the derivation exists
// for: a footer entry backed by a command names the key that actually runs it.
func TestFooterKeysAreTheKeysThatDispatch(t *testing.T) {
	bindings := dashboardBindings()
	declared := map[string]bool{}
	for _, binding := range bindings {
		declared[binding.Key()] = true
	}
	// The footer also draws four things that are not commands: cursor movement,
	// needs-you navigation, the actions overlay, the command palette, and the
	// pane switch. Those name keys the table does not, and they are listed here
	// so a command key that quietly stops being bound cannot hide among them.
	nonCommand := map[string]bool{
		"j/k": true, "n/N": true, "a": true, ":": true, "tab": true,
	}
	for _, e := range listFooterBindings() {
		if nonCommand[e.key] {
			continue
		}
		if !declared[e.key] {
			t.Errorf("footer draws %q (%s), which the binding table does not dispatch", e.key, e.label)
		}
	}
}

// retiredDashboardKeys is every spelling the old dashboard answered and the new
// one does not, taken from `docs/dashboard-keymap-inventory.md`. It is written
// out rather than derived, because deriving it from the binding table would
// make it say only "keys that are not bound", which is true of every key on the
// keyboard and proves nothing about the ones that used to be.
//
// The list is the union of both inventory tables: the command-backed keys whose
// capability moved to the actions overlay or the palette, and the
// cursor/structure keys whose capability moved to another key. `g`/`G` are here
// and `home`/`end` are not, which is the whole substitution.
var retiredDashboardKeys = []string{
	// Session actions, now entries in the `a` overlay.
	"e", "r", "c", "C", "x", "X", "o", "O", "y", "L", "m",
	// Surfaces, now palette entries.
	"P", "W", "A", "T", "d", "v", "R", "u", "f", "b",
	// Cursor and structure keys with a successor spelling.
	"g", "G", "ctrl+d", "ctrl+u", "left", "right",
	// The bracket chords and their focus-on-arrival variants.
	"]", "[", "}", "{",
	// Chip focus and removal, now editing inside `/`.
	"backspace", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	// The second spelling of focus and of fold.
	keySpace,
}

// TestEveryRetiredDashboardKeyIsInertAtTheList is the migration's closing
// assertion, and the one the per-family retirement tests cannot make between
// them: pressing any retired spelling at the list does nothing observable at
// all — no mode change, no overlay, no command, no filter edit, no view flip,
// no toast.
//
// Inertness is measured against the untouched model rather than against a list
// of things that must not happen, so a key that starts doing something new
// fails here even if nobody thought to check for that particular effect.
// bindingOutcome is deliberately wider than any one command touches, which is
// what makes the comparison worth more than its individual fields.
//
// Every state in dashboardBindingStates is swept, because several of these keys
// were guarded rather than unconditional in the old keymap: a key that only
// fired on a locked row, an unconfigured project, or a visible banner would
// look inert in the one state a narrower test happened to pick.
func TestEveryRetiredDashboardKeyIsInertAtTheList(t *testing.T) {
	bound := map[string]bool{}
	for _, binding := range dashboardBindings() {
		for _, key := range binding.Keys {
			bound[key] = true
		}
	}

	for _, key := range retiredDashboardKeys {
		if bound[key] {
			t.Errorf("%q is listed as retired but the binding table still declares it", key)
		}
	}

	for _, tc := range dashboardBindingStates(t) {
		for _, key := range retiredDashboardKeys {
			t.Run(tc.name+"/"+key, func(t *testing.T) {
				before := observeBinding(tc.m, nil)
				after := runViaKey(tc.m, key)
				if after != before {
					t.Errorf("retired %q is still live:\n  before: %+v\n  after:  %+v", key, before, after)
				}
			})
		}
	}
}

// TestEveryCatalogCommandIsAvailableSomewhere is the "no unavailable commands"
// review item stated as a test. Retiring a direct key moved every one of these
// capabilities behind the palette, which makes the palette the only way some of
// them can be reached: a command that is visible-but-never-available there is
// not a dimmed row the operator can reason about, it is a capability that left
// the build without anybody noticing.
//
// Availability is per state, so the claim is existential rather than universal:
// each command must pass its gates in at least one of the states swept here. A
// command that fails is either genuinely unreachable, or gated on a state this
// list does not build — and the fix for the second is to build that state here,
// which keeps the list an inventory of what the gates actually depend on.
func TestEveryCatalogCommandIsAvailableSomewhere(t *testing.T) {
	states := dashboardBindingStates(t)

	// Two gates need a state the binding sweep has no reason to build: an undo
	// entry that has not expired, and the Projects projection.
	closed := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 7, WindowID: 17, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	closed.pendingUndo = []undoEntry{{
		snapshot: config.Session{Title: "api", Directory: "/home/user/api"},
		account:  config.Account{Name: "work", Harness: "claude"},
		deadline: time.Now().Add(time.Hour),
		label:    "api",
	}}
	states = append(states, struct {
		name string
		m    Model
	}{"pending undo", closed})

	projects := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 8, WindowID: 18, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	projects.listView = dashboard.ViewProjects
	states = append(states, struct {
		name string
		m    Model
	}{"projects view", projects})

	for _, candidate := range commandCatalog() {
		t.Run(string(candidate.ID), func(t *testing.T) {
			for _, tc := range states {
				ctx := captureCommandContext(tc.m)
				if candidate.IsVisible(tc.m, ctx) && candidate.AvailabilityFor(tc.m, ctx).Available {
					return
				}
			}
			t.Errorf("%s is visible or available in none of the %d swept states, so nothing can run it", candidate.ID, len(states))
		})
	}
}

// TestEveryFinalDashboardBindingDispatches is the positive half of the same
// claim. Non-dispatch is only half a keymap: a table whose keys were all inert
// would pass the sweep above perfectly.
//
// It asserts reachability rather than effect — that the key is claimed by the
// dispatcher at all — because what each binding does is already pinned, per
// state and against the palette, by TestDirectKeyAndPaletteAgree and
// TestUncheckedBindingsRunTheirCommandRegardless.
func TestEveryFinalDashboardBindingDispatches(t *testing.T) {
	// A row under the cursor, so the checked bindings' gates can pass.
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})

	for _, binding := range dashboardBindings() {
		for _, key := range binding.Keys {
			t.Run(string(binding.Command)+"/"+key, func(t *testing.T) {
				if _, _, claimed := m.dispatchDashboardBinding(key); !claimed {
					t.Fatalf("%q (%s) was not claimed by the dispatcher", key, binding.Command)
				}
			})
		}
	}
}

// TestInspectorKeyMatchesThePaletteFromEitherPath is the latent divergence the
// shared body closed: the direct key and the palette entry used to reconcile
// different state after toggling the pane, so the same command left two
// different models depending on how it was reached.
func TestInspectorKeyMatchesThePaletteFromEitherPath(t *testing.T) {
	base := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	base.width, base.height = 200, 60
	base.cfg.Defaults.Preview = config.PreviewConfig{MinCols: 80, MinRows: 20, Ratio: 0.4, RefreshMs: 500, Direction: "vertical"}
	base.listView = dashboard.ViewUrgency

	// Both directions, because the two halves of the toggle touch different
	// state: hiding releases the read target, showing re-acquires it and takes
	// the focus.
	for _, name := range []string{"hide", "show"} {
		viaKey := runViaKey(base, keyInspector)
		viaPalette := runViaPalette(base, commands.ToggleInspector)
		if viaKey != viaPalette {
			t.Errorf("inspector %s diverges:\n  key:     %+v\n  palette: %+v", name, viaKey, viaPalette)
		}
		base.inspectorDisplay = inspectorHidden
	}
}
