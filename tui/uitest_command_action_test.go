// This file pins the scenario harness's `command` action: that it reaches a
// capability through the palette the operator uses rather than around it, and
// that each distinct way that path can fail is reported as itself.
//
// The four refusals below — absent, hidden, disabled, ambiguous — all look the
// same from the screen: enter did nothing. A scenario that could not tell them
// apart would keep passing on the day a command is deleted, and would report a
// deletion on the day one is correctly disabled. That is the whole reason the
// driver diagnoses them separately, so it is the whole reason this file exists.

package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

// TestCommandActionDispatchesThroughThePalette is the happy path stated as the
// UI path: the palette opens, the cursor lands on the named row, enter runs it,
// and the palette closes. The Kitty call is the proof the operation itself ran
// rather than the mode merely changing.
func TestCommandActionDispatchesThroughThePalette(t *testing.T) {
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, client := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	if err := d.Apply(UITestAction{Type: UITestActionCommand, CommandID: string(commands.FocusSession)}); err != nil {
		t.Fatalf("focus through the palette: %v", err)
	}
	if got := d.Model().mode; got != ModeList {
		t.Fatalf("palette left open: mode = %q", uitestModeName(got))
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 1 {
		t.Fatalf("FocusTab calls = %d, want 1", len(calls))
	}
}

// TestCommandActionWalksTheCursorWithNavigationKeys proves the row is selected
// by the palette's own navigation rather than by assigning its cursor: the
// command chosen is deliberately not the one the palette opens on, so a driver
// that skipped the walk would run the wrong row.
func TestCommandActionWalksTheCursorWithNavigationKeys(t *testing.T) {
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	if err := d.Key(keyColon); err != nil {
		t.Fatal(err)
	}
	opened := d.Model().commandCursor
	target, err := uitestCommandRowIndex(d.Model().commandResults, commands.SearchPlans, true)
	if err != nil {
		t.Fatal(err)
	}
	if target == opened {
		t.Fatalf("test is vacuous: the palette already opens on row %d", target)
	}
	if err := d.Key(keyEsc); err != nil {
		t.Fatal(err)
	}

	if err := d.Command(commands.SearchPlans); err != nil {
		t.Fatalf("search plans through the palette: %v", err)
	}
	if got := d.Model().mode; got != ModePlanSearch {
		t.Fatalf("mode = %q, want plan_search", uitestModeName(got))
	}
}

// TestCommandActionFailsForAbsentCommand covers an id nothing declares. The
// driver is asked directly because scenario validation rejects such an id
// before a run starts — both gates exist, and this is the second one.
func TestCommandActionFailsForAbsentCommand(t *testing.T) {
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	err = d.Command(commands.ID("session.no-such-command"))
	if err == nil || !strings.Contains(err.Error(), "no command with id") {
		t.Fatalf("absent command error = %v", err)
	}
}

// TestCommandActionFailsForHiddenCommand uses the one command the dashboard
// hides rather than disables: clearing filters is offered only when a filter is
// active, so on an unfiltered list the palette has no row for it at all.
func TestCommandActionFailsForHiddenCommand(t *testing.T) {
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	if !d.Model().activeFilter.IsEmpty() {
		t.Fatal("fixture starts filtered, so clear-filters would be visible")
	}
	err = d.Command(commands.ClearFilters)
	if err == nil || !strings.Contains(err.Error(), "hidden") {
		t.Fatalf("hidden command error = %v", err)
	}
	if got := d.Model().mode; got != ModeCommandPalette {
		t.Fatalf("mode = %q: the failure should be diagnosed with the palette open", uitestModeName(got))
	}
}

// TestCommandActionFailsForDisabledCommandWithoutIssuingIt is the load-bearing
// half of "explicitly fail without issuing the operation": peeking at a stopped
// session's scrollback is refused, and the refusal must arrive with no pager
// call recorded — a scenario that merely observed "nothing changed" could not
// tell a refusal from a peek that opened and closed.
func TestCommandActionFailsForDisabledCommandWithoutIssuingIt(t *testing.T) {
	fx, err := uitestFixtureByName("unseen_stops")
	if err != nil {
		t.Fatal(err)
	}
	d, client := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	sel, ok := d.Model().selectedSession()
	if !ok || sel.State == "bolt" {
		t.Fatalf("fixture selection is not a stopped session: %+v (ok=%v)", sel, ok)
	}
	err = d.Command(commands.PeekScrollback)
	if err == nil || !strings.Contains(err.Error(), commands.ReasonPeekInactive) {
		t.Fatalf("disabled command error = %v", err)
	}
	if calls := client.CallsFor("LaunchOverlayPager"); len(calls) != 0 {
		t.Fatalf("refused peek still issued %d pager call(s)", len(calls))
	}
}

// TestCommandRowResolutionRejectsDuplicateIDs is the ambiguity arm. The
// production catalog cannot produce it — its ids are unique — so the rule is
// pinned on the pure resolver, which is exactly where a future catalog that
// grew a duplicate would be caught.
func TestCommandRowResolutionRejectsDuplicateIDs(t *testing.T) {
	rows := []commands.Command[Model]{
		{ID: commands.ShowKeyboardHelp, Title: commands.StaticTitle[Model]("Keyboard shortcuts")},
		{ID: commands.SearchPlans, Title: commands.StaticTitle[Model]("Plans")},
		{ID: commands.ShowKeyboardHelp, Title: commands.StaticTitle[Model]("Keyboard shortcuts (again)")},
	}
	_, err := uitestCommandRowIndex(rows, commands.ShowKeyboardHelp, true)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate id error = %v", err)
	}
	if index, err := uitestCommandRowIndex(rows, commands.SearchPlans, true); err != nil || index != 1 {
		t.Fatalf("unique id resolution = %d, %v", index, err)
	}
	if _, err := uitestCommandRowIndex(rows, commands.Quit, true); err == nil || !strings.Contains(err.Error(), "hidden") {
		t.Fatalf("declared-but-absent error = %v", err)
	}
	if _, err := uitestCommandRowIndex(rows, commands.Quit, false); err == nil || !strings.Contains(err.Error(), "no command with id") {
		t.Fatalf("undeclared error = %v", err)
	}
}

// TestCommandActionRefusesWhileTheRailHoldsFocus pins the one dashboard state
// where the palette is unreachable by key. `:` is not in the rail's keymap, so
// a scenario that entered the rail and then named a command would otherwise
// fail with "the palette did not open", which describes the symptom rather than
// the cause.
func TestCommandActionRefusesWhileTheRailHoldsFocus(t *testing.T) {
	fx, err := uitestFixtureByName("project_families")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	if err := d.Key(keyLeft); err != nil {
		t.Fatal(err)
	}
	if !d.Model().railFocused {
		t.Fatal("left did not move focus into the rail")
	}
	err = d.Command(commands.SearchPlans)
	if err == nil || !strings.Contains(err.Error(), "rail") {
		t.Fatalf("rail-focused error = %v", err)
	}
}

// TestCommandActionRefusesOutsideTheDashboard states the scope of the action:
// the palette is a dashboard surface, so a command named from inside another
// screen is a scenario-authoring mistake and says so by name.
func TestCommandActionRefusesOutsideTheDashboard(t *testing.T) {
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	if err := d.Command(commands.SearchPlans); err != nil {
		t.Fatalf("enter Plans: %v", err)
	}
	err = d.Command(commands.SearchAllSessions)
	if err == nil || !strings.Contains(err.Error(), "plan_search") {
		t.Fatalf("wrong-mode error = %v", err)
	}
}

// TestCommandScenarioActionRunsEndToEnd runs the action through the scenario
// surface rather than the driver method, so the JSON field, the validator, and
// Apply's dispatch are covered by the same claim.
func TestCommandScenarioActionRunsEndToEnd(t *testing.T) {
	sc, err := parseUITestScenario([]byte(`{
		"name": "command action",
		"fixture": "basic",
		"size": {"width": 100, "height": 32},
		"actions": [{"type": "command", "command_id": "explore.show-runs"}],
		"assertions": [{"type": "mode_equals", "mode": "asl_runs"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunUITestScenario(sc, UITestScenarioEnv{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.OK {
		t.Fatalf("scenario failed: %+v", result.Failures)
	}
}
