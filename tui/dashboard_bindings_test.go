package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
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

// refusedForLock reports whether id would refuse m because the selected
// session is locked — the single refusal a dashboard key answers out loud.
func refusedForLock(m Model, id commands.ID) bool {
	candidate, ok := dashboardCommand(id)
	if !ok {
		return false
	}
	ctx := dashboardBindingContext(m)
	if !candidate.IsVisible(m, ctx) {
		return false
	}
	return candidate.AvailabilityFor(m, ctx).Reason == commands.ReasonLocked
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
			// enter and space branch on list structure before they reach the
			// table, and `d` is shared; both are asserted on their own terms.
			if binding.Binds(keyEnter) || binding.Binds(keySpace) || binding.Binds(keyD) {
				continue
			}
			key := binding.Key()
			t.Run(state.name+"/"+key, func(t *testing.T) {
				viaKey := runViaKey(state.m, key)
				viaPalette := runViaPalette(state.m, binding.Command)
				if refusedForLock(state.m, binding.Command) {
					// The one declared divergence. A lock is a decision the
					// operator made and can undo, so the key says so out loud
					// while the palette says it by dimming the row. Nothing
					// else about the two outcomes may differ.
					if !strings.Contains(viaKey.toastTexts, "locked") {
						t.Errorf("%s on a locked row said nothing: toasts %q", binding.Command, viaKey.toastTexts)
					}
					viaKey.toastTexts, viaKey.issuedCmd = viaPalette.toastTexts, viaPalette.issuedCmd
				}
				if viaKey != viaPalette {
					t.Errorf("%s diverges:\n  key:     %+v\n  palette: %+v", binding.Command, viaKey, viaPalette)
				}
			})
		}
	}
}

// TestUncheckedBindingsRunTheirCommandRegardless states what the three declared
// exceptions buy. Each either refuses by doing something the operator can see or
// feel — which a dropped keypress would throw away — or has no state in which it
// refuses at all, so a gate could only ever be a chance to get that wrong.
func TestUncheckedBindingsRunTheirCommandRegardless(t *testing.T) {
	unchecked := map[commands.ID]bool{}
	for _, binding := range dashboardBindings() {
		if binding.Gate == commands.GateUnchecked {
			unchecked[binding.Command] = true
		}
	}
	want := map[commands.ID]bool{commands.ToggleInspector: true, commands.SwitchProject: true, commands.UndoClose: true}
	if len(unchecked) != len(want) {
		t.Fatalf("unchecked bindings: got %v, want %v", unchecked, want)
	}
	for id := range want {
		if !unchecked[id] {
			t.Errorf("%s is no longer declared unchecked; the key now drops its own refusal", id)
		}
	}
}

// TestLockedRowDeniesWithExactlyOneToast is the lock half of the review item.
// The three destructive keys refuse a locked session out loud — once — and
// nothing else about the model moves.
func TestLockedRowDeniesWithExactlyOneToast(t *testing.T) {
	locked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt", Locked: true,
	})
	for _, key := range []string{keyR, keyClose, keyC} {
		t.Run(key, func(t *testing.T) {
			updated, _ := locked.Update(dashboardKeyPress(key))
			out := updated.(Model)
			if out.mode != ModeList {
				t.Errorf("mode moved to %v on a locked row", out.mode)
			}
			if len(out.toasts) != 1 {
				t.Fatalf("expected exactly 1 denial toast, got %d", len(out.toasts))
			}
			if out.toasts[0].Severity != toastError {
				t.Errorf("expected toastError, got %v", out.toasts[0].Severity)
			}
			if !strings.Contains(out.toasts[0].Text, "locked") {
				t.Errorf("expected a toast naming the lock, got %q", out.toasts[0].Text)
			}
		})
	}
}

// TestRefusalIsSilentUnlessItIsALock is the other side of that rule: a lock is
// the one refusal a key announces. Everything else — no target, an inactive
// pane, an unconfigured project — already shows on the row the cursor is on,
// and a toast for each would make an ordinary keystroke noisy.
func TestRefusalIsSilentUnlessItIsALock(t *testing.T) {
	unconfigured := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 4, WindowID: 14, Base: "stray", Account: "nowhere", Directory: "/tmp/stray", State: "",
	})
	for _, key := range []string{keyO, keyOpenEditor, keyE, keyClearStatus} {
		t.Run(key, func(t *testing.T) {
			updated, cmd := unconfigured.Update(dashboardKeyPress(key))
			out := updated.(Model)
			if len(out.toasts) != 0 {
				t.Errorf("expected a silent refusal, got toasts %+v", out.toasts)
			}
			if cmd != nil {
				t.Errorf("expected no command from a refused key")
			}
			if out.mode != ModeList {
				t.Errorf("mode moved to %v on a refused key", out.mode)
			}
		})
	}
}

// TestRestartKeyStillConfirms pins the confirmation half of the review item.
// The direct key must not have become a one-keystroke restart by routing
// through a shared body.
func TestRestartKeyStillConfirms(t *testing.T) {
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 7, WindowID: 17, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	// A stale return mode from an overlay raised somewhere else must not
	// survive into this confirmation: answering it has to come back to the
	// dashboard the key was pressed on.
	m.confirmReturnMode = ModeWorktrees

	updated, _ := m.Update(dashboardKeyPress(keyR))
	out := updated.(Model)
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
// item for the two config-picker keys, which is where a wrong answer strands
// the operator in a mode they never opened.
func TestProfileOverlaysReturnToTheDashboard(t *testing.T) {
	for _, key := range []string{keyC, keyDashboardLaunch} {
		t.Run(key, func(t *testing.T) {
			m := withListRows(testModelWithSessions(), session.RunningSession{
				TabID: 8, WindowID: 18, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
			})
			m.configPickerReturnMode = ModeWorktrees

			updated, _ := m.Update(dashboardKeyPress(key))
			out := updated.(Model)
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

	for _, binding := range dashboardBindings() {
		candidate, ok := dashboardCommand(binding.Command)
		if !ok || candidate.Group != commands.GroupSession {
			continue
		}
		key := binding.Key()
		if key == keyEnter || key == keySpace {
			continue // enter/space fold the family; asserted below.
		}
		t.Run(key, func(t *testing.T) {
			updated, cmd := m.Update(dashboardKeyPress(key))
			out := updated.(Model)
			if out.targetSession.TabID != 0 {
				t.Errorf("%s captured %d as a target from a summary row", binding.Command, out.targetSession.TabID)
			}
			if out.mode != ModeList {
				t.Errorf("%s left the dashboard for %v from a summary row", binding.Command, out.mode)
			}
			if cmd != nil {
				t.Errorf("%s issued a command from a summary row", binding.Command)
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
	for _, key := range []string{keyEnter, keySpace} {
		updated, _ := m.Update(dashboardKeyPress(key))
		if got := updated.(Model).listItemCount(); got == before {
			t.Errorf("%s did not fold the family: item count stayed at %d", key, got)
		}
	}
}

// TestSharedKeyDismissesTheBannerFirst pins the one place where declaration
// order in the table is a behavior. The banner claims `d` while it is visible
// and yields it once it is gone.
func TestSharedKeyDismissesTheBannerFirst(t *testing.T) {
	m := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 9, WindowID: 19, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})
	m.deprecations = []config.Deprecation{{Key: "accounts", Replacement: "profiles"}}

	first, _ := m.Update(dashboardKeyPress(keyD))
	afterBanner := first.(Model)
	if !afterBanner.deprecationDismissed {
		t.Fatalf("first d did not dismiss the banner")
	}
	if afterBanner.mode != ModeList {
		t.Fatalf("first d opened %v instead of dismissing the banner", afterBanner.mode)
	}

	second, _ := afterBanner.Update(dashboardKeyPress(keyD))
	afterDetail := second.(Model)
	if afterDetail.mode != ModeDetail {
		t.Errorf("second d opened %v, want ModeDetail", afterDetail.mode)
	}
	if afterDetail.detailWindowID != 19 {
		t.Errorf("detail opened on window %d, want the row under the cursor", afterDetail.detailWindowID)
	}
}

// TestBindingsShareOneCapturedTarget is the captured-target review item. Both
// claimants of a shared key are offered the same snapshot, so a keypress cannot
// be gated against one row and executed against another.
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

	// Every command reached through the table must act on that snapshot, not on
	// a cursor re-read after the gates ran.
	updated, _ := m.Update(dashboardKeyPress(keyOpenTimeline))
	out := updated.(Model)
	if out.detailWindowID != 12 {
		t.Errorf("timeline opened on window %d, want 12", out.detailWindowID)
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
	// dispatches on the mode before it reaches the binding table.
	for _, key := range []string{keyR, keyClose, keyE, keyOpenTimeline} {
		after := pressKey(open, key)
		if after.mode != ModeProjectSwitcher || after.targetSession.TabID != 0 {
			t.Errorf("%q reached a row binding: mode=%v target=%d", key, after.mode, after.targetSession.TabID)
		}
	}
}

// TestFooterDimsALockedRowsDestructiveKeys is the footer half of "preservation
// of locks". Before the footer derived its availability the three keys rendered
// live on a locked row and then refused when pressed; now the row says so
// before the operator commits to the keystroke.
func TestFooterDimsALockedRowsDestructiveKeys(t *testing.T) {
	locked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt", Locked: true,
	})
	unlocked := withListRows(testModelWithSessions(), session.RunningSession{
		TabID: 3, WindowID: 13, Base: "api", Account: "work", Directory: "/home/user/api", State: "bolt",
	})

	byKey := map[string]footerEntry{}
	for _, e := range listFooterBindings() {
		byKey[e.key] = e
	}
	for _, key := range []string{keyR, keyC, keyClose} {
		entry, ok := byKey[key]
		if !ok {
			t.Fatalf("footer no longer declares %q", key)
		}
		if entry.availFn(locked) {
			t.Errorf("footer draws %q live on a locked row", key)
		}
		if !entry.availFn(unlocked) {
			t.Errorf("footer dims %q on an unlocked row", key)
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
	// The footer also draws things that are not commands — cursor movement, the
	// chip controls, the jump chords, the actions overlay. Those name keys the
	// table does not, and they are listed here so a command key that quietly
	// stops being bound cannot hide among them.
	nonCommand := map[string]bool{
		"j/k": true, "a": true, ":": true, "1-9": true,
		"tab": true, "]/[": true, "}/{": true, "←/→": true,
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
