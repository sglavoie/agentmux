// This file is what one selectable line in the dashboard list stands for. The
// cursor and the scroll offset index items, not sessions, so every question the
// list asks about "the current row" — which group it sits in, which state it
// carries, which running session (if any) an action may target — is answered
// here rather than by indexing the filtered session slice directly.

package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// ItemKind names what a selectable dashboard line stands for. Every kind added
// here is a kind the cursor can land on, and therefore a kind every
// session-targeted lookup has to answer for.
type ItemKind int

const (
	// ItemSession is a live session leaf: exactly one running session. It is the
	// only kind that resolves to a session, and therefore the only kind a Kitty
	// or daemon mutation may target.
	ItemSession ItemKind = iota
	// ItemFamilySummary stands for a whole multi-session family rather than for
	// any one of its members. It is synthetic: it has no session of its own, so
	// every session lookup through it fails closed instead of guessing at a
	// representative member.
	ItemFamilySummary
)

// Item is one selectable line in the dashboard list.
//
// An item never carries a copy of the session it names. It carries the index of
// that session in the row set it was built from, which is what lets the row set
// stay the single source of truth: there is exactly one place a session's facts
// live, and an item is a pointer into it rather than a second copy that could
// disagree.
type Item struct {
	Kind ItemKind
	// Row is the index into ItemList.Rows this item stands for. It is meaningful
	// only for ItemSession; every other kind carries -1, so a caller that forgets
	// to check Kind indexes out of range rather than silently reading row zero.
	Row int
	// Members are the indices into ItemList.Rows a summary speaks for, in list
	// order. Nil for a session leaf.
	Members []int
	// FamilyKey is the stable grouping identity a row was discovered with. It is
	// built out of filesystem paths and must never be rendered; it exists here so
	// a cursor identity can name a family across a rebuild.
	FamilyKey string
	// FamilyLabel is the family's readable name — the one of the two that is
	// meant to reach a surface. Empty for an ungrouped row.
	FamilyLabel string
}

// IsSession reports whether this item resolves to exactly one running session.
func (it Item) IsSession() bool { return it.Kind == ItemSession }

// ItemList is the flattened projection the dashboard cursor and scroll offset
// index. Rows stays the filtered, urgency-sorted source of truth; Items names
// what each rendered line stands for and, for a session leaf, which row.
//
// The two halves are always built together by BuildItems and are read together
// everywhere, which is why they travel as one value rather than as two
// parameters that a caller could pair wrongly.
type ItemList struct {
	Rows  []session.RunningSession
	Items []Item
}

// BuildItems projects the filtered session list onto the item list. It emits
// exactly one session item per row, in row order, so index i of the projection
// names row i of the source: the urgency view's rendering, pagination, and
// selection are unchanged by the existence of the item layer.
//
// Family summaries are not projected here. Grouping deterministic multi-session
// families into summaries is a later change; what this establishes is that the
// cursor already indexes a kinded item, so adding a non-session kind cannot
// reach a session-targeted action.
func BuildItems(rows []session.RunningSession) ItemList {
	items := make([]Item, len(rows))
	for i, s := range rows {
		items[i] = Item{
			Kind:        ItemSession,
			Row:         i,
			FamilyKey:   s.FamilyKey,
			FamilyLabel: s.FamilyLabel,
		}
	}
	return ItemList{Rows: rows, Items: items}
}

// Len reports how many selectable lines the projection holds.
func (l ItemList) Len() int { return len(l.Items) }

// At returns the item at index i, reporting false for an out-of-range index so
// a transiently invalid cursor reads as "nothing selected" rather than panicking.
func (l ItemList) At(i int) (Item, bool) {
	if i < 0 || i >= len(l.Items) {
		return Item{}, false
	}
	return l.Items[i], true
}

// SessionAt returns the running session the item at i stands for. It reports
// false for an out-of-range index and for every non-session kind: a summary
// speaks for many sessions at once, and naming one of them would let a close, a
// restart, or a status clear land on a row the operator never selected.
func (l ItemList) SessionAt(i int) (session.RunningSession, bool) {
	it, ok := l.At(i)
	if !ok {
		return session.RunningSession{}, false
	}
	return l.Session(it)
}

// Session resolves an item to its running session under the same fail-closed
// rule as SessionAt.
func (l ItemList) Session(it Item) (session.RunningSession, bool) {
	if it.Kind != ItemSession || it.Row < 0 || it.Row >= len(l.Rows) {
		return session.RunningSession{}, false
	}
	return l.Rows[it.Row], true
}

// StateAt returns the harness state name carried by the item at i, or "" when
// the item is not a session leaf. The empty string is in no chord set and in no
// attention set, so a synthetic row is unreachable by jump navigation without
// the matchers having to learn about item kinds.
func (l ItemList) StateAt(i int) string {
	s, ok := l.SessionAt(i)
	if !ok {
		return ""
	}
	return s.State
}

// GroupAt classifies the item at i into the same three urgency buckets
// StateGroup uses. A summary takes the most urgent bucket any of its members
// sits in, so a family can never hide a session that needs attention behind a
// calmer aggregate. A summary with no members, and an out-of-range index, both
// answer Idle — the bucket that claims the least.
func (l ItemList) GroupAt(i int, lastActivity map[int]time.Time, threshold time.Duration) int {
	it, ok := l.At(i)
	if !ok {
		return idleGroup
	}
	if s, isSession := l.Session(it); isSession {
		return StateGroup(s, lastActivity, threshold)
	}
	group := idleGroup
	for _, row := range it.Members {
		if row < 0 || row >= len(l.Rows) {
			continue
		}
		if g := StateGroup(l.Rows[row], lastActivity, threshold); g < group {
			group = g
		}
	}
	return group
}

// CountGroups returns how many distinct urgency groups the projected items
// span. The list renders group labels and borders only when this is greater
// than one, so pagination and scroll math charge the same chrome the renderer
// draws. It is the item-indexed counterpart of CountGroups over rows, and the
// two agree exactly while every row projects to one item.
func (l ItemList) CountGroups(lastActivity map[int]time.Time, threshold time.Duration) int {
	seen := make(map[int]bool)
	for i := range l.Items {
		seen[l.GroupAt(i, lastActivity, threshold)] = true
	}
	return len(seen)
}

// countInGroup reports how many projected items sit in the given group. This is
// the number the group label renders, so it counts selectable lines rather than
// sessions: a collapsed family contributes its summary, not its members.
func (l ItemList) countInGroup(group int, lastActivity map[int]time.Time, threshold time.Duration) int {
	n := 0
	for i := range l.Items {
		if l.GroupAt(i, lastActivity, threshold) == group {
			n++
		}
	}
	return n
}

// idleGroup is the bucket that claims the least, used wherever a projection
// question has no answer to give.
const idleGroup = 2
