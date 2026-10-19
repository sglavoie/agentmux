package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
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
func listViewModel(view dashboard.ViewMode) (Model, *kitty.MockRunner) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
	}
	runner := kitty.NewMockRunner(nil)
	m := NewModel(cfg, kitty.NewClientWithSocket(runner, ""))
	m.width, m.height = 160, 50
	m.cfg.Defaults.Preview = config.PreviewConfig{
		Direction: "horizontal", Ratio: 0.5, RefreshMs: 500, MinCols: 100, MinRows: 30,
	}
	m.listView = view
	return loadSessions(m, listViewRows()), runner
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
	if got := itemLines(m); !equalStrings(got, want) {
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
	fresh := NewModel(&config.Config{}, kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""))
	if fresh.listView != dashboard.ViewUrgency {
		t.Errorf("a fresh model opened in view %v, want Urgency", fresh.listView)
	}
	if len(fresh.collapsedFamilies) != 0 {
		t.Errorf("a fresh model already holds folds: %v", fresh.collapsedFamilies)
	}

	m, _ := listViewModel(dashboard.ViewUrgency)
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
	byKey, _ := listViewModel(dashboard.ViewUrgency)
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
	byPalette, _ := listViewModel(dashboard.ViewUrgency)
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
	m, _ := listViewModel(dashboard.ViewUrgency)
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
	m, _ := listViewModel(dashboard.ViewProjects)

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
	m, _ := listViewModel(dashboard.ViewProjects)
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

// TestEnterFoldsOnlyOnASummary walks what is left of the fold vocabulary now
// that it is one key. The interesting part used to be the asymmetry between Left
// on a summary and Left on a child; with a toggle there is no asymmetry to have,
// so the claims that matter are that the key toggles, that it does so without
// touching Kitty, and that on every line which is not a summary it is
// FocusSession and nothing else.
func TestEnterFoldsOnlyOnASummary(t *testing.T) {
	t.Run("enter toggles the selected family without touching kitty", func(t *testing.T) {
		m, runner := listViewModel(dashboard.ViewProjects)
		calls := len(runner.Calls)
		m = selectLine(t, m, "summary:monorepo(open)")

		m = foldToggle(m)
		wantLines(t, m, []string{
			"summary:monorepo(folded)", "session:etl",
			"summary:toolbox(open)", "session:cli", "session:lib",
		})
		if got := selectedLine(m); got != "summary:monorepo(folded)" {
			t.Errorf("folding moved the cursor to %q", got)
		}

		m = foldToggle(m)
		wantLines(t, m, projectLines)
		if got := selectedLine(m); got != "summary:monorepo(open)" {
			t.Errorf("unfolding moved the cursor to %q", got)
		}
		if n := len(runner.Calls); n != calls {
			t.Errorf("folding made %d Kitty call(s) on a summary: %+v", n-calls, runner.Calls[calls:])
		}
	})

	t.Run("enter on a child is a focus, not a fold of its family", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewProjects)
		m = selectLine(t, m, "session:web")

		after := foldToggle(m)
		wantLines(t, after, projectLines) // nothing folded
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter on a child folded %v", after.collapsedFamilies)
		}
		if got := selectedLine(after); got != "session:web" {
			t.Errorf("enter on a child moved the cursor to %q", got)
		}
	})

	t.Run("the retired arrow keys fold nothing", func(t *testing.T) {
		// `←`/`→` were the fold and unfold keys. They are retired outright rather
		// than rebound, so on a summary — the one line they used to act on — they
		// must now change neither the list nor the cursor.
		for _, view := range []dashboard.ViewMode{dashboard.ViewProjects, dashboard.ViewUrgency} {
			m, _ := listViewModel(view)
			line := "summary:monorepo(open)"
			if view == dashboard.ViewUrgency {
				line = "session:api"
			}
			m = selectLine(t, m, line)
			before, cursor := itemLines(m), m.cursor

			for _, key := range []string{keyLeft, keyRight} {
				after := pressKey(m, key)
				wantLines(t, after, before)
				if after.cursor != cursor {
					t.Errorf("view %v: %q moved the cursor to %q", view, key, selectedLine(after))
				}
				if len(after.collapsedFamilies) != 0 {
					t.Errorf("view %v: %q folded %v", view, key, after.collapsedFamilies)
				}
			}
		}
	})

	t.Run("a top-level leaf has no family to fold", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewProjects)
		m = selectLine(t, m, "session:etl")
		before, cursor := itemLines(m), m.cursor

		after := foldToggle(m)
		wantLines(t, after, before)
		if after.cursor != cursor {
			t.Errorf("enter moved the cursor off the unfamilied leaf to %q", selectedLine(after))
		}
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter folded %v from an unfamilied leaf", after.collapsedFamilies)
		}
	})

	t.Run("the urgency view has nothing to fold", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewUrgency)
		m = selectLine(t, m, "session:api")
		after := foldToggle(m)
		wantLines(t, after, urgencyLines)
		if len(after.collapsedFamilies) != 0 {
			t.Errorf("enter folded %v in the urgency view", after.collapsedFamilies)
		}
	})
}

// TestFoldsSurviveRefreshFilterAndViewSwitch is the memory claim. A fold is a
// choice about the list, not about the projection that happened to be in the
// model when it was made, so every rebuild has to carry it — otherwise the next
// two-second topology refresh silently unfolds the screen.
func TestFoldsSurviveRefreshFilterAndViewSwitch(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	m = foldToggle(m)
	folded := []string{
		"summary:monorepo(folded)", "session:etl",
		"summary:toolbox(open)", "session:cli", "session:lib",
	}
	wantLines(t, m, folded)

	// A refresh delivering the same topology.
	m = loadSessions(m, listViewRows())
	wantLines(t, m, folded)
	if got := selectedLine(m); got != "summary:monorepo(folded)" {
		t.Errorf("the refresh moved the cursor to %q", got)
	}

	// A filter that hides the folded family entirely, then its removal. The
	// fold outlives the family's absence from the list.
	filtered := m
	cmd := filtered.applyFilterChange(dashboard.Parse("tools"))
	_ = cmd
	for _, line := range itemLines(filtered) {
		if strings.Contains(line, "monorepo") {
			t.Fatalf("the filter left %q in the list %v", line, itemLines(filtered))
		}
	}
	filtered.applyFilterChange(dashboard.Filter{})
	wantLines(t, filtered, folded)

	// And a round trip through the urgency view, which draws no summaries at
	// all and so has nowhere to record a fold except the map.
	m = toggleView(t, m)
	wantLines(t, m, urgencyLines)
	m = toggleView(t, m)
	wantLines(t, m, folded)
}

// TestFoldingCopiesTheCollapseMap guards the aliasing hazard behind that memory.
// Model is copied by value on every Update, so a fold written into a shared map
// would reach into every copy that still holds it — including the pre-fold model
// a test, or an in-flight command, is holding.
func TestFoldingCopiesTheCollapseMap(t *testing.T) {
	before, _ := listViewModel(dashboard.ViewProjects)
	before = selectLine(t, before, "summary:monorepo(open)")

	after := foldToggle(before)
	if !after.collapsedFamilies["git:/repo"] {
		t.Fatal("the fold did not reach the model that pressed the key")
	}
	if before.collapsedFamilies["git:/repo"] {
		t.Error("the fold reached back into the model held before the keypress")
	}

	// The second fold is the one that can alias: by then the map exists, so a
	// write in place would land in every copy that shares it rather than only in
	// the one the update loop is about to return.
	second := selectLine(t, after, "summary:toolbox(open)")
	second = foldToggle(second)
	if !second.collapsedFamilies["git:/tools"] {
		t.Fatal("the second fold did not reach the model that pressed the key")
	}
	if after.collapsedFamilies["git:/tools"] {
		t.Error("the second fold reached back into the model held before the keypress")
	}
	if got := itemLines(after); !equalStrings(got, []string{
		"summary:monorepo(folded)", "session:etl",
		"summary:toolbox(open)", "session:cli", "session:lib",
	}) {
		t.Errorf("the earlier model's own projection changed under it: %v", got)
	}

	// Unfolding deletes rather than storing false, so an unfolded family leaves
	// no entry claiming anything about it.
	restored := foldToggle(selectLine(t, after, "summary:monorepo(folded)"))
	if _, present := restored.collapsedFamilies["git:/repo"]; present {
		t.Errorf("unfolding left %v behind; absent must mean expanded", restored.collapsedFamilies)
	}
}

// TestProjectsNavigationAtListBoundaries drives the cursor over the ends of the
// list under every fold arrangement that changes its length. The bound is the
// item count, which is what a folded family makes smaller than the session
// count — a clamp still charging sessions would let the cursor sit past the end.
func TestProjectsNavigationAtListBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		folds  []string
		length int
	}{
		{"all expanded", nil, 7},
		{"one folded", []string{"summary:monorepo(open)"}, 5},
		{"both folded", []string{"summary:monorepo(open)", "summary:toolbox(open)"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := listViewModel(dashboard.ViewProjects)
			for _, line := range tc.folds {
				m = selectLine(t, m, line)
				m = foldToggle(m)
			}
			if got := m.listItemCount(); got != tc.length {
				t.Fatalf("list holds %d lines (%v), want %d", got, itemLines(m), tc.length)
			}

			// Named keys and familiar aliases navigate the same projected list.
			m = pressKey(m, keyHome)
			if m.cursor != 0 {
				t.Errorf("home left the cursor at %d, want the first line", m.cursor)
			}
			m = pressKey(m, keyK)
			if m.cursor != 0 {
				t.Errorf("k at the top wrapped or ran off to %d", m.cursor)
			}

			m = pressKey(m, keyEnd)
			if got, want := m.cursor, tc.length-1; got != want {
				t.Errorf("end left the cursor at %d, want %d", got, want)
			}
			m = pressKey(m, keyJ)
			if got, want := m.cursor, tc.length-1; got != want {
				t.Errorf("j at the bottom ran off to %d, want %d", got, want)
			}

			m = pressKey(m, keyG)
			if m.cursor != 0 {
				t.Fatalf("g did not reach the first line: %d", m.cursor)
			}
			m = pressKey(m, keyGG)
			if m.cursor != tc.length-1 {
				t.Fatalf("G did not reach the last line: %d", m.cursor)
			}

			// Every line is reachable, and every line the cursor stops on is a
			// line the renderer can scroll to.
			m.height = 12
			for i := 0; i < tc.length; i++ {
				m.cursor = i
				scrollListToCursor(&m)
				if !listCursorFitsWindow(&m, m.listOffset, listVisibleHeight(m), listShowsGroupHeadings(&m)) {
					t.Errorf("line %d (%q) does not fit the window at offset %d", i, selectedLine(m), m.listOffset)
				}
			}
		})
	}
}

// TestProjectsNavigationUnderAFilter asserts the projection is built from the
// rows that survived the filter, not from the family as it exists. A family
// filtered down to one visible checkout is a plain leaf: a summary over one row
// would be a claim about a group the operator cannot see the rest of.
func TestProjectsNavigationUnderAFilter(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewProjects)

	m.applyFilterChange(dashboard.Parse("api"))
	wantLines(t, m, []string{"session:api"})
	if m.cursor != 0 {
		t.Errorf("the filter left the cursor at %d over a one-line list", m.cursor)
	}
	if _, ok := m.selectedSession(); !ok {
		t.Error("the lone surviving checkout did not resolve to a session")
	}

	// Widening the filter back to both members restores the summary, and the
	// count it reports is of visible rows only.
	m.applyFilterChange(dashboard.Parse("repo"))
	wantLines(t, m, []string{"summary:monorepo(open)", "session:api", "session:web"})
	summary, _ := m.items.At(0)
	if got := m.items.SummaryCounts(summary, m.lastActivity, m.idleThreshold); got != [3]int{1, 1, 0} {
		t.Errorf("summary counts under the filter = %v, want one attention and one active", got)
	}

	m.applyFilterChange(dashboard.Filter{})
	wantLines(t, m, projectLines)
}

// TestSummaryPreviewPaintsACardAndCallsNoKitty is the pane's half of the
// fail-closed rule. A summary owns no window, so there is nothing to read; the
// card answers the question the selection actually asked instead, out of facts
// already on the rows.
func TestSummaryPreviewPaintsACardAndCallsNoKitty(t *testing.T) {
	m, runner := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	if cmd := m.refreshPreviewForCursor(); cmd != nil {
		if msg, ok := cmd().(previewContentMsg); !ok || msg.windowID != 0 {
			t.Fatalf("the summary retargeted the preview at %#v, want the empty window", msg)
		}
	}
	if m.previewWindowID != 0 {
		t.Errorf("the preview still tracks window %d on a summary", m.previewWindowID)
	}

	calls := len(runner.Calls)
	card := stripAnsi(renderPreviewPane(m, 60, 12))
	if n := len(runner.Calls); n != calls {
		t.Fatalf("painting a project card made %d Kitty call(s): %+v", n-calls, runner.Calls[calls:])
	}

	if !strings.Contains(card, "monorepo") {
		t.Errorf("the card never named the family:\n%s", card)
	}
	if !strings.Contains(card, "1 attention") || !strings.Contains(card, "1 active") {
		t.Errorf("the card lost its tally:\n%s", card)
	}
	for _, want := range []string{"api", "web", "/repo/api", "/repo/web", "claude", "main", "feat"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card lost %q, which is what tells two checkouts apart:\n%s", want, card)
		}
	}
	if strings.Contains(card, "git:/repo") {
		t.Errorf("the family key leaked into the card:\n%s", card)
	}
	if got, want := len(strings.Split(card, "\n")), 12; got != want {
		t.Errorf("the card rendered %d lines, want exactly the %d it was given", got, want)
	}

	// A pane too short for the members says how many it dropped rather than
	// overflowing the height the split gave it.
	short := stripAnsi(renderPreviewPane(m, 60, 4))
	if got, want := len(strings.Split(short, "\n")), 4; got != want {
		t.Fatalf("the short card rendered %d lines, want %d:\n%s", got, want, short)
	}
	if !strings.Contains(short, "more") {
		t.Errorf("the short card dropped members silently:\n%s", short)
	}

	// The whole split renders the card in place of scrollback, still without
	// asking Kitty anything.
	calls = len(runner.Calls)
	split := stripAnsi(renderSplitFrame(m).text)
	if n := len(runner.Calls); n != calls {
		t.Errorf("rendering the split over a summary made %d Kitty call(s): %+v", n-calls, runner.Calls[calls:])
	}
	if !strings.Contains(split, "monorepo") {
		t.Errorf("the split did not paint the project card:\n%s", split)
	}
}

// TestRefreshRetargetsThePreviewAtTheSelectionItEndedOn pins the order inside a
// rebuild. A refresh can move the cursor — a row can vanish, and in Projects a
// row can be replaced by the summary standing for it — so the pane has to be
// pointed at the selection the refresh finished with, not the one it started
// from and not whichever line the cursor passed over on the way.
func TestRefreshRetargetsThePreviewAtTheSelectionItEndedOn(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != 11 {
		t.Fatalf("setup: the preview tracks window %d, want api's 11", m.previewWindowID)
	}

	// The selected checkout goes away, which also drops its family to one
	// visible member and so shortens every line index above it. The cursor
	// index the refresh started from now names a different session entirely —
	// which is exactly the row a pane retargeted too early would show.
	m = loadSessions(m, listViewRows()[1:])
	wantLines(t, m, []string{"session:web", "session:etl", "summary:toolbox(open)", "session:cli", "session:lib"})
	if got := selectedLine(m); got != "session:web" {
		t.Fatalf("the refresh landed the cursor on %q, want session:web", got)
	}
	if m.previewWindowID != 12 {
		t.Errorf("the preview tracks window %d but the cursor ended on %q (window 12)",
			m.previewWindowID, selectedLine(m))
	}

	// And when the refresh ends on a summary — here because the family the
	// cursor was inside is folded — the pane drops its window rather than
	// keeping the one it had.
	m = loadSessions(m, listViewRows())
	m = selectLine(t, m, "summary:monorepo(open)")
	m = foldToggle(m)
	m = toggleView(t, m)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != 11 {
		t.Fatalf("setup: the preview tracks window %d, want api's 11", m.previewWindowID)
	}
	m = toggleView(t, m)
	if got := selectedLine(m); got != "summary:monorepo(folded)" {
		t.Fatalf("the fold did not swallow the selection; cursor on %q", got)
	}
	if m.previewWindowID != 0 {
		t.Errorf("the preview still tracks window %d with a summary selected", m.previewWindowID)
	}
}

// TestPreviewRejectsAResponseForAnOlderTarget is why the fetch carries a
// generation. A window ID alone cannot decide this: the cursor can leave a
// session, visit a summary that owns no window, and come back, and a read issued
// before the detour would match on window ID and repaint the pane with
// scrollback captured for an older selection.
func TestPreviewRejectsAResponseForAnOlderTarget(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()

	stale := previewContentMsg{windowID: m.previewWindowID, targetSeq: m.previewTargetSeq, text: "scrollback for api"}
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if m.previewText != "scrollback for api" {
		t.Fatalf("the current target's response was dropped; previewText = %q", m.previewText)
	}

	// Leave for the summary and come back. The window ID is the same on both
	// sides of the detour; the generation is not.
	m = selectLine(t, m, "summary:monorepo(open)")
	m.refreshPreviewForCursor()
	m = selectLine(t, m, "session:api")
	m.refreshPreviewForCursor()
	if m.previewWindowID != stale.windowID {
		t.Fatalf("the round trip left the preview on window %d, want %d", m.previewWindowID, stale.windowID)
	}
	if m.previewTargetSeq == stale.targetSeq {
		t.Fatal("the round trip reused the generation the stale read was stamped with")
	}

	updated, _ = m.Update(stale)
	m = updated.(Model)
	if m.previewText != "" {
		t.Errorf("a response for an older target repainted the pane with %q", m.previewText)
	}

	// A response for the current generation but the wrong window is refused
	// too: both halves of the identity have to match.
	wrongWindow := previewContentMsg{windowID: stale.windowID + 100, targetSeq: m.previewTargetSeq, text: "someone else's scrollback"}
	updated, _ = m.Update(wrongWindow)
	if got := updated.(Model).previewText; got != "" {
		t.Errorf("a response for another window repainted the pane with %q", got)
	}

	// And the periodic tick re-reads the SAME target under the SAME generation,
	// so a read slower than the tick interval is not superseded before it lands.
	seq := m.previewTargetSeq
	m.previewPollSeq = 1
	ticked, _ := m.handlePreviewTick(previewTickMsg{pollSeq: 1})
	if got := ticked.(Model).previewTargetSeq; got != seq {
		t.Errorf("a preview tick bumped the generation from %d to %d", seq, got)
	}
}

// TestKeyboardHelpDocumentsTheProjectsView covers what the generated half of the
// help cannot reach on its own. Folding is bound in the list handler and appears
// in no palette, so it needs a static section that a test holds in place.
func TestKeyboardHelpDocumentsTheProjectsView(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewProjects)
	m.mode = ModeHelp
	m.height = 0
	out := stripAnsi(renderHelp(m))

	if !strings.Contains(out, "Projects view") {
		t.Fatalf("the help overlay has no Projects section:\n%s", out)
	}
	// Three surfaces that all narrow by project and must stay legible as three:
	// the switcher's own section, the filter component it writes, and the DSL
	// selector the operator can type instead.
	for _, phrase := range []string{"Project switcher", "project filter", "#project"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("the help overlay does not keep %q distinct:\n%s", phrase, out)
		}
	}
	// Enter means something different on a summary than on a session, and the
	// help has to say so rather than promising a focus that will not happen.
	if !strings.Contains(out, "fold") {
		t.Errorf("the help overlay never explains folding:\n%s", out)
	}
	// And it may not still advertise the three keys the fold and the arrangement
	// used to be spelled with. `←`/`→` are gone outright; `v` is a palette
	// command, which the help documents by name rather than by letter.
	for _, retired := range []string{"←", "→"} {
		if strings.Contains(out, retired) {
			t.Errorf("the help overlay still documents the retired fold key %q:\n%s", retired, out)
		}
	}
}

// TestBuiltSummaryOffersOnlyViewActions is the availability claim across all
// three surfaces the operator can reach an action from, made against a summary
// the real builder emitted rather than a spliced one. Enter is excluded because
// on a summary it is no longer a session action at all — TestEnterFoldsOnlyOnASummary
// covers what it does instead.
func TestBuiltSummaryOffersOnlyViewActions(t *testing.T) {
	forbidDaemonMutations(t)
	m, runner := listViewModel(dashboard.ViewProjects)
	m = selectLine(t, m, "summary:monorepo(open)")
	calls := len(runner.Calls)

	for _, key := range summaryRowActionKeys {
		if key == keyEnter {
			continue
		}
		after, cmd := m.Update(keyPress(key))
		if cmd != nil {
			t.Errorf("key %q dispatched %#v with the cursor on a built summary", key, cmd())
		}
		if got := after.(Model); got.mode != ModeList || got.targetSession.TabID != 0 {
			t.Errorf("key %q left mode %d targeting tab %d", key, got.mode, got.targetSession.TabID)
		}
	}
	if n := len(runner.Calls); n != calls {
		t.Fatalf("the row actions made %d Kitty call(s) on a summary: %+v", n-calls, runner.Calls[calls:])
	}

	ctx := captureCommandContext(m)
	if ctx.HasTarget {
		t.Fatalf("the command context claimed target %+v on a summary", ctx.Target)
	}
	for _, cmd := range commandCatalog() {
		available := cmd.AvailabilityFor(m, ctx).Available
		if cmd.Group == commands.GroupSession && available {
			t.Errorf("session command %q is available on a summary", cmd.ID)
		}
		if cmd.ID == commands.ToggleListView && !available {
			t.Error("the view command is unavailable on a summary; it is not a row action")
		}
	}

	for _, e := range listFooterBindings() {
		if e.section == footerSectionRow && e.availFn(m) {
			t.Errorf("footer entry %q (%s) is available on a summary", e.key, e.label)
		}
	}

	// The footer says what the keys actually do here instead of offering an
	// action that will refuse.
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "fold") {
		t.Errorf("the footer never mentions folding with a summary selected:\n%s", footer)
	}
}
