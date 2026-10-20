package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// projectScopeTestModel is a dashboard with three projects across two profiles,
// one of which reports a casing the filter does not ("API" vs "api"), sized wide
// enough for the inspector to share the row with the queue. The switcher is an
// overlay rather than a column, so nothing here has to make room for it.
func projectScopeTestModel() Model {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Project: "api", FamilyLabel: "API", Profile: "work", State: "key"},
		{TabID: 2, WindowID: 22, Project: "docs", Profile: "work", State: "bolt"},
		{TabID: 3, WindowID: 33, Project: "notes", Profile: "personal", State: "stop"},
	})
	m.width = 240
	m.height = 30
	return m
}

// railTestModel is projectScopeTestModel under the name the mouse and frame
// tests reach it by. It is kept as an alias rather than renamed at every call
// site because those tests are about geometry the project scopes no longer
// participate in at all.
func railTestModel() Model { return projectScopeTestModel() }

func scopeLabels(scopes []dashboard.ProjectScope) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, scope.Label)
	}
	return out
}

// TestProjectSwitcherOpensOnTheAppliedScopeAndNavigates covers the overlay's
// entry point and its cursor: `p` opens it preselecting what the filter already
// names, j/k walk the list, and g/G reach both ends.
func TestProjectSwitcherOpensOnTheAppliedScopeAndNavigates(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m.activeFilter = dashboard.Parse("#docs")
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.rebuildItems()

	m = pressKey(m, keyProjectSwitcher)
	if m.mode != ModeProjectSwitcher {
		t.Fatalf("%q left mode %v", keyProjectSwitcher, m.mode)
	}
	scopes := projectSwitcherScopes(m)
	if got := scopeLabels(scopes); len(got) != 4 || got[0] != "All projects" {
		t.Fatalf("scopes = %v, want All plus three projects", got)
	}
	// The applied selector is what the cursor opens on, matched case-insensitively
	// against the label the session reported.
	if scopes[projectSwitcherCursor(m)].Label != "docs" {
		t.Fatalf("opened on %q, want the applied scope", scopes[projectSwitcherCursor(m)].Label)
	}

	m = pressKey(m, keyG)
	if projectSwitcherCursor(m) != 0 {
		t.Fatalf("g landed on %d, want All", projectSwitcherCursor(m))
	}
	m = pressKey(m, keyJ)
	afterDown := projectSwitcherCursor(m)
	if afterDown != 1 {
		t.Fatalf("j landed on %d, want 1", afterDown)
	}
	m = pressKey(m, keyK)
	if projectSwitcherCursor(m) != 0 {
		t.Fatalf("k landed on %d, want 0", projectSwitcherCursor(m))
	}
	m = pressKey(m, keyGG)
	if got, want := projectSwitcherCursor(m), len(scopes)-1; got != want {
		t.Fatalf("G landed on %d, want %d", got, want)
	}
	// Neither end wraps, and neither walks past the list.
	m = pressKey(m, keyJ)
	if got, want := projectSwitcherCursor(m), len(scopes)-1; got != want {
		t.Fatalf("j past the end landed on %d, want %d", got, want)
	}
}

// TestProjectSwitcherAppliesOneComponentAndPreservesTheRest is the apply path:
// enter swaps Filter.Project, leaves every other chip alone, re-serializes
// through the shared path so a label with a space comes back quoted, and lands
// the dashboard cursor back on the session it was on.
func TestProjectSwitcherAppliesOneComponentAndPreservesTheRest(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m.activeFilter = dashboard.Parse("@work !attention")
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.rebuildItems()

	m = pressKey(m, keyProjectSwitcher)
	// The contextual aggregation: `@work !attention` leaves one session, so the
	// switcher offers All plus the one project that still has something in it.
	if got := scopeLabels(projectSwitcherScopes(m)); len(got) != 2 || got[1] != "API" {
		t.Fatalf("contextual scopes = %v, want All plus API", got)
	}
	m = pressKey(m, keyJ)
	m = pressKey(m, keyEnter)

	if m.mode != ModeProjectSwitcher && m.mode != ModeList {
		t.Fatalf("apply left mode %v", m.mode)
	}
	if m.mode != ModeList {
		t.Fatalf("enter did not close the overlay: mode %v", m.mode)
	}
	// The label's own casing, not the case-folded identity.
	if m.activeFilter.Project != "API" {
		t.Fatalf("applied project = %q", m.activeFilter.Project)
	}
	if m.activeFilter.Account != "work" || m.activeFilter.State != "attention" {
		t.Fatalf("apply dropped a sibling component: %+v", m.activeFilter)
	}
	if !strings.Contains(m.activeFilter.Raw, "#API") || !strings.Contains(m.activeFilter.Raw, "@work") {
		t.Fatalf("re-serialized filter = %q", m.activeFilter.Raw)
	}

	// All is the way back out, and it changes nothing but the project selector.
	m = pressKey(m, keyProjectSwitcher)
	m = pressKey(m, keyG)
	m = pressKey(m, keyEnter)
	if m.activeFilter.Project != "" || m.activeFilter.Account != "work" || m.activeFilter.State != "attention" {
		t.Fatalf("All changed more than the project selector: %+v", m.activeFilter)
	}
}

// TestProjectSwitcherQuotesLabelsThatNeedIt pins the serialization path rather
// than a second spelling of it: a label with a space in it must come back out of
// the filter parser as the same one project.
func TestProjectSwitcherQuotesLabelsThatNeedIt(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Project: "wide", FamilyLabel: "wide project", Profile: "work", State: "key"},
		{TabID: 2, WindowID: 22, Project: "docs", Profile: "work", State: "bolt"},
	})
	m.width = 200
	m.height = 30

	m = pressKey(m, keyProjectSwitcher)
	scopes := projectSwitcherScopes(m)
	idx := -1
	for i, scope := range scopes {
		if scope.Label == "wide project" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no wide-label scope in %v", scopeLabels(scopes))
	}
	m.projectSwitcherKey = scopes[idx].Key()
	m = pressKey(m, keyEnter)

	if !strings.Contains(m.activeFilter.Raw, `#"wide project"`) {
		t.Fatalf("unquoted wide label: %q", m.activeFilter.Raw)
	}
	if reparsed := dashboard.Parse(m.activeFilter.Raw); reparsed.Project != "wide project" {
		t.Fatalf("round trip produced project %q", reparsed.Project)
	}
	if len(m.filtered) != 1 || m.filtered[0].WindowID != 11 {
		t.Fatalf("quoted scope filtered to %d rows", len(m.filtered))
	}
}

// TestProjectSwitcherCancelsWithoutTouchingTheFilter covers the three ways out
// that are not enter. Each restores ModeList, leaves the applied scope exactly
// as it was, and drops the transient query rather than carrying it into the next
// time the overlay is opened.
func TestProjectSwitcherCancelsWithoutTouchingTheFilter(t *testing.T) {
	t.Parallel()

	for _, key := range []string{keyEsc, keyQ, keyProjectSwitcher} {
		t.Run(key, func(t *testing.T) {
			m := projectScopeTestModel()
			m.activeFilter = dashboard.Parse("#docs @work")
			m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
			m.rebuildItems()
			before := m.activeFilter
			beforeRows := len(m.filtered)

			m = pressKey(m, keyProjectSwitcher)
			m = pressKey(m, keySlash)
			m = pressKey(m, keyJ) // move off the applied scope inside the overlay
			m = pressKey(m, keyEsc)
			m = pressKey(m, key)

			if m.mode != ModeList {
				t.Fatalf("%q left mode %v", key, m.mode)
			}
			if m.activeFilter.Project != before.Project || m.activeFilter.Account != before.Account || m.activeFilter.Raw != before.Raw {
				t.Fatalf("%q mutated the filter: %+v", key, m.activeFilter)
			}
			if len(m.filtered) != beforeRows {
				t.Fatalf("%q reapplied the filter: %d rows, want %d", key, len(m.filtered), beforeRows)
			}
			if m.projectSwitcherQuery != "" || m.projectSwitcherSearch {
				t.Fatalf("%q kept transient search state: %q searching=%v", key, m.projectSwitcherQuery, m.projectSwitcherSearch)
			}
		})
	}
}

// TestProjectSwitcherSearchNarrowsAndKeepsAllReachable covers the search
// sub-mode: typed text narrows the list, Esc peels the input while keeping what
// it produced, All survives every query, and a query matching nothing still
// leaves a way out rather than an empty overlay.
func TestProjectSwitcherSearchNarrowsAndKeepsAllReachable(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m = pressKey(m, keyProjectSwitcher)
	m = pressKey(m, keySlash)
	if !m.projectSwitcherSearch {
		t.Fatal("/ did not enter the search input")
	}
	for _, r := range "OT" { // case-folded against "notes"
		m = pressKey(m, string(r))
	}
	if m.projectSwitcherQuery != "OT" {
		t.Fatalf("query = %q", m.projectSwitcherQuery)
	}
	if got := scopeLabels(projectSwitcherScopes(m)); len(got) != 2 || got[0] != "All projects" || got[1] != "notes" {
		t.Fatalf("narrowed scopes = %v", got)
	}

	// Esc leaves the input, keeps the narrowing, and the following Esc cancels.
	m = pressKey(m, keyEsc)
	if m.projectSwitcherSearch || m.projectSwitcherQuery != "OT" || m.mode != ModeProjectSwitcher {
		t.Fatalf("esc from input: searching=%v query=%q mode=%v", m.projectSwitcherSearch, m.projectSwitcherQuery, m.mode)
	}

	m = pressKey(m, keySlash)
	m = pressKey(m, keyBackspace)
	if m.projectSwitcherQuery != "O" {
		t.Fatalf("backspace left query %q", m.projectSwitcherQuery)
	}
	for _, r := range "zzz" {
		m = pressKey(m, string(r))
	}
	scopes := projectSwitcherScopes(m)
	if len(scopes) != 1 || scopes[0].Project != "" {
		t.Fatalf("no-match query offered %v, want All alone", scopeLabels(scopes))
	}
	// Applying the only row left is applying All, which is the way out of the
	// filter rather than a refusal.
	m.activeFilter = dashboard.Parse("#docs")
	m = pressKey(m, keyEsc)
	m = pressKey(m, keyEnter)
	if m.mode != ModeList || m.activeFilter.Project != "" {
		t.Fatalf("no-match apply = mode %v project %q", m.mode, m.activeFilter.Project)
	}
}

// TestProjectSwitcherSelectionSurvivesARefreshThatReordersScopes is the reason
// the selection is stored as an identity rather than an index. A refresh that
// changes which project leads the priority sort must move the cursor with the
// project the operator was looking at, not leave it on whatever row inherited
// the number.
func TestProjectSwitcherSelectionSurvivesARefreshThatReordersScopes(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m = pressKey(m, keyProjectSwitcher)
	m = pressKey(m, keyGG)
	last := projectSwitcherScopes(m)[projectSwitcherCursor(m)]
	if last.Project == "" {
		t.Fatal("G landed on All; the fixture no longer has real scopes")
	}

	// Reverse the live set. Aggregation is priority-ordered, so a state change on
	// the leading session is what actually reorders the list.
	reversed := make([]session.RunningSession, 0, len(m.sessions))
	for i := len(m.sessions) - 1; i >= 0; i-- {
		reversed = append(reversed, m.sessions[i])
	}
	reversed[0].State = "key"
	m = loadSessions(m, reversed)

	scopes := projectSwitcherScopes(m)
	cursor := projectSwitcherCursor(m)
	if scopes[cursor].Label != last.Label {
		t.Fatalf("refresh moved the selection from %q to %q", last.Label, scopes[cursor].Label)
	}

	// A scope that disappears falls back to All rather than to a neighbour.
	kept := make([]session.RunningSession, 0, len(m.sessions))
	for _, row := range m.sessions {
		if !strings.EqualFold(dashboard.ProjectLabel(row), last.Label) {
			kept = append(kept, row)
		}
	}
	m = loadSessions(m, kept)
	if got := projectSwitcherScopes(m)[projectSwitcherCursor(m)].Project; got != "" {
		t.Fatalf("vanished scope fell back to %q, want All", got)
	}
}

// TestProjectSwitcherLeavesTheSessionSurfaceAlone states the modal boundary: an
// overlay key that names a session action on the dashboard must not reach the
// dashboard, and the overlay must not move the list cursor behind itself.
func TestProjectSwitcherLeavesTheSessionSurfaceAlone(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m = pressKey(m, keyProjectSwitcher)
	beforeCursor := m.cursor
	beforeFilter := m.activeFilter
	for _, key := range []string{keyA, keySpace, keyClose, keyR, keyC, keyClearStatus, keyColon, keyQuestion, keyL} {
		after := pressKey(m, key)
		if after.mode != ModeProjectSwitcher {
			t.Fatalf("%q escaped the overlay into mode %v", key, after.mode)
		}
		if after.cursor != beforeCursor || after.activeFilter.Project != beforeFilter.Project {
			t.Fatalf("%q reached the dashboard: cursor %d filter %+v", key, after.cursor, after.activeFilter)
		}
		m = after
	}
}

// TestProjectSwitcherSurvivesAResizeAndPaintsInsideTheBody pins the overlay's
// own geometry. It is centred in the body area and clipped to it, so a terminal
// short enough to leave no room must still produce a box that fits rather than
// one that overruns the footer.
func TestProjectSwitcherSurvivesAResizeAndPaintsInsideTheBody(t *testing.T) {
	t.Parallel()

	m := projectScopeTestModel()
	m = pressKey(m, keyProjectSwitcher)
	m = pressKey(m, keyGG)
	selected := projectSwitcherScopes(m)[projectSwitcherCursor(m)].Label

	for _, size := range []struct{ w, h int }{{240, 30}, {100, 24}, {60, 12}, {40, 9}} {
		updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = updated.(Model)
		if m.mode != ModeProjectSwitcher {
			t.Fatalf("%dx%d closed the overlay", size.w, size.h)
		}
		if got := projectSwitcherScopes(m)[projectSwitcherCursor(m)].Label; got != selected {
			t.Fatalf("%dx%d moved the selection to %q", size.w, size.h, got)
		}
		box := renderProjectSwitcherBox(m)
		lines := strings.Split(box, "\n")
		if len(lines) > rootBodyHeight(m) {
			t.Fatalf("%dx%d box is %d rows, body holds %d", size.w, size.h, len(lines), rootBodyHeight(m))
		}
		for _, line := range lines {
			if w := layout.StyledWidth(line); w > m.width {
				t.Fatalf("%dx%d box line is %d cells wide, terminal is %d", size.w, size.h, w, m.width)
			}
		}
	}
}
