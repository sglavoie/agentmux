package dashboard

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// itemTestRows is the fixture every parity assertion below reads. It spans all
// three urgency groups so the group-counting assertions have something to
// disagree about if the item-indexed walk and the row-indexed one ever drift.
func itemTestRows() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", State: "key", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 2, WindowID: 12, Base: "web", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 3, WindowID: 13, Base: "etl", State: ""},
	}
}

// TestBuildItemsProjectsOneSessionItemPerRow pins the projection session 2
// establishes: index i of the item list names row i of the source, so the
// urgency view's rendering, pagination, and selection are unchanged by the
// existence of the item layer.
func TestBuildItemsProjectsOneSessionItemPerRow(t *testing.T) {
	rows := itemTestRows()
	list := BuildItems(rows)

	if got, want := list.Len(), len(rows); got != want {
		t.Fatalf("Len() = %d, want %d (one item per filtered row)", got, want)
	}
	for i, row := range rows {
		it, ok := list.At(i)
		if !ok {
			t.Fatalf("At(%d) reported no item for a row that exists", i)
		}
		if !it.IsSession() {
			t.Errorf("item %d kind = %v, want ItemSession", i, it.Kind)
		}
		if it.Row != i {
			t.Errorf("item %d Row = %d, want %d (the projection must be index-identical)", i, it.Row, i)
		}
		if it.Members != nil {
			t.Errorf("item %d carries members %v; a session leaf speaks for no one else", i, it.Members)
		}
		if it.FamilyKey != row.FamilyKey || it.FamilyLabel != row.FamilyLabel {
			t.Errorf("item %d family = (%q,%q), want (%q,%q)", i, it.FamilyKey, it.FamilyLabel, row.FamilyKey, row.FamilyLabel)
		}
		got, ok := list.SessionAt(i)
		if !ok {
			t.Fatalf("SessionAt(%d) reported no session for a session item", i)
		}
		if got.TabID != row.TabID {
			t.Errorf("SessionAt(%d).TabID = %d, want %d", i, got.TabID, row.TabID)
		}
	}
}

// TestItemListOutOfRangeFailsClosed asserts a transiently invalid cursor reads
// as "nothing selected" rather than panicking or reading row zero. Every
// row-targeted action resolves its target through these, so an out-of-range
// answer is what stands between a resize race and a mutation on the wrong row.
func TestItemListOutOfRangeFailsClosed(t *testing.T) {
	list := BuildItems(itemTestRows())
	for _, i := range []int{-1, len(list.Items), 99} {
		if _, ok := list.At(i); ok {
			t.Errorf("At(%d) reported an item for an out-of-range index", i)
		}
		if _, ok := list.SessionAt(i); ok {
			t.Errorf("SessionAt(%d) reported a session for an out-of-range index", i)
		}
		if got := list.StateAt(i); got != "" {
			t.Errorf("StateAt(%d) = %q, want \"\"", i, got)
		}
	}
	empty := BuildItems(nil)
	if empty.Len() != 0 {
		t.Errorf("BuildItems(nil).Len() = %d, want 0", empty.Len())
	}
	if _, ok := empty.SessionAt(0); ok {
		t.Error("SessionAt(0) on an empty projection reported a session")
	}
}

// summaryList returns a hand-built projection holding one family summary over
// rows 0 and 1 followed by the ungrouped row 2. BuildItems cannot produce this
// yet — session 2 projects session leaves only — so it is assembled directly,
// which is exactly the point: the vocabulary must already answer for a kind the
// builder does not emit.
func summaryList() ItemList {
	rows := itemTestRows()
	return ItemList{
		Rows: rows,
		Items: []Item{
			{Kind: ItemFamilySummary, Row: -1, Members: []int{0, 1}, FamilyKey: "git:/repo", FamilyLabel: "repo"},
			{Kind: ItemSession, Row: 2},
		},
	}
}

// TestFamilySummaryResolvesToNoSession is the fail-closed guarantee the whole
// item layer exists for: a summary speaks for many sessions at once, so naming
// one of them would let a close, a restart, or a status clear land on a row the
// operator never selected.
func TestFamilySummaryResolvesToNoSession(t *testing.T) {
	list := summaryList()

	it, ok := list.At(0)
	if !ok {
		t.Fatal("At(0) reported no item for the summary")
	}
	if it.IsSession() {
		t.Fatal("the summary reported itself as a session leaf")
	}
	if s, ok := list.SessionAt(0); ok {
		t.Errorf("SessionAt(0) resolved the summary to session %+v; it must resolve to none", s)
	}
	if s, ok := list.Session(it); ok {
		t.Errorf("Session(summary) resolved to %+v; it must resolve to none", s)
	}
	// The sibling session item in the same projection still resolves, so the
	// refusal above is about the kind and not about the projection being broken.
	if _, ok := list.SessionAt(1); !ok {
		t.Error("SessionAt(1) refused a session leaf sitting beside a summary")
	}
}

// TestFamilySummaryStateIsEmptySoJumpWalksPast pins the mechanism that keeps
// jump navigation working without teaching the matchers about item kinds: the
// empty state is in no chord set and in no attention set.
func TestFamilySummaryStateIsEmptySoJumpWalksPast(t *testing.T) {
	list := summaryList()
	if got := list.StateAt(0); got != "" {
		t.Fatalf("StateAt(summary) = %q, want \"\"", got)
	}
	// Both members are matched by their own states, but the summary standing
	// for them answers to neither matcher.
	attn := AttentionMatcher(nil)
	if attn("") {
		t.Error("the attention matcher accepts the empty state, so a summary would be a jump target")
	}
	if next := FindNextMatching(list, -1, +1, attn); next != -1 {
		t.Errorf("FindNextMatching landed on item %d; the only attention row is folded into the summary, so there is nothing to land on", next)
	}
}

// TestFamilySummaryTakesMostUrgentMemberGroup asserts a family can never hide a
// session that needs attention behind a calmer aggregate.
func TestFamilySummaryTakesMostUrgentMemberGroup(t *testing.T) {
	list := summaryList()
	idle := 10 * time.Minute

	if got := list.GroupAt(0, nil, idle); got != 0 {
		t.Errorf("GroupAt(summary) = %d, want 0: member %q is in Needs Attention", got, list.Rows[0].State)
	}

	// Drop the attention member and the summary relaxes to its next most
	// urgent member rather than staying pinned.
	calmer := list
	calmer.Items = []Item{{Kind: ItemFamilySummary, Row: -1, Members: []int{1}, FamilyKey: "git:/repo"}}
	if got := calmer.GroupAt(0, nil, idle); got != 1 {
		t.Errorf("GroupAt(summary over the active member) = %d, want 1", got)
	}

	// A summary with no members, and one whose members point outside the row
	// set, both answer Idle — the bucket that claims the least.
	for name, members := range map[string][]int{"no members": nil, "out of range": {7, -1}} {
		degenerate := list
		degenerate.Items = []Item{{Kind: ItemFamilySummary, Row: -1, Members: members}}
		if got := degenerate.GroupAt(0, nil, idle); got != idleGroup {
			t.Errorf("GroupAt(summary with %s) = %d, want %d", name, got, idleGroup)
		}
	}
}

// TestItemGroupCountsMatchRowGroupCounts is the parity assertion that keeps the
// urgency view behaviorally unchanged: while every row projects to one item,
// the item-indexed group walk and the row-indexed one must agree exactly, or
// the renderer and the scroll math would charge different group chrome.
func TestItemGroupCountsMatchRowGroupCounts(t *testing.T) {
	idle := 10 * time.Minute
	cases := map[string][]session.RunningSession{
		"three groups": itemTestRows(),
		"one group":    {{TabID: 1, Base: "a", State: ""}, {TabID: 2, Base: "b", State: ""}},
		"empty":        nil,
	}
	for name, rows := range cases {
		list := BuildItems(rows)
		if got, want := list.CountGroups(nil, idle), CountGroups(rows, nil, idle); got != want {
			t.Errorf("%s: ItemList.CountGroups = %d, row CountGroups = %d", name, got, want)
		}
		for i, row := range rows {
			if got, want := list.GroupAt(i, nil, idle), StateGroup(row, nil, idle); got != want {
				t.Errorf("%s: GroupAt(%d) = %d, StateGroup(row) = %d", name, i, got, want)
			}
		}
		for group := 0; group <= idleGroup; group++ {
			n := 0
			for _, row := range rows {
				if StateGroup(row, nil, idle) == group {
					n++
				}
			}
			if got := list.countInGroup(group, nil, idle); got != n {
				t.Errorf("%s: countInGroup(%d) = %d, want %d", name, group, got, n)
			}
		}
	}
}

// TestCountGroupsCountsSelectableLinesNotSessions pins the unit the group label
// speaks in. Two sessions folded into one summary contribute one line, not two.
func TestCountGroupsCountsSelectableLinesNotSessions(t *testing.T) {
	list := summaryList()
	idle := 10 * time.Minute

	// Summary in Needs Attention (via its member) plus the one remaining leaf,
	// which carries no state and no activity record and so counts as Active.
	if got, want := list.CountGroups(nil, idle), 2; got != want {
		t.Errorf("CountGroups = %d, want %d", got, want)
	}
	if got, want := list.countInGroup(0, nil, idle), 1; got != want {
		t.Errorf("countInGroup(attention) = %d, want %d: the two members contribute one line", got, want)
	}
	if got, want := list.countInGroup(1, nil, idle), 1; got != want {
		t.Errorf("countInGroup(active) = %d, want %d", got, want)
	}
}

// TestCaptureItemIdentityNamesKindAndFamily asserts a captured selection says
// what was selected at all, not only which session. Without the kind a captured
// summary would restore through the session chain, find nothing, and fall
// through to the attention fallback — losing a selection still on screen.
func TestCaptureItemIdentityNamesKindAndFamily(t *testing.T) {
	list := summaryList()

	summary := CaptureItemIdentity(list, 0)
	if summary.Kind != ItemFamilySummary {
		t.Errorf("captured summary Kind = %v, want ItemFamilySummary", summary.Kind)
	}
	if summary.FamilyKey != "git:/repo" {
		t.Errorf("captured summary FamilyKey = %q, want %q", summary.FamilyKey, "git:/repo")
	}
	if summary.TabID != 0 || summary.WindowID != 0 || summary.Base != "" {
		t.Errorf("captured summary carries session identity %+v; it has no session of its own", summary)
	}

	leaf := CaptureItemIdentity(BuildItems(itemTestRows()), 0)
	if leaf.Kind != ItemSession {
		t.Errorf("captured leaf Kind = %v, want ItemSession", leaf.Kind)
	}
	if leaf.TabID != 1 || leaf.FamilyKey != "git:/repo" {
		t.Errorf("captured leaf = %+v, want TabID 1 and the row's family key", leaf)
	}

	if got := CaptureItemIdentity(list, 99); (got != CursorIdentity{}) {
		t.Errorf("CaptureItemIdentity(out of range) = %+v, want the zero identity", got)
	}
}

// TestRestoreItemCursorCrossesBetweenKinds pins the two one-directional
// crossings: a session folded into a summary restores onto that summary, and a
// summary that has expanded restores onto one of its members. In both cases the
// row the operator was looking at is still on screen under a different kind.
func TestRestoreItemCursorCrossesBetweenKinds(t *testing.T) {
	idle := 10 * time.Minute

	// The operator had row "web" selected; the rebuild folded it into a
	// summary, so the summary is where the selection belongs.
	prior := CaptureItemIdentity(BuildItems(itemTestRows()), 1)
	if got := RestoreItemCursor(summaryList(), prior, nil, idle); got != 0 {
		t.Errorf("folded session restored to item %d, want the summary at 0", got)
	}

	// The operator had the summary selected; the rebuild expanded it, so the
	// first member is where the selection belongs.
	priorSummary := CaptureItemIdentity(summaryList(), 0)
	if got := RestoreItemCursor(BuildItems(itemTestRows()), priorSummary, nil, idle); got != 0 {
		t.Errorf("expanded summary restored to item %d, want its first member at 0", got)
	}

	// A summary whose family is gone entirely falls through to the ordinary
	// attention fallback rather than to a member of some other family.
	gone := CursorIdentity{Kind: ItemFamilySummary, FamilyKey: "git:/elsewhere"}
	rows := []session.RunningSession{{TabID: 9, Base: "z", State: ""}, {TabID: 8, Base: "y", State: "key"}}
	if got := RestoreItemCursor(BuildItems(rows), gone, nil, idle); got != 1 {
		t.Errorf("vanished family restored to item %d, want the first attention item at 1", got)
	}
}

// TestRestoreItemCursorKeepsExistingSessionChain asserts the identity chain
// session 1 shipped still resolves in the same order once kind and family sit
// above it: exact tab, then window, then key, then the detailed base match.
func TestRestoreItemCursorKeepsExistingSessionChain(t *testing.T) {
	idle := 10 * time.Minute
	rows := []session.RunningSession{
		{TabID: 1, WindowID: 11, Key: "k1", Base: "api", Harness: "claude", Directory: "/a"},
		{TabID: 2, WindowID: 12, Key: "k2", Base: "web", Harness: "claude", Directory: "/b"},
	}
	list := BuildItems(rows)

	cases := []struct {
		name  string
		prior CursorIdentity
		want  int
	}{
		{"tab id wins", CursorIdentity{TabID: 2, WindowID: 11, Key: "k1", Base: "api"}, 1},
		{"window id when the tab is gone", CursorIdentity{WindowID: 12}, 1},
		{"session key when both ids are gone", CursorIdentity{Key: "k2"}, 1},
		{"detailed base match", CursorIdentity{Base: "web", Harness: "claude", Directory: "/b"}, 1},
		{"base-only fallback", CursorIdentity{Base: "web", Directory: "/mismatched"}, 1},
		{"nothing matches", CursorIdentity{Base: "absent"}, 0},
	}
	for _, tc := range cases {
		if got := RestoreItemCursor(list, tc.prior, nil, idle); got != tc.want {
			t.Errorf("%s: restored to %d, want %d", tc.name, got, tc.want)
		}
	}

	if got := RestoreItemCursor(BuildItems(nil), CursorIdentity{TabID: 1}, nil, idle); got != 0 {
		t.Errorf("restore against an empty projection = %d, want 0", got)
	}
}
