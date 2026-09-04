package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func mouseFixture(t *testing.T) (*uitestDriver, *uitestKittyClient) {
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
	hits := renderDashboardWithHits(m).hitMap.clicks
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
	m := testModel()
	view := m.View()
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
		t.Fatalf("ModeList mouse = mode %v handler %v", view.MouseMode, view.OnMouse != nil)
	}
	if view.Content != m.render() {
		t.Fatal("hit-aware ModeList render changed the existing string-rendering result")
	}
	for mode := ModeSearch; mode <= ModeASLResponse; mode++ {
		m.mode = mode
		view = m.View()
		if view.MouseMode != tea.MouseModeNone || view.OnMouse != nil {
			t.Fatalf("mode %v unexpectedly exposes mouse mode %v handler %v", mode, view.MouseMode, view.OnMouse != nil)
		}
	}
}

func TestMouseSessionSelectionDoubleClickAndInterveningTarget(t *testing.T) {
	d, client := mouseFixture(t)
	notes := findMouseHit(t, d.model, mouseHitSession, func(hit mouseHitRegion) bool { return hit.identity.WindowID == 61 })
	clickHit(t, d, notes)
	if selected, ok := d.model.selectedSession(); !ok || selected.WindowID != 61 || d.model.railFocused {
		t.Fatalf("single click selection = %+v ok=%v railFocused=%v", selected, ok, d.model.railFocused)
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

func TestMouseRailChipHeaderAndWheelRouting(t *testing.T) {
	d, _ := mouseFixture(t)
	apiRail := findMouseHit(t, d.model, mouseHitRailRow, func(hit mouseHitRegion) bool { return hit.project == "api" })
	clickHit(t, d, apiRail)
	if d.model.activeFilter.Project != "api" || !d.model.railFocused {
		t.Fatalf("rail click filter=%+v focused=%v", d.model.activeFilter, d.model.railFocused)
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

	hitMap := renderDashboardWithHits(d.model).hitMap
	d.model.cursor = 0
	if err := d.Mouse(hitMap.listPane.X, hitMap.listPane.Y, UITestMouseWheelDown); err != nil {
		t.Fatal(err)
	}
	if d.model.cursor != 3 {
		t.Fatalf("list wheel cursor = %d, want 3", d.model.cursor)
	}
	d.model.railCursor = 0
	if err := d.Mouse(hitMap.railPane.X, hitMap.railPane.Y, UITestMouseWheelDown); err != nil {
		t.Fatal(err)
	}
	if d.model.railCursor != 3 {
		t.Fatalf("rail wheel cursor = %d, want 3", d.model.railCursor)
	}
}

func TestMouseGeometryWrappedChipsSuppressedRailAndPreview(t *testing.T) {
	d, _ := mouseFixture(t)
	d.model.activeFilter = dashboard.Parse(`界界 alpha beta gamma #"wide project" @personal`)
	d.model.width = 36
	d.model.height = 30
	d.model.applyActiveFilter(dashboard.CursorIdentity{})
	rendered := renderDashboardWithHits(d.model)
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
	if rendered.hitMap.hasRail {
		t.Fatal("narrow layout exposed a suppressed rail hit pane")
	}
	for _, hit := range rendered.hitMap.clicks {
		if hit.kind == mouseHitRailToggle {
			t.Fatalf("narrow header exposed omitted control hit kind %v", hit.kind)
		}
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

	for _, direction := range []string{"horizontal", "vertical"} {
		d.model = railTestModel()
		d.model.width = 220
		d.model.height = 40
		d.model.cfg.Defaults.Preview.Direction = direction
		d.model.cfg.Defaults.Preview.Ratio = .4
		d.model.previewEnabled = true
		before := d.model.cursor
		g := dashboardGeometry(d.model)
		mapNow := renderDashboardWithHits(d.model).hitMap
		x, y := 0, mapNow.listPane.Y+g.listHeight+1
		if direction == "horizontal" {
			x = mapNow.listPane.X + g.listWidth + 2
			y = mapNow.listPane.Y
		}
		if err := d.Mouse(x, y, UITestMouseWheelDown); err != nil {
			t.Fatal(err)
		}
		if d.model.cursor != before {
			t.Fatalf("%s preview wheel moved list cursor to %d", direction, d.model.cursor)
		}
	}
}

func TestMouseIgnoresUnsupportedEventsAndOutsideCoordinates(t *testing.T) {
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
