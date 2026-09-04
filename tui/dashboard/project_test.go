package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

const projectIdle = 10 * time.Minute

// projectRows is the fixture most of the builder assertions read. It is written
// in urgency order, as the source always is by the time it reaches a builder, and
// it deliberately interleaves the two families so an implementation that emitted
// families in map order or in "all of family A, then all of family B" order would
// have to disagree with it.
//
// Row 0 and row 3 share a Git family; row 1 and row 4 share a configured non-Git
// family; row 2 is a one-session family; row 5 carries no family at all.
func projectRows() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", State: "key", Directory: "/repo/api", FamilyKey: "git:/repo/.git", FamilyLabel: "monorepo"},
		{TabID: 2, WindowID: 12, Base: "notes", State: "question", Directory: "/notes", FamilyKey: "project:/notes\x1fwork\x1fNotes", FamilyLabel: "Notes"},
		{TabID: 3, WindowID: 13, Base: "solo", State: "bolt", Directory: "/solo", FamilyKey: "git:/solo/.git", FamilyLabel: "solo"},
		{TabID: 4, WindowID: 14, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo/.git", FamilyLabel: "monorepo"},
		{TabID: 5, WindowID: 15, Base: "notes-2", State: "", Directory: "/notes", FamilyKey: "project:/notes\x1fwork\x1fNotes", FamilyLabel: "Notes"},
		{TabID: 6, WindowID: 16, Base: "orphan", State: "", Directory: "/tmp/orphan"},
	}
}

// itemShape is the readable form the ordering and nesting assertions compare in.
// Comparing shapes rather than field-by-field is what lets one expectation state
// the whole arrangement at once, so a change that reorders lines fails with the
// arrangement it produced rather than with a single mismatched index.
type itemShape struct {
	kind  ItemKind
	label string
	tab   int // the row's TabID for a leaf, 0 for a summary
	depth int
}

func shapes(list ItemList) []itemShape {
	out := make([]itemShape, 0, len(list.Items))
	for i, it := range list.Items {
		shape := itemShape{kind: it.Kind, label: it.FamilyLabel, depth: it.Depth}
		if s, ok := list.SessionAt(i); ok {
			shape.tab = s.TabID
		}
		out = append(out, shape)
	}
	return out
}

// TestBuildProjectItemsArrangesFamiliesInSourceOrder is the ordering and nesting
// claim in one assertion: a family sits where its earliest member sat in the
// urgency-sorted source, its members keep the order they had there, and only a
// family with more than one member earns a summary. Repository families and
// configured non-Git families are built by the same rule — the key space they
// come from is session 1's business, not this builder's.
func TestBuildProjectItemsArrangesFamiliesInSourceOrder(t *testing.T) {
	list := BuildProjectItems(projectRows(), nil)

	want := []itemShape{
		// The Git family leads because its earliest member is row 0.
		{kind: ItemFamilySummary, label: "monorepo"},
		{kind: ItemSession, label: "monorepo", tab: 1, depth: 1},
		{kind: ItemSession, label: "monorepo", tab: 4, depth: 1},
		// The non-Git family follows: earliest member is row 1.
		{kind: ItemFamilySummary, label: "Notes"},
		{kind: ItemSession, label: "Notes", tab: 2, depth: 1},
		{kind: ItemSession, label: "Notes", tab: 5, depth: 1},
		// A one-session family and an unfamilied row are both top-level leaves.
		{kind: ItemSession, label: "solo", tab: 3},
		{kind: ItemSession, tab: 6},
	}
	if got := shapes(list); !reflect.DeepEqual(got, want) {
		t.Errorf("Projects arrangement =\n%+v\nwant\n%+v", got, want)
	}

	// The rows half is the untouched source: a summary adds a line, not a
	// session, so the row set must be the one that was passed in.
	if !reflect.DeepEqual(list.Rows, projectRows()) {
		t.Errorf("BuildProjectItems rewrote its rows:\n%+v", list.Rows)
	}
	for i, it := range list.Items {
		if it.Kind == ItemFamilySummary && it.Row != -1 {
			t.Errorf("summary at %d carries Row %d; it stands for no single row", i, it.Row)
		}
	}
}

// TestBuildProjectItemsLeavesUnresolvedSessionsUngrouped pins the safety rule
// the whole family identity rests on: a session that could not be resolved to
// one family is not grouped by anything else. Two orphans that share a title, a
// directory, and a project must still be two top-level leaves, because grouping
// them would claim a relationship session 1's resolver explicitly declined to
// assert.
func TestBuildProjectItemsLeavesUnresolvedSessionsUngrouped(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", Project: "API", Directory: "/repo", State: "key"},
		{TabID: 2, Base: "api", Project: "API", Directory: "/repo", State: "bolt"},
	}
	list := BuildProjectItems(rows, nil)

	if got, want := list.Len(), 2; got != want {
		t.Fatalf("Len() = %d, want %d: ambiguous rows must stay two leaves", got, want)
	}
	for i := range list.Items {
		it, _ := list.At(i)
		if it.Kind != ItemSession {
			t.Errorf("item %d kind = %v, want ItemSession", i, it.Kind)
		}
		if it.Depth != 0 {
			t.Errorf("item %d depth = %d, want 0: an ungrouped row is top-level", i, it.Depth)
		}
	}
}

// TestBuildProjectItemsCollapse asserts the three things a collapse map means:
// an absent entry is expanded, a true entry folds the children away, and a
// folded summary still carries every member — a family that forgot its members
// while folded would also forget that one of them needs attention, which is
// exactly the state the operator folded it in.
func TestBuildProjectItemsCollapse(t *testing.T) {
	rows := projectRows()

	expanded := BuildProjectItems(rows, map[string]bool{"git:/elsewhere": true})
	if got, want := expanded.Len(), BuildProjectItems(rows, nil).Len(); got != want {
		t.Errorf("a collapse map naming no visible family changed the line count: %d, want %d", got, want)
	}

	folded := BuildProjectItems(rows, map[string]bool{"git:/repo/.git": true})
	want := []itemShape{
		{kind: ItemFamilySummary, label: "monorepo"},
		{kind: ItemFamilySummary, label: "Notes"},
		{kind: ItemSession, label: "Notes", tab: 2, depth: 1},
		{kind: ItemSession, label: "Notes", tab: 5, depth: 1},
		{kind: ItemSession, label: "solo", tab: 3},
		{kind: ItemSession, tab: 6},
	}
	if got := shapes(folded); !reflect.DeepEqual(got, want) {
		t.Errorf("collapsed arrangement =\n%+v\nwant\n%+v", got, want)
	}

	summary, _ := folded.At(0)
	if !summary.Collapsed {
		t.Error("the folded summary does not report itself collapsed, so nothing can draw the affordance")
	}
	if got, want := summary.Members, []int{0, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("folded summary members = %v, want %v", got, want)
	}
	if got := folded.GroupAt(0, nil, projectIdle); got != 0 {
		t.Errorf("folded summary group = %d, want 0: folding must not hide a member that needs attention", got)
	}
	if open, _ := BuildProjectItems(rows, nil).At(0); open.Collapsed {
		t.Error("an absent collapse-map entry produced a collapsed summary; absent must mean expanded")
	}
}

// TestBuildProjectItemsIsDeterministicAndSideEffectFree covers the property the
// whole projection rests on: it is a pure function of its inputs. Two builds of
// the same rows must be identical (a family walk driven by map iteration order
// would not be), and neither build may touch the rows it read.
func TestBuildProjectItemsIsDeterministicAndSideEffectFree(t *testing.T) {
	rows := projectRows()
	before := projectRows()

	first := BuildProjectItems(rows, nil)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(BuildProjectItems(rows, nil).Items, first.Items) {
			t.Fatalf("build %d produced a different arrangement; the family walk is not deterministic", i)
		}
	}
	if !reflect.DeepEqual(rows, before) {
		t.Errorf("building the projection mutated its input rows:\n%+v", rows)
	}

	// The members slice is the summary's own, not an alias into anything a later
	// build could rewrite.
	summary, _ := first.At(0)
	summary.Members[0] = 99
	if again, _ := BuildProjectItems(rows, nil).At(0); again.Members[0] != 0 {
		t.Error("editing one summary's members reached a later build; the slice is shared")
	}
}

// TestBuildProjectItemsTakesTheFirstNonEmptyLabel asserts a member whose label
// was lost to a degraded probe cannot rename a family its siblings already
// named, and that a family nobody named still groups.
func TestBuildProjectItemsTakesTheFirstNonEmptyLabel(t *testing.T) {
	cases := map[string]struct {
		labels []string
		want   string
	}{
		"first member names it":      {[]string{"repo", ""}, "repo"},
		"a later member names it":    {[]string{"", "repo"}, "repo"},
		"no member names it":         {[]string{"", ""}, ""},
		"the first name is the name": {[]string{"repo", "other"}, "repo"},
	}
	for name, tc := range cases {
		rows := make([]session.RunningSession, len(tc.labels))
		for i, label := range tc.labels {
			rows[i] = session.RunningSession{TabID: i + 1, FamilyKey: "git:/repo", FamilyLabel: label}
		}
		summary, ok := BuildProjectItems(rows, nil).At(0)
		if !ok || summary.Kind != ItemFamilySummary {
			t.Fatalf("%s: expected a summary at 0, got %+v", name, summary)
		}
		if summary.FamilyLabel != tc.want {
			t.Errorf("%s: family label = %q, want %q", name, summary.FamilyLabel, tc.want)
		}
	}
}

// TestSummaryCountsDescribeVisibleMembers is the counting contract. The tally
// speaks in the same buckets StateGroup does — including the unseen-stop
// demotion, which is the one bucket decision a state name does not settle on its
// own — and it counts only what the current filter left behind.
func TestSummaryCountsDescribeVisibleMembers(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "a", State: "key", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 2, Base: "b", State: "stop", Unseen: true, FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 3, Base: "c", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 4, Base: "d", State: "stop", FamilyKey: "git:/repo", FamilyLabel: "repo"},
	}
	list := BuildProjectItems(rows, nil)
	summary, _ := list.At(0)

	// Two attention (the key row and the UNSEEN stop), one active, one idle
	// (the seen stop). A tally that named states rather than groups would put
	// both stops in the same bucket.
	if got, want := list.SummaryCounts(summary, nil, projectIdle), [3]int{2, 1, 1}; got != want {
		t.Errorf("SummaryCounts = %v, want %v", got, want)
	}

	// The projection is built from already-filtered rows, so "visible" is simply
	// "present". A filter that leaves two members reports two.
	twoLeft := BuildProjectItems(rows[:2], nil)
	twoLeftSummary, _ := twoLeft.At(0)
	if got, want := twoLeft.SummaryCounts(twoLeftSummary, nil, projectIdle), [3]int{2, 0, 0}; got != want {
		t.Errorf("filtered SummaryCounts = %v, want %v", got, want)
	}

	// A filter that leaves one member leaves a leaf, not a summary claiming a
	// count of one; a filter that leaves none removes the family entirely.
	oneLeft := BuildProjectItems(rows[:1], nil)
	if got, _ := oneLeft.At(0); got.Kind != ItemSession {
		t.Errorf("a family filtered down to one member produced %v, want a leaf", got.Kind)
	}
	if got := BuildProjectItems(nil, nil).Len(); got != 0 {
		t.Errorf("a family filtered away entirely left %d line(s), want 0", got)
	}

	// A non-summary item and an out-of-range member contribute nothing.
	if got := list.SummaryCounts(Item{Kind: ItemSession, Row: 0}, nil, projectIdle); got != [3]int{} {
		t.Errorf("SummaryCounts(leaf) = %v, want the zero tally", got)
	}
	stray := Item{Kind: ItemFamilySummary, Row: -1, Members: []int{0, 9, -1}}
	if got, want := list.SummaryCounts(stray, nil, projectIdle), [3]int{1, 0, 0}; got != want {
		t.Errorf("SummaryCounts with out-of-range members = %v, want %v", got, want)
	}
}

// TestSummaryLeadIsTheMostUrgentMember asserts the summary speaks with its
// highest-priority child's voice using the same severity and durable-recency
// relation as SortRows.
func TestSummaryLeadIsTheMostUrgentMember(t *testing.T) {
	old := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	newest := old.Add(time.Hour)
	rows := []session.RunningSession{
		{TabID: 1, Base: "a", State: "key", PresentationChangedAt: newest, FamilyKey: "git:/repo"},
		{TabID: 2, Base: "b", State: "error", PresentationChangedAt: old, FamilyKey: "git:/repo"},
		{TabID: 3, Base: "c", State: "error", PresentationChangedAt: newest, FamilyKey: "git:/repo"},
	}
	list := BuildProjectItems(rows, nil)
	summary, _ := list.At(0)

	lead, ok := list.SummaryLead(summary, nil, projectIdle)
	if !ok {
		t.Fatal("SummaryLead found no member of a three-member family")
	}
	// Error outranks the newer key prompt; among the two errors, the newer
	// presentation transition wins even though it appears later in source order.
	if lead.TabID != 3 {
		t.Errorf("SummaryLead = tab %d, want tab 3 (newest member at the highest severity)", lead.TabID)
	}
	if got, want := StateGroup(lead, nil, projectIdle), list.GroupAt(0, nil, projectIdle); got != want {
		t.Errorf("lead group = %d but GroupAt(summary) = %d; the icon and the band would disagree", got, want)
	}

	tiedRows := []session.RunningSession{
		{TabID: 4, State: "question", PresentationChangedAt: newest, FamilyKey: "git:/tied"},
		{TabID: 5, State: "key", PresentationChangedAt: newest, FamilyKey: "git:/tied"},
	}
	tied := BuildProjectItems(tiedRows, nil)
	tiedSummary, _ := tied.At(0)
	if tiedLead, found := tied.SummaryLead(tiedSummary, nil, projectIdle); !found || tiedLead.TabID != 4 {
		t.Errorf("SummaryLead exact tie = tab %d (found=%v), want first source tab 4", tiedLead.TabID, found)
	}

	if _, ok := list.SummaryLead(Item{Kind: ItemSession, Row: 0}, nil, projectIdle); ok {
		t.Error("SummaryLead answered for a session leaf")
	}
	if _, ok := list.SummaryLead(Item{Kind: ItemFamilySummary, Row: -1, Members: []int{9}}, nil, projectIdle); ok {
		t.Error("SummaryLead answered from a member outside the row set")
	}
}

// TestBuildItemsForMapsViewsToBuilders pins the one place a view names a
// builder, including the zero value: a caller that never mentions a view gets
// the urgency arrangement back.
func TestBuildItemsForMapsViewsToBuilders(t *testing.T) {
	rows := projectRows()

	urgency := BuildItemsFor(ViewUrgency, rows, map[string]bool{"git:/repo/.git": true})
	if !reflect.DeepEqual(urgency.Items, BuildItems(rows).Items) {
		t.Error("ViewUrgency did not produce the urgency arrangement (or read the collapse map, which it has nothing to collapse)")
	}
	var zero ViewMode
	if zero != ViewUrgency {
		t.Errorf("the zero ViewMode is %v, want ViewUrgency", zero)
	}

	projects := BuildItemsFor(ViewProjects, rows, nil)
	if !reflect.DeepEqual(projects.Items, BuildProjectItems(rows, nil).Items) {
		t.Error("ViewProjects did not produce the project arrangement")
	}
	if got, want := ViewUrgency.String(), "Urgency"; got != want {
		t.Errorf("ViewUrgency.String() = %q, want %q", got, want)
	}
	if got, want := ViewProjects.String(), "Projects"; got != want {
		t.Errorf("ViewProjects.String() = %q, want %q", got, want)
	}
}
