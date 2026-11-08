package tui

import (
	"fmt"
	"slices"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the interaction half of the Projects view: the palette entry
// that switches the arrangement, Enter's folding, and the three things that
// have to survive a rebuild — which line the cursor is on, which families are
// folded, and what the preview pane is pointed at.
//
// Both of the vocabularies it used to drive are retired. `v` switched the
// arrangement and is a palette command now; `←`/`→` folded and unfolded and are
// Enter alone, which toggles. Enter is unambiguous here because a summary is the
// one line where it has no session to focus, so the key that means "act on this
// row" already meant nothing else on the row folding applies to.
//
// Every assertion here is written against the item list rather than against a
// session index. That is the whole point of the projection: under Projects a
// line is not a session, so a test that counted sessions would agree with the
// model right up to the first fold and then quietly stop describing the screen.

// toggleView switches the arrangement the way the operator now can: through the
// palette. It is a helper rather than an inlined call because this file switches
// views a dozen times and the route is the thing under test in exactly one of
// them.
func toggleView(t *testing.T, m Model) Model {
	t.Helper()
	return pressDashboardCommand(t, m, commands.ToggleListView)
}

// foldToggle presses the one key that changes a fold. Enter on a summary toggles
// it; Enter anywhere else is FocusSession, which is why every caller here parks
// the cursor on the line it means first.
func foldToggle(m Model) Model { return pressKey(m, keyEnter) }

// listViewModel returns a model holding two multi-checkout families and one
// unfamilied session, arranged under the given view.
//
// The shape is deliberate. "monorepo" spans two urgency groups so its summary
// has a lead worth resolving; "toolbox" sits entirely in Idle so a lead that
// merely picked the most urgent row cannot be mistaken for one that picked the
// first; and "etl" belongs to no family, so a top-level leaf is present at a
// list position the collapse keys must decline to act on.
func listViewModel(view dashboard.ViewMode) Model {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
	}
	m := NewModel(cfg, nil)
	m.width, m.height = 160, 50
	m.cfg.Defaults.Preview = config.PreviewConfig{
		Direction: "horizontal", Ratio: 0.5, RefreshMs: 500, MinCols: 100, MinRows: 30,
	}
	m.listView = view
	return loadSessions(m, listViewRows())
}

func listViewRows() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/repo/api", Branch: "main", State: "key", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, WindowID: 12, Base: "web", Account: "work", Directory: "/repo/web", Branch: "feat", State: "bolt", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 3, WindowID: 13, Base: "cli", Account: "work", Directory: "/tools/cli", State: "", Harness: "claude", FamilyKey: "git:/tools", FamilyLabel: "toolbox"},
		{TabID: 4, WindowID: 14, Base: "lib", Account: "work", Directory: "/tools/lib", State: "", Harness: "claude", FamilyKey: "git:/tools", FamilyLabel: "toolbox"},
		{TabID: 5, WindowID: 15, Base: "etl", Account: "work", Directory: "/etl", State: "bolt", Harness: "codex"},
	}
}

// itemLines describes the projection the way the screen reads it: one entry per
// line, a summary named by its label and a session by its base. Assertions in
// this file compare these rather than indices, so a failure says which list came
// out instead of which number did.
func itemLines(m Model) []string {
	out := make([]string, 0, m.listItemCount())
	for i := 0; i < m.listItemCount(); i++ {
		it, _ := m.items.At(i)
		if s, ok := m.items.SessionAt(i); ok {
			out = append(out, "session:"+s.Base)
			continue
		}
		fold := "open"
		if it.Collapsed {
			fold = "folded"
		}
		out = append(out, fmt.Sprintf("summary:%s(%s)", it.FamilyLabel, fold))
	}
	return out
}

func selectedLine(m Model) string {
	lines := itemLines(m)
	if m.cursor < 0 || m.cursor >= len(lines) {
		return fmt.Sprintf("<cursor %d outside %d lines>", m.cursor, len(lines))
	}
	return lines[m.cursor]
}

// selectLine parks the cursor on the named line, the way an operator would
// arrive at it, and fails the test when the line is not on screen.
func selectLine(t *testing.T, m Model, want string) Model {
	t.Helper()
	for i, line := range itemLines(m) {
		if line == want {
			m.cursor = i
			scrollListToCursor(&m)
			return m
		}
	}
	t.Fatalf("line %q is not in the list %v", want, itemLines(m))
	return m
}

func wantLines(t *testing.T, m Model, want []string) {
	t.Helper()
	if got := itemLines(m); !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
}

var (
	urgencyLines = []string{"session:api", "session:web", "session:etl", "session:cli", "session:lib"}
	projectLines = []string{
		"summary:monorepo(open)", "session:api", "session:web",
		"session:etl",
		"summary:toolbox(open)", "session:cli", "session:lib",
	}
)

// TestListStartsInUrgencyWithEveryFamilyExpanded pins the launch state. Both
// halves matter: the view is the arrangement the dashboard has always opened in,
// and the collapse map is empty rather than restored, because absent means
// expanded and nothing carries a fold across a process boundary.
func TestListStartsInUrgencyWithEveryFamilyExpanded(t *testing.T) {
	t.Parallel()

	fresh := NewModel(&config.Config{}, nil)
	if fresh.listView != dashboard.ViewUrgency {
		t.Errorf("a fresh model opened in view %v, want Urgency", fresh.listView)
	}
	if len(fresh.collapsedFamilies) != 0 {
		t.Errorf("a fresh model already holds folds: %v", fresh.collapsedFamilies)
	}

	m := listViewModel(dashboard.ViewUrgency)
	wantLines(t, m, urgencyLines)

	// And the first thing the operator can do about it — switch views — finds
	// every family open, which is the claim a persisted fold would break.
	m = toggleView(t, m)
	wantLines(t, m, projectLines)
}

// TestToggleListViewIsReachableOnlyThroughThePalette is the retirement of `v`
// stated as a test. Two claims: the letter dispatches nothing in ModeList, and
// the palette entry still performs the whole switch — the field, the projection,
// and the cursor mapping — so the capability moved rather than went away.
func TestToggleListViewIsReachableOnlyThroughThePalette(t *testing.T) {
	t.Parallel()

	byKey := listViewModel(dashboard.ViewUrgency)
	byKey = selectLine(t, byKey, "session:lib")
	before := itemLines(byKey)
	after := pressKey(byKey, "v")
	if after.listView != dashboard.ViewUrgency {
		t.Fatalf("`v` still switched the list to %v", after.listView)
	}
	wantLines(t, after, before)
	if after.mode != ModeList {
		t.Fatalf("`v` left mode %v", after.mode)
	}

	// The palette entry is the whole switch: the field, the projection, and the
	// summary/session cursor mapping that a bare field write would skip.
	byPalette := listViewModel(dashboard.ViewUrgency)
	byPalette = selectLine(t, byPalette, "session:lib")
	cmd := commandByID(t, commands.ToggleListView)
	ctx := captureCommandContext(byPalette)
	if !cmd.AvailabilityFor(byPalette, ctx).Available {
		t.Fatal("the view command is unavailable on a session row")
	}
	byPalette = toggleView(t, byPalette)
	if byPalette.listView != dashboard.ViewProjects {
		t.Fatalf("the palette entry left the list in view %v, want Projects", byPalette.listView)
	}
	wantLines(t, byPalette, projectLines)
	if got := selectedLine(byPalette); got != "session:lib" {
		t.Errorf("the palette landed the cursor on %q, want session:lib", got)
	}

	byPalette = toggleView(t, byPalette)
	if byPalette.listView != dashboard.ViewUrgency {
		t.Fatalf("the palette entry did not switch back; view = %v", byPalette.listView)
	}
	wantLines(t, byPalette, urgencyLines)
}

// TestViewSwitchKeepsAVisibleSelection is the ordinary case: a session is a line
// in both arrangements, so switching views moves it up or down the screen but
// never off the operator's row.
func TestViewSwitchKeepsAVisibleSelection(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewUrgency)
	m = selectLine(t, m, "session:lib")

	m = toggleView(t, m)
	if got := selectedLine(m); got != "session:lib" {
		t.Errorf("switching to Projects moved the selection to %q, want session:lib", got)
	}

	m = toggleView(t, m)
	if got := selectedLine(m); got != "session:lib" {
		t.Errorf("switching back to Urgency moved the selection to %q, want session:lib", got)
	}
}

// TestSummarySelectionMapsToItsLeadOnReturnToUrgency covers the one selection
// that has no counterpart on the other side. Urgency draws no summaries, so the
// cursor has to land on a session — and it lands on the one whose state the
// summary was painting, not on whichever member happens to be listed first.
func TestSummarySelectionMapsToItsLeadOnReturnToUrgency(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)

	m = selectLine(t, m, "summary:monorepo(open)")
	m = toggleView(t, m)
	if got := selectedLine(m); got != "session:api" {
		t.Errorf("the monorepo summary mapped to %q, want session:api (its attention lead)", got)
	}

	// toolbox is entirely idle, so its lead is decided by source order rather
	// than by urgency — which is what makes this the other half of the claim.
	m = toggleView(t, m)
	m = selectLine(t, m, "summary:toolbox(open)")
	m = toggleView(t, m)
	if got := selectedLine(m); got != "session:cli" {
		t.Errorf("the toolbox summary mapped to %q, want session:cli (its first member)", got)
	}
}

// TestChildHiddenByAFoldMapsToItsSummary is the mapping in the other direction:
// entering Projects with a session selected whose family is folded lands on the
// line that stands for it, because that line is where the row now lives.
func TestChildHiddenByAFoldMapsToItsSummary(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	m = foldToggle(m)

	// Back in Urgency the folded family's rows are all visible again, so the
	// operator can select one the Projects view has nowhere to draw.
	m = toggleView(t, m)
	m = selectLine(t, m, "session:web")

	m = toggleView(t, m)
	if got := selectedLine(m); got != "summary:monorepo(folded)" {
		t.Errorf("a child hidden by a fold selected %q, want the monorepo summary", got)
	}
	if _, ok := m.selectedSession(); ok {
		t.Error("the summary the cursor landed on resolved to a session")
	}
}
