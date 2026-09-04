package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the root half of the Projects projection: that the view the model
// holds is the view the projection, the renderer, and the scroll math all act
// under, and that a summary the real builder produced refuses exactly what the
// hand-spliced one in model_summary_item_test.go does.

// projectViewModel returns a model holding two sessions of one Git family plus
// an unfamilied session, arranged under the given view.
func projectViewModel(view dashboard.ViewMode) Model {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{{Name: "work", Color: "blue"}},
	}
	m := NewModel(cfg, kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""))
	m.width, m.height = 120, 40
	m.listView = view
	return loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/repo/api", State: "key", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, WindowID: 12, Base: "web", Account: "work", Directory: "/repo/web", State: "bolt", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 3, WindowID: 13, Base: "etl", Account: "work", Directory: "/etl", State: "bolt", Harness: "codex"},
	})
}

// TestRebuildItemsFollowsTheView asserts the stored projection is the one the
// current view names, and that it is re-derived rather than left behind: the
// urgency view still projects one line per session, and Projects folds the
// family into a summary plus its two members.
func TestRebuildItemsFollowsTheView(t *testing.T) {
	urgency := projectViewModel(dashboard.ViewUrgency)
	if got, want := urgency.listItemCount(), 3; got != want {
		t.Errorf("urgency projected %d lines, want %d (one per session)", got, want)
	}
	for i := 0; i < urgency.listItemCount(); i++ {
		if it, _ := urgency.items.At(i); !it.IsSession() {
			t.Errorf("urgency line %d is a %v; the urgency view folds nothing", i, it.Kind)
		}
	}

	projects := projectViewModel(dashboard.ViewProjects)
	if got, want := projects.listItemCount(), 4; got != want {
		t.Fatalf("Projects projected %d lines, want %d (a summary, its two members, and the orphan)", got, want)
	}
	summary, _ := projects.items.At(0)
	if summary.Kind != dashboard.ItemFamilySummary || summary.FamilyLabel != "monorepo" {
		t.Errorf("Projects line 0 = %+v, want the monorepo summary", summary)
	}

	// Switching the view on a model that already holds rows reprojects them:
	// the projection is never a record of the view it was first built under.
	switched := urgency
	switched.listView = dashboard.ViewProjects
	switched.rebuildItems()
	if got, want := switched.listItemCount(), projects.listItemCount(); got != want {
		t.Errorf("reprojecting after a view switch gave %d lines, want %d", got, want)
	}

	// And the collapse map reaches the builder from the model, so a folded family
	// is a shorter list rather than a flag nothing reads.
	folded := projects
	folded.collapsedFamilies = map[string]bool{"git:/repo": true}
	folded.rebuildItems()
	if got, want := folded.listItemCount(), 2; got != want {
		t.Errorf("folding the family left %d lines, want %d (the summary and the orphan)", got, want)
	}
}

// TestProjectsViewRendersTheTreeAndOmitsHeadings drives the real root renderer,
// which is what proves the view reaches ListInput rather than only the builder.
func TestProjectsViewRendersTheTreeAndOmitsHeadings(t *testing.T) {
	projects := stripAnsi(renderList(projectViewModel(dashboard.ViewProjects)))
	if !strings.Contains(projects, "monorepo") {
		t.Errorf("the family label never reached the rendered list:\n%s", projects)
	}
	if strings.Contains(projects, "git:/repo") {
		t.Errorf("the family key leaked into the rendered list:\n%s", projects)
	}
	for _, heading := range []string{"Needs Attention (", "Active (", "Idle ("} {
		if strings.Contains(projects, heading) {
			t.Errorf("Projects drew the %q heading:\n%s", heading, projects)
		}
	}

	urgency := stripAnsi(renderList(projectViewModel(dashboard.ViewUrgency)))
	if !strings.Contains(urgency, "Needs Attention (") {
		t.Errorf("the same rows under the urgency view lost their headings:\n%s", urgency)
	}
	if strings.Contains(urgency, "monorepo") {
		t.Errorf("the urgency view drew a family summary:\n%s", urgency)
	}
}

// TestProjectsScrollMathChargesTheChromeItDraws is the pagination claim at the
// root: with no headings drawn, the cost from offset to cursor is the plain item
// count, and the cursor the model scrolls to is one the renderer emits.
func TestProjectsScrollMathChargesTheChromeItDraws(t *testing.T) {
	m := projectViewModel(dashboard.ViewProjects)
	if listShowsGroupHeadings(&m) {
		t.Fatal("the Projects view claims it draws urgency headings")
	}
	if got, want := listLineCost(&m, 0, m.listItemCount()-1, listShowsGroupHeadings(&m)), m.listItemCount(); got != want {
		t.Errorf("line cost over the whole Projects list = %d, want %d", got, want)
	}

	// The urgency view over the same rows still charges its headings, so the
	// difference above is the view's and not the fixture's.
	u := projectViewModel(dashboard.ViewUrgency)
	if !listShowsGroupHeadings(&u) {
		t.Fatal("the urgency view over multi-group rows claims it draws no headings")
	}
	if got := listLineCost(&u, 0, u.listItemCount()-1, listShowsGroupHeadings(&u)); got <= u.listItemCount() {
		t.Errorf("urgency line cost = %d, want more than the %d items (headings and separators)", got, u.listItemCount())
	}

	// Every cursor position stays inside the window the model scrolls it into,
	// at a height short enough that scrolling is actually required.
	m.height = 12
	for cursor := 0; cursor < m.listItemCount(); cursor++ {
		m.cursor = cursor
		scrollListToCursor(&m)
		if !listCursorFitsWindow(&m, m.listOffset, listVisibleHeight(m), listShowsGroupHeadings(&m)) {
			t.Errorf("cursor %d does not fit the window at offset %d", cursor, m.listOffset)
		}
	}
}

// TestBuiltSummaryRefusesTheSelectedSession closes the loop on the fail-closed
// guarantee. model_summary_item_test.go proves every row action refuses against
// a hand-spliced summary; this asserts the summary the real builder now emits is
// the same kind of thing, so that proof still covers what the dashboard draws.
func TestBuiltSummaryRefusesTheSelectedSession(t *testing.T) {
	m := projectViewModel(dashboard.ViewProjects)
	m.cursor = 0

	if it, ok := m.selectedItem(); !ok || it.Kind != dashboard.ItemFamilySummary {
		t.Fatalf("cursor 0 selected %+v (ok=%v), want the builder's summary", it, ok)
	}
	if s, ok := m.selectedSession(); ok {
		t.Errorf("the built summary resolved to session %+v; it must resolve to none", s)
	}
	if hasRow(m) {
		t.Error("hasRow reported a row with the cursor on a built summary")
	}
	if ctx := captureCommandContext(m); ctx.HasTarget {
		t.Errorf("captureCommandContext claimed target %+v on a built summary", ctx.Target)
	}

	// Stepping onto a member resolves again, so the refusal is about the kind.
	m = pressKey(m, "j")
	if s, ok := m.selectedSession(); !ok || s.TabID != 1 {
		t.Errorf("the first member resolved to %+v (ok=%v), want tab 1", s, ok)
	}
}
