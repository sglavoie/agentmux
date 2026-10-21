//go:build tuitest

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/kitty/kittytest"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func mouseFixture(t *testing.T) (*uitestDriver, *kittytest.Client) {
	t.Helper()
	fx, err := uitestFixtureByName("project_families")
	if err != nil {
		t.Fatal(err)
	}
	d, client := newUITestFixtureDriver(fx, fx.Size)
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close driver: %v", err)
		}
	})
	return d, client
}

func findMouseHit(t *testing.T, m Model, kind mouseHitKind, match func(mouseHitRegion) bool) mouseHitRegion {
	t.Helper()
	hits := renderDashboardFrame(m).hitMap.clicks
	for _, hit := range hits {
		if hit.kind == kind && (match == nil || match(hit)) {
			return hit
		}
	}
	t.Fatalf("mouse hit kind %v not found in %+v", kind, hits)
	return mouseHitRegion{}
}

func clickHit(t *testing.T, d *uitestDriver, hit mouseHitRegion) {
	t.Helper()
	if err := d.Mouse(hit.rect.X, hit.rect.Y, UITestMouseLeft); err != nil {
		t.Fatalf("mouse click at %d,%d: %v", hit.rect.X, hit.rect.Y, err)
	}
}

func TestViewEnablesMouseOnlyForLiveDashboard(t *testing.T) {
	t.Parallel()

	m := testModel()
	view := m.View()
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
		t.Fatalf("ModeList mouse = mode %v handler %v", view.MouseMode, view.OnMouse != nil)
	}
	if view.Content != m.render() {
		t.Fatal("hit-aware ModeList render changed the existing string-rendering result")
	}
	// The project switcher is the second mouse-live mode: its scope rows are
	// clickable, and they are published by the same single render pass that
	// produces the overlay's text.
	m.mode = ModeProjectSwitcher
	view = m.View()
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
		t.Fatalf("ModeProjectSwitcher mouse = mode %v handler %v", view.MouseMode, view.OnMouse != nil)
	}
	for mode := ModeSearch; mode <= ModeProjectSwitcher; mode++ {
		if mode == ModeProjectSwitcher {
			continue
		}
		m.mode = mode
		view = m.View()
		if view.MouseMode != tea.MouseModeNone || view.OnMouse != nil {
			t.Fatalf("mode %v unexpectedly exposes mouse mode %v handler %v", mode, view.MouseMode, view.OnMouse != nil)
		}
	}
}

func TestMouseSessionSelectionDoubleClickAndInterveningTarget(t *testing.T) {
	t.Parallel()

	d, client := mouseFixture(t)
	notes := findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })
	clickHit(t, d, notes)
	if selected, ok := d.model.selectedSession(); !ok || selected.WindowID != 61 {
		t.Fatalf("single click selection = %+v ok=%v", selected, ok)
	}
	if got := len(client.CallsFor("FocusTab")); got != 0 {
		t.Fatalf("single click issued %d FocusTab calls", got)
	}
	clickHit(t, d, findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 }))
	if got := len(client.CallsFor("FocusTab")); got != 1 {
		t.Fatalf("double click issued %d FocusTab calls, want 1", got)
	}

	d2, client2 := mouseFixture(t)
	clickHit(t, d2, findMouseHit(t, d2.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 }))
	clickHit(t, d2, findMouseHit(t, d2.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 71 }))
	clickHit(t, d2, findMouseHit(t, d2.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 }))
	if got := len(client2.CallsFor("FocusTab")); got != 0 {
		t.Fatalf("intervening target left %d FocusTab calls", got)
	}
}

func TestMouseDoubleClickBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		gap  time.Duration
		want int
	}{{"at boundary", 400 * time.Millisecond, 1}, {"past boundary", 401 * time.Millisecond, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			d, client := mouseFixture(t)
			now := time.Unix(100, 0)
			d.model.now = func() time.Time { return now }
			hit := findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })
			clickHit(t, d, hit)
			now = now.Add(tc.gap)
			clickHit(t, d, findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 }))
			if got := len(client.CallsFor("FocusTab")); got != tc.want {
				t.Fatalf("FocusTab calls = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMouseStaleAndReorderedSessionTargets(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	hit := findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })
	view := d.model.View()
	cmd := view.OnMouse(tea.MouseClickMsg{X: hit.rect.X, Y: hit.rect.Y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("rendered session did not produce a command")
	}
	msg := cmd()

	// Reordering between render and delivery still resolves the captured session
	// by identity, not by the row now occupying its old coordinates.
	for i, j := 0, len(d.model.filtered)-1; i < j; i, j = i+1, j-1 {
		d.model.filtered[i], d.model.filtered[j] = d.model.filtered[j], d.model.filtered[i]
	}
	d.model.rebuildItems()
	updated, _ := d.model.Update(msg)
	reordered := updated.(Model)
	if selected, ok := reordered.selectedSession(); !ok || selected.WindowID != 61 {
		t.Fatalf("reordered click selected %+v ok=%v", selected, ok)
	}

	// Removing the captured identity makes the same message inert rather than
	// redirecting it to whichever row inherited the screen coordinate.
	kept := append([]session.RunningSession(nil), reordered.filtered...)
	for i, row := range kept {
		if row.WindowID == 61 {
			kept = append(kept[:i], kept[i+1:]...)
			break
		}
	}
	reordered.filtered = kept
	reordered.rebuildItems()
	reordered.cursor = 0
	before := dashboard.CaptureItemIdentity(reordered.items, reordered.cursor)
	updated, cmd2 := reordered.Update(msg)
	stale := updated.(Model)
	if cmd2 != nil || !sameMouseIdentity(before, dashboard.CaptureItemIdentity(stale.items, stale.cursor)) {
		t.Fatalf("stale click changed cursor or emitted command: before=%+v after=%+v cmd=%v", before, dashboard.CaptureItemIdentity(stale.items, stale.cursor), cmd2 != nil)
	}
}

func TestMouseSummaryBodyAndChevronHaveDistinctActions(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	d.model.setListView(dashboard.ViewProjects)
	body := findMouseHit(t, d.model, mouseHitFamily, func(hit mouseHitRegion) bool { return hit.identity.FamilyKey == "git:api" })
	clickHit(t, d, body)
	if item, ok := d.model.selectedItem(); !ok || item.Kind != dashboard.ItemFamilySummary || item.FamilyKey != "git:api" || item.Collapsed {
		t.Fatalf("summary body selected %+v ok=%v", item, ok)
	}
	chevron := findMouseHit(t, d.model, mouseHitFamilyChevron, func(hit mouseHitRegion) bool { return hit.identity.FamilyKey == "git:api" })
	clickHit(t, d, chevron)
	if !d.model.collapsedFamilies["git:api"] {
		t.Fatal("summary chevron did not collapse family")
	}
}

// findRootMouseHit is findMouseHit for a mode composed by renderRootFrame rather
// than by the dashboard frame alone. The switcher publishes its rows from the
// root composition because that is where its overlay placement is known.
func findRootMouseHit(t *testing.T, m Model, kind mouseHitKind, match func(mouseHitRegion) bool) mouseHitRegion {
	t.Helper()
	hits := renderRootFrame(m).hitMap.clicks
	for _, hit := range hits {
		if hit.kind == kind && (match == nil || match(hit)) {
			return hit
		}
	}
	t.Fatalf("root mouse hit kind %v not found in %+v", kind, hits)
	return mouseHitRegion{}
}

// TestMouseProjectSwitcherHeaderChipAndScopeRow walks the pointer through the
// whole scope path: the header control opens the overlay, a scope row applies
// itself, and the chip the apply produced removes it again.
func TestMouseProjectSwitcherHeaderChipAndScopeRow(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	d.Resize(240, 32)

	clickHit(t, d, findMouseHit(t, d.model, mouseHitProjectSwitcher, nil))
	if d.model.mode != ModeProjectSwitcher {
		t.Fatalf("header control left mode %v", d.model.mode)
	}
	apiScope := findRootMouseHit(t, d.model, mouseHitProjectScope, func(hit mouseHitRegion) bool { return hit.project == "api" })
	clickHit(t, d, apiScope)
	if d.model.activeFilter.Project != "api" || d.model.mode != ModeList {
		t.Fatalf("scope click filter=%+v mode=%v", d.model.activeFilter, d.model.mode)
	}
	chip := findMouseHit(t, d.model, mouseHitChip, func(hit mouseHitRegion) bool { return hit.chipLabel == "#api" })
	clickHit(t, d, chip)
	if d.model.activeFilter.Project != "" {
		t.Fatalf("chip click left project %q", d.model.activeFilter.Project)
	}

	viewHit := findMouseHit(t, d.model, mouseHitView, nil)
	clickHit(t, d, viewHit)
	if d.model.listView != dashboard.ViewProjects {
		t.Fatalf("view click left %v", d.model.listView)
	}
	clickHit(t, d, findMouseHit(t, d.model, mouseHitView, nil))
	if d.model.listView != dashboard.ViewUrgency {
		t.Fatalf("second view click left %v", d.model.listView)
	}

	hitMap := renderDashboardFrame(d.model).hitMap
	d.model.cursor = 0
	if err := d.Mouse(hitMap.listPane.X, hitMap.listPane.Y, UITestMouseWheelDown); err != nil {
		t.Fatal(err)
	}
	if d.model.cursor != 3 {
		t.Fatalf("list wheel cursor = %d, want 3", d.model.cursor)
	}
}

// TestMouseProjectSwitcherIgnoresEverythingButItsOwnRows states the modal
// boundary for the pointer. While the overlay is up, the dashboard's own hit
// regions are behind it: a click landing where a session row used to be must do
// nothing rather than reach through the dimmed base.
func TestMouseProjectSwitcherIgnoresEverythingButItsOwnRows(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	d.Resize(240, 32)
	sessionHit := findMouseHit(t, d.model, mouseHitSession, nil)
	before := d.model.cursor

	if err := d.Key(keyProjectSwitcher); err != nil {
		t.Fatal(err)
	}
	clickHit(t, d, sessionHit)
	if d.model.mode != ModeProjectSwitcher || d.model.cursor != before {
		t.Fatalf("click through the overlay: mode=%v cursor=%d", d.model.mode, d.model.cursor)
	}
	if err := d.Mouse(sessionHit.rect.X, sessionHit.rect.Y, UITestMouseWheelDown); err != nil {
		t.Fatal(err)
	}
	if d.model.cursor != before {
		t.Fatalf("wheel through the overlay moved the list cursor to %d", d.model.cursor)
	}
}

func TestMouseGeometryWrappedChipsAndPreview(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	d.model.activeFilter = dashboard.Parse(`界界 alpha beta gamma #"wide project" @personal`)
	d.model.width = 36
	d.model.height = 30
	d.model.applyActiveFilter(dashboard.CursorIdentity{})
	rendered := renderDashboardFrame(d.model)
	maxChipY := -1
	for _, hit := range rendered.hitMap.clicks {
		if hit.kind == mouseHitChip && hit.rect.Y > maxChipY {
			maxChipY = hit.rect.Y
		}
	}
	firstChip := findMouseHit(t, d.model, mouseHitChip, nil)
	if maxChipY <= firstChip.rect.Y {
		t.Fatal("wide Unicode/ANSI chips did not wrap into multiple hit rows")
	}
	wideChip := findMouseHit(t, d.model, mouseHitChip, func(hit mouseHitRegion) bool { return hit.chipLabel == "/界界" })
	if err := d.Mouse(wideChip.rect.X+wideChip.rect.Width-1, wideChip.rect.Y, UITestMouseLeft); err != nil {
		t.Fatal(err)
	}
	for _, component := range d.model.activeFilter.Components() {
		if component.Label() == "/界界" {
			t.Fatal("clicking the final display cell of a wide-glyph chip missed its hit region")
		}
	}

	// The wheel acts where the pointer is. Over the inspector column it scrolls
	// output and the queue cursor must not move — the two panes are side by side
	// now, so a wheel event that fell through to the list would scroll the wrong
	// thing while the operator is looking at the other one.
	d.model = railTestModel()
	d.model.width = 220
	d.model.height = 40
	d.model.cfg.Defaults.Preview.Direction = "horizontal"
	d.model.cfg.Defaults.Preview.Ratio = .4
	requireInspector(t, d.model)
	before := d.model.cursor
	g := dashboardGeometry(d.model)
	mapNow := renderDashboardFrame(d.model).hitMap
	if !mapNow.hasInspector {
		t.Fatal("the split frame recorded no inspector hit region")
	}
	if err := d.Mouse(mapNow.listPane.X+g.listWidth+2, mapNow.listPane.Y, UITestMouseWheelDown); err != nil {
		t.Fatal(err)
	}
	if d.model.cursor != before {
		t.Fatalf("the inspector wheel moved the list cursor to %d", d.model.cursor)
	}
}

func TestMouseIgnoresUnsupportedEventsAndOutsideCoordinates(t *testing.T) {
	t.Parallel()

	d, _ := mouseFixture(t)
	hit := findMouseHit(t, d.model, mouseHitSession, nil)
	view := d.model.View()
	for _, msg := range []tea.MouseMsg{
		tea.MouseClickMsg{X: hit.rect.X, Y: hit.rect.Y, Button: tea.MouseRight},
		tea.MouseClickMsg{X: hit.rect.X, Y: hit.rect.Y, Button: tea.MouseLeft, Mod: tea.ModShift},
		tea.MouseReleaseMsg{X: hit.rect.X, Y: hit.rect.Y, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: hit.rect.X, Y: hit.rect.Y, Button: tea.MouseLeft},
		tea.MouseClickMsg{X: -1, Y: -1, Button: tea.MouseLeft},
		tea.MouseWheelMsg{X: d.model.width + 1, Y: d.model.height + 1, Button: tea.MouseWheelDown},
	} {
		if cmd := view.OnMouse(msg); cmd != nil {
			t.Fatalf("unsupported mouse event %T produced a command", msg)
		}
	}
}

// TestMouseInspectorClickTakesTheKeysAndBreaksAPendingDoubleClick is the pane
// half of mouse targeting. The inspector has no selectable rows, so a click in
// it does exactly two things: it aims the keyboard at the pane, and it ends
// whatever double-click chain the queue had going. The second half is the one
// that would bite — a click in the other pane is the operator's attention
// leaving the queue, so the next click on a row starts a new chain rather than
// completing the old one and focusing a terminal nobody asked for.
func TestMouseInspectorClickTakesTheKeysAndBreaksAPendingDoubleClick(t *testing.T) {
	t.Parallel()

	d, client := mouseFixture(t)
	requireInspector(t, d.model)

	row := func() mouseHitRegion {
		return findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })
	}
	clickHit(t, d, row())
	cursor := d.model.cursor

	pane := renderDashboardFrame(d.model).hitMap
	if !pane.hasInspector {
		t.Fatal("the split frame published no inspector region")
	}
	if err := d.Mouse(pane.inspectorPane.X+1, pane.inspectorPane.Y+1, UITestMouseLeft); err != nil {
		t.Fatal(err)
	}
	if !d.model.inspectorFocused() {
		t.Fatal("clicking the inspector did not aim the keyboard at it")
	}
	if d.model.cursor != cursor {
		t.Fatalf("clicking the inspector moved the queue cursor to %d", d.model.cursor)
	}

	clickHit(t, d, row())
	if got := len(client.CallsFor("FocusTab")); got != 0 {
		t.Fatalf("the click across the divider left the chain intact: %d FocusTab calls", got)
	}
	if d.model.inspectorFocused() {
		t.Fatal("clicking a queue row left the keyboard in the inspector")
	}
}

// TestMouseOverAFullScreenInspectorReachesNoQueueRow is the other targeting
// rule, and it is about a pane that is not there. Full screen draws no queue
// column, so no queue row may be clickable — a click at the coordinate a row
// occupied before the pane took the body would otherwise select whatever the
// hit map still remembered being there.
func TestMouseOverAFullScreenInspectorReachesNoQueueRow(t *testing.T) {
	t.Parallel()

	d, client := mouseFixture(t)
	requireInspector(t, d.model)
	row := findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })

	d.model.inspectorDisplay = inspectorFullScreen
	frame := renderDashboardFrame(d.model)
	if frame.hitMap.hasList {
		t.Fatal("a full-screen inspector published a queue pane")
	}
	for _, hit := range frame.hitMap.clicks {
		switch hit.kind {
		case mouseHitSession, mouseHitFamily, mouseHitFamilyChevron:
			t.Fatalf("a full-screen inspector left a queue region clickable: %+v", hit)
		}
	}
	if !frame.hitMap.hasInspector {
		t.Fatal("a full-screen inspector published no pane of its own")
	}

	cursor := d.model.cursor
	if err := d.Mouse(row.rect.X, row.rect.Y, UITestMouseLeft); err != nil {
		t.Fatal(err)
	}
	if d.model.cursor != cursor {
		t.Fatalf("a click over the full-screen pane moved the cursor to %d", d.model.cursor)
	}
	if got := len(client.CallsFor("FocusTab")); got != 0 {
		t.Fatalf("a click over the full-screen pane issued %d FocusTab calls", got)
	}
}

// railTestModel is projectScopeTestModel under the name the mouse and frame
// tests reach it by. It is kept as an alias rather than renamed at every call
// site because those tests are about geometry the project scopes no longer
// participate in at all.
func railTestModel() Model { return projectScopeTestModel() }
