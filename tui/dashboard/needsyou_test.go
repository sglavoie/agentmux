package dashboard

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

const needsYouThreshold = time.Minute

// needsYouRows is the fixture the navigation assertions walk. Two of its five
// sessions need the operator and they are deliberately the two DIFFERENT kinds:
// a blocked prompt, which the old state-name jump already found, and an unread
// completion, which it structurally could not. One family holds a needs-you row
// and a working sibling, so folding it produces the case the whole RowTarget
// vocabulary exists for.
func needsYouRows() []session.RunningSession {
	return []session.RunningSession{
		{WindowID: 11, Base: "alpha", State: "key", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{WindowID: 12, Base: "beta", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{WindowID: 13, Base: "gamma", State: "warning"},
		{WindowID: 14, Base: "delta", State: "stop", Unseen: true},
		{WindowID: 15, Base: "epsilon", State: "stop"},
	}
}

// TestNeedsYouIsTheQueueDefinitionNotACopy pins the navigator to the same
// judgement the heading and the count are drawn from. Asserting against
// StateGroup rather than against a list of state names is the point: a rule that
// moves must move for all three at once.
func TestNeedsYouIsTheQueueDefinitionNotACopy(t *testing.T) {
	for i, s := range needsYouRows() {
		want := StateGroup(s, nil, needsYouThreshold) == attentionGroup
		if got := NeedsYou(s, nil, needsYouThreshold); got != want {
			t.Errorf("row %d (%s/%q): NeedsYou = %v, StateGroup says %v", i, s.Base, s.State, got, want)
		}
	}
}

// TestNeedsYouIncludesUnreadCompletions is the half the retired state-name
// matcher could not express. "An unseen stop" is not a state name, so it could
// not be spelled in the user-editable attention list at all — a finished turn
// nobody had looked at sat in the top group with no key that reached it.
//
// The `Unseen` bit is what carries it, and the pair below is the assertion: the
// same state name answers differently depending on it, which no predicate over
// a state name alone can do.
func TestNeedsYouIncludesUnreadCompletions(t *testing.T) {
	unread := session.RunningSession{State: "stop", Unseen: true}
	if !NeedsYou(unread, nil, needsYouThreshold) {
		t.Error("an unread completion does not need the operator")
	}

	seen := session.RunningSession{State: "stop"}
	if NeedsYou(seen, nil, needsYouThreshold) {
		t.Error("a completion the operator has already seen still needs them")
	}
}

// TestNeedsYouExcludesRecoveredToolWarnings is the other half, and the one that
// removes a stop rather than adding one. `]]` landed on warnings because
// `warning` was a state name in the default attention set — but the harness
// recovered and kept working, so the row needs nothing, and stopping the cursor
// there is exactly the false alarm the queue is meant to end.
func TestNeedsYouExcludesRecoveredToolWarnings(t *testing.T) {
	warned := session.RunningSession{State: "warning"}
	if NeedsYou(warned, nil, needsYouThreshold) {
		t.Error("a recovered tool warning stops needs-you navigation")
	}
	// The row is still drawn, and still outside the queue: the exclusion is a
	// grouping judgement rather than the navigator's own private list.
	if StateGroup(warned, nil, needsYouThreshold) == attentionGroup {
		t.Fatal("a recovered tool warning now groups under Needs you: re-derive the navigator and the heading together")
	}
}

// TestNeedsYouNavigatesPastRowsThatDoNotWantYou walks the flat urgency
// arrangement, where every row has a line of its own and no target ever needs
// revealing. Forward from the working row skips the warning and the seen
// completion and lands on the unread one.
func TestNeedsYouNavigatesPastRowsThatDoNotWantYou(t *testing.T) {
	list := BuildItems(needsYouRows())

	target, ok := FindNextNeedsYou(list, 1, 1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("forward from the working row found nothing")
	}
	if target.Row != 3 {
		t.Errorf("forward landed on row %d (%q), want row 3, the unread completion", target.Row, list.Rows[target.Row].Base)
	}
	if target.Item != 3 || target.Reveal {
		t.Errorf("target = %+v, want item 3 with nothing to reveal", target)
	}

	back, ok := FindNextNeedsYou(list, 1, -1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("backward from the working row found nothing")
	}
	if back.Row != 0 {
		t.Errorf("backward landed on row %d (%q), want row 0, the blocked prompt", back.Row, list.Rows[back.Row].Base)
	}
}

// TestNeedsYouWrapsExactlyOnce pins the wrap: from the last needs-you row,
// forward reaches the first one again, and from the first, backward reaches the
// last. Exactly once is what keeps a list with no match from spinning.
func TestNeedsYouWrapsExactlyOnce(t *testing.T) {
	list := BuildItems(needsYouRows())

	forward, ok := FindNextNeedsYou(list, 3, 1, nil, needsYouThreshold)
	if !ok || forward.Row != 0 {
		t.Errorf("forward from the last needs-you row = %+v (ok=%v), want row 0", forward, ok)
	}
	back, ok := FindNextNeedsYou(list, 0, -1, nil, needsYouThreshold)
	if !ok || back.Row != 3 {
		t.Errorf("backward from the first needs-you row = %+v (ok=%v), want row 3", back, ok)
	}
}

// TestNeedsYouFindsNothingWhenNothingWantsYou is the quiet-dashboard case, and
// the answer has to be a refusal rather than a target: moving the cursor to prove
// there was nothing to move to would lose the operator's place for no reason.
func TestNeedsYouFindsNothingWhenNothingWantsYou(t *testing.T) {
	calm := []session.RunningSession{
		{WindowID: 11, State: "bolt"},
		{WindowID: 12, State: "warning"},
		{WindowID: 13, State: "stop"},
	}
	if target, ok := FindNextNeedsYou(BuildItems(calm), 0, 1, nil, needsYouThreshold); ok {
		t.Errorf("a dashboard that needs nothing answered %+v", target)
	}
	if _, ok := FindNextNeedsYou(BuildItems(nil), 0, 1, nil, needsYouThreshold); ok {
		t.Error("an empty projection answered a target")
	}
	if _, ok := FindNextNeedsYou(BuildItems(needsYouRows()), 0, 0, nil, needsYouThreshold); ok {
		t.Error("a direction that is neither forward nor backward answered a target")
	}
}

// TestNeedsYouFromAnUnplaceableCursorStartsAtAnEnd covers the cursor that names
// no line: nothing selected yet, or a selection left over from a longer list
// mid-resize. Forward starts at the top and backward starts by wrapping to the
// bottom, so an unplaceable cursor costs the operator a landing rather than a
// refusal.
func TestNeedsYouFromAnUnplaceableCursorStartsAtAnEnd(t *testing.T) {
	list := BuildItems(needsYouRows())
	for _, from := range []int{-1, 99} {
		forward, ok := FindNextNeedsYou(list, from, 1, nil, needsYouThreshold)
		if !ok || forward.Row != 0 {
			t.Errorf("forward from %d = %+v (ok=%v), want the first needs-you row", from, forward, ok)
		}
		back, ok := FindNextNeedsYou(list, from, -1, nil, needsYouThreshold)
		if !ok || back.Row != 3 {
			t.Errorf("backward from %d = %+v (ok=%v), want the last needs-you row", from, back, ok)
		}
	}
}

// TestNeedsYouReachesARowFoldedInsideAFamily is the case the whole RowTarget
// vocabulary exists for. In the Projects view a folded family projects to one
// summary and its members project to nothing, so the blocked prompt inside it has
// no line to select — and a navigator walking only visible lines reported the
// dashboard as calm while a session waited.
func TestNeedsYouReachesARowFoldedInsideAFamily(t *testing.T) {
	folded := map[string]bool{"git:/repo": true}
	list := BuildProjectItems(needsYouRows(), folded)

	// The premise: the family is one line and the blocked prompt is not on it.
	if list.Items[0].Kind != ItemFamilySummary || !list.Items[0].Collapsed {
		t.Fatalf("item 0 = %+v, want a collapsed family summary", list.Items[0])
	}

	// From the warning leaf, forward reaches the unread completion, then the
	// blocked prompt hidden inside the family.
	target, ok := FindNextNeedsYou(list, 2, 1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("forward from the unread completion found nothing; the folded prompt is unreachable")
	}
	if target.Row != 0 {
		t.Errorf("landed on row %d, want row 0, the prompt inside the folded family", target.Row)
	}
	if !target.Reveal {
		t.Error("the target does not ask for the family to be revealed; the cursor would sit on a summary that is not the row")
	}
	if target.Item != 0 || target.FamilyKey != "git:/repo" {
		t.Errorf("target = %+v, want the summary at item 0 naming git:/repo", target)
	}
}

// TestRevealedFamilyResolvesToTheRowItself completes the caller protocol the
// Reveal flag defines: expand the named family, rebuild, ask for the SAME row,
// and land on a real leaf. The second answer must not still be asking to reveal
// something, or the keystroke would never terminate.
func TestRevealedFamilyResolvesToTheRowItself(t *testing.T) {
	collapsed := map[string]bool{"git:/repo": true}
	target, ok := FindNextNeedsYou(BuildProjectItems(needsYouRows(), collapsed), 2, 1, nil, needsYouThreshold)
	if !ok || !target.Reveal {
		t.Fatalf("setup: expected a reveal target, got %+v (ok=%v)", target, ok)
	}

	delete(collapsed, target.FamilyKey)
	revealed := BuildProjectItems(needsYouRows(), collapsed)
	final, ok := revealed.ResolveRowTarget(target.Row)
	if !ok {
		t.Fatal("the row is unreachable after its family was expanded")
	}
	if final.Reveal {
		t.Errorf("the expanded family still asks to be revealed: %+v", final)
	}
	it, _ := revealed.At(final.Item)
	if !it.IsSession() || it.Row != target.Row {
		t.Errorf("item %d = %+v, want the leaf for row %d", final.Item, it, target.Row)
	}
}

// TestNeedsYouCanLandOnAnExpandedFamilysFirstChild pins the other summary kind.
// An expanded summary is only a heading — its members are lines of their own
// directly below it — so it stands for no row, and a forward jump from it may
// land on the very next line. Treating it like a folded one would skip the whole
// family.
func TestNeedsYouCanLandOnAnExpandedFamilysFirstChild(t *testing.T) {
	list := BuildProjectItems(needsYouRows(), nil)
	if list.Items[0].Kind != ItemFamilySummary || list.Items[0].Collapsed {
		t.Fatalf("item 0 = %+v, want an expanded family summary", list.Items[0])
	}

	target, ok := FindNextNeedsYou(list, 0, 1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("forward from an expanded summary found nothing")
	}
	if target.Row != 0 {
		t.Errorf("landed on row %d, want row 0, the summary's own first child", target.Row)
	}
	if target.Reveal {
		t.Errorf("target = %+v, want no reveal: the child is already on screen", target)
	}
	if target.Item != 1 {
		t.Errorf("target item = %d, want 1, the line immediately below the heading", target.Item)
	}
}

// TestASoleNeedsYouRowWrapsBackOntoItself states the degenerate wrap, where the
// answer differs between the two arrangements for a good reason. On a visible
// leaf it is a no-op, exactly as repeating a jump with one match always was.
// Inside a folded family it is not: the operator has never actually seen the row,
// so the keystroke unfolds and shows it.
func TestASoleNeedsYouRowWrapsBackOntoItself(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Base: "alpha", State: "key", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{WindowID: 12, Base: "beta", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "repo"},
	}

	flat := BuildItems(rows)
	target, ok := FindNextNeedsYou(flat, 0, 1, nil, needsYouThreshold)
	if !ok || target.Row != 0 || target.Item != 0 || target.Reveal {
		t.Errorf("on a visible leaf: %+v (ok=%v), want a no-op back onto item 0", target, ok)
	}

	folded := BuildProjectItems(rows, map[string]bool{"git:/repo": true})
	revealing, ok := FindNextNeedsYou(folded, 0, 1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("from a folded family holding the only needs-you row, nothing was found")
	}
	if !revealing.Reveal || revealing.Row != 0 {
		t.Errorf("on a folded summary: %+v, want row 0 with a reveal", revealing)
	}
}

// TestFoldedFamilyIsWalkedAtItsOwnPosition pins where a hidden row sits in the
// order. On screen the summary IS its members, so a jump away from a selected
// folded family goes to the next family rather than into the one already under
// the cursor — and a jump toward it arrives from the right side.
func TestFoldedFamilyIsWalkedAtItsOwnPosition(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Base: "alpha", State: "key", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{WindowID: 12, Base: "beta", State: "question", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{WindowID: 13, Base: "gamma", State: "error"},
	}
	list := BuildProjectItems(rows, map[string]bool{"git:/repo": true})

	forward, ok := FindNextNeedsYou(list, 0, 1, nil, needsYouThreshold)
	if !ok || forward.Row != 2 {
		t.Errorf("forward from the folded family = %+v (ok=%v), want row 2: both of its own members are behind it", forward, ok)
	}
	back, ok := FindNextNeedsYou(list, 1, -1, nil, needsYouThreshold)
	if !ok || back.Row != 1 {
		t.Errorf("backward from the leaf = %+v (ok=%v), want row 1, the family's LAST hidden member", back, ok)
	}
}

// TestResolveRowTargetPrefersTheVisibleLeaf states the precedence directly. A row
// with a line of its own is reached on that line; a summary is consulted only for
// a row that has none, so a caller is never asked to unfold a family in order to
// select something already on screen.
func TestResolveRowTargetPrefersTheVisibleLeaf(t *testing.T) {
	rows := needsYouRows()
	list := ItemList{
		Rows: rows,
		Items: []Item{
			{Kind: ItemFamilySummary, Row: -1, Members: []int{0, 1}, FamilyKey: "git:/repo", Collapsed: true},
			{Kind: ItemSession, Row: 0, FamilyKey: "git:/repo"},
		},
	}

	target, ok := list.ResolveRowTarget(0)
	if !ok {
		t.Fatal("a row with a visible leaf resolved to nothing")
	}
	if target.Item != 1 || target.Reveal {
		t.Errorf("target = %+v, want the leaf at item 1 with no reveal", target)
	}

	hidden, ok := list.ResolveRowTarget(1)
	if !ok || hidden.Item != 0 || !hidden.Reveal {
		t.Errorf("the row with no leaf resolved to %+v (ok=%v), want the summary with a reveal", hidden, ok)
	}
}

// TestResolveRowTargetFailsClosed covers the three answers that must be refusals.
// An out-of-range row, a row no line reaches, and a row whose family summary is
// EXPANDED but whose leaf is missing are all "I cannot reach this" — and moving
// the cursor somewhere plausible instead would point every subsequent action at a
// session the operator did not select.
func TestResolveRowTargetFailsClosed(t *testing.T) {
	rows := needsYouRows()
	full := BuildItems(rows)
	for _, row := range []int{-1, len(rows), 99} {
		if target, ok := full.ResolveRowTarget(row); ok {
			t.Errorf("ResolveRowTarget(%d) = %+v, want a refusal", row, target)
		}
	}

	// A row inside the set that no line names. Neither builder produces this; it is
	// asserted because the two halves of an ItemList travel as separate fields.
	partial := ItemList{Rows: rows, Items: []Item{{Kind: ItemSession, Row: 0}}}
	if target, ok := partial.ResolveRowTarget(3); ok {
		t.Errorf("a row no line reaches resolved to %+v", target)
	}

	// An expanded summary claiming members is a heading, not a way in: its members
	// have their own lines, and if they do not, it cannot stand in for them.
	expanded := ItemList{
		Rows:  rows,
		Items: []Item{{Kind: ItemFamilySummary, Row: -1, Members: []int{0, 1}, FamilyKey: "git:/repo"}},
	}
	if target, ok := expanded.ResolveRowTarget(0); ok {
		t.Errorf("an expanded summary stood in for a member with no line: %+v", target)
	}
}

// TestNeedsYouSurvivesAProjectionThatPointsOutOfRange guards the same
// separate-fields hazard from the navigator's side. A member index outside the
// row set is skipped rather than panicking, which is the rule GroupAt,
// SummaryCounts, and HasUnreadNotice already keep about the same field.
func TestNeedsYouSurvivesAProjectionThatPointsOutOfRange(t *testing.T) {
	rows := needsYouRows()
	list := ItemList{
		Rows: rows,
		Items: []Item{
			{Kind: ItemFamilySummary, Row: -1, Members: []int{99, 0, -3}, FamilyKey: "git:/repo", Collapsed: true},
			{Kind: ItemSession, Row: 42},
			{Kind: ItemSession, Row: 3},
		},
	}

	target, ok := FindNextNeedsYou(list, 2, 1, nil, needsYouThreshold)
	if !ok {
		t.Fatal("a projection with bad indices lost the rows that were still good")
	}
	if target.Row != 0 || !target.Reveal {
		t.Errorf("target = %+v, want row 0 behind a reveal", target)
	}
}

// TestNeedsYouReadsTheIdleThreshold pins the one input that is not a property of
// the rows. A session with no icon is never the operator's turn, at any age — the
// threshold decides whether it is Active or Idle, and neither is Needs you — so
// moving the threshold must not create or destroy a stop.
func TestNeedsYouReadsTheIdleThreshold(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Base: "quiet"},
		{WindowID: 12, Base: "blocked", State: "key"},
	}
	list := BuildItems(rows)
	stale := map[int]time.Time{11: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)}

	for _, lastActivity := range []map[int]time.Time{nil, stale} {
		target, ok := FindNextNeedsYou(list, 1, 1, lastActivity, needsYouThreshold)
		if !ok || target.Row != 1 {
			t.Errorf("with lastActivity=%v: %+v (ok=%v), want the blocked row and nothing else", lastActivity, target, ok)
		}
	}
}
