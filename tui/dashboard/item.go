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
	// Depth is how far the line is indented, in nesting levels. Zero for every
	// summary and every top-level leaf; one for a session rendered underneath the
	// summary that speaks for it. It is presentation only: an indented line is
	// still one line, so nothing in the fit walk or the scroll math reads it.
	Depth int
	// Collapsed reports that a summary's members are folded away and therefore
	// project to no lines of their own. The summary still carries those members,
	// because a folded family that forgot them would also forget that one of them
	// needs attention.
	Collapsed bool
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
// Family summaries are not projected here: this is the urgency arrangement, in
// which a line is a session and grouping is the urgency band the renderer draws
// around it. BuildProjectItems is the arrangement that folds families.
func BuildItems(rows []session.RunningSession) ItemList {
	items := make([]Item, len(rows))
	for i := range rows {
		items[i] = sessionItem(rows, i)
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

// BandAt is the heading the item at i is listed under in the urgency view:
// Pinned for a pinned session leaf, else GroupAt's bucket. A summary is never
// pinned: it speaks for a family, and the Projects view that draws summaries
// draws no headings.
func (l ItemList) BandAt(i int, lastActivity map[int]time.Time, threshold time.Duration) int {
	if s, ok := l.SessionAt(i); ok && s.Starred {
		return pinnedBand
	}
	return l.GroupAt(i, lastActivity, threshold)
}

// HasUnreadNotice reports whether the item carries a completion the operator
// has not looked at yet: a session leaf resting on an unseen stop, or a
// COLLAPSED summary at least one of whose members is one.
//
// An expanded summary answers false. Its members are lines of their own in this
// same projection and each carries its own marker, so aggregating here as well
// would print one family's news twice on adjacent lines. A collapsed summary is
// the opposite case: its members project to no lines at all, so the summary is
// the only place their news can be said — the same reason GroupAt lets a folded
// family speak with its most urgent member's bucket.
//
// A member index outside the row set contributes nothing, exactly as it does to
// GroupAt and SummaryCounts, and every member index is checked rather than
// trusted: a summary is built beside the row set it points into, but the two
// travel as separate fields and a caller may pair them wrongly.
func (l ItemList) HasUnreadNotice(it Item) bool {
	if s, isSession := l.Session(it); isSession {
		return unreadStop(s)
	}
	if it.Kind != ItemFamilySummary || !it.Collapsed {
		return false
	}
	for _, row := range it.Members {
		if row < 0 || row >= len(l.Rows) {
			continue
		}
		if unreadStop(l.Rows[row]) {
			return true
		}
	}
	return false
}

// unreadStop is the leaf rule the whole marker rests on, and it requires both
// halves. Unseen is daemon-owned and inverted so that its zero value means seen,
// and the daemon only ever raises it alongside a resting icon — but it is a bare
// bool on a struct any caller may build, and a fallback row with no daemon
// behind it carries whatever it was given. Requiring the stop state as well is
// what keeps a stale or malformed bit from announcing a completion on a session
// that is still working. It is the same claim StateGroup makes about the same
// two fields, said here for the marker rather than for the bucket.
func unreadStop(s session.RunningSession) bool {
	return s.State == "stop" && s.Unseen
}

// RowTarget names where a cursor has to go to reach one underlying row, and
// what has to happen first.
//
// It exists because "the row I want" and "a line I can put the cursor on" are
// not the same thing in the Projects arrangement: a folded family projects to one
// summary line and its members project to none, so a row inside it has no line
// of its own to select. Answering with the summary plus Reveal is what lets the
// caller fold the family open and then land on the row, rather than either
// silently selecting the summary (the wrong session for every subsequent action)
// or reporting that the row is unreachable (it is not — it is one keystroke away).
type RowTarget struct {
	// Item is the index of the line to select: the row's own leaf when it has one,
	// and otherwise the summary standing for the family that hides it.
	Item int
	// Row is the index into ItemList.Rows this target was resolved for. It travels
	// with the target so a caller that reveals a family can ask for the same row
	// again without remembering which one it asked about.
	Row int
	// FamilyKey is the family that must be expanded, and "" when nothing must be.
	FamilyKey string
	// Reveal reports that Item is a summary rather than the row itself. A caller
	// that honors it expands FamilyKey and resolves the same Row again against the
	// rebuilt projection; a caller that ignores it leaves the cursor on the summary,
	// which is a legal selection that simply is not the row it asked for.
	Reveal bool
}

// ResolveRowTarget resolves one row index to the line that reaches it, reporting
// false when the projection holds no such line at all.
//
// The visible leaf wins outright. Only when the row projects to no line of its
// own is a collapsed summary consulted, and then only a summary that actually
// claims the row as a member — a family is never guessed at from the row's own
// FamilyKey, because a key names a family that the current filter may have
// reduced to a single leaf or removed entirely.
//
// False is the answer for an out-of-range row and for a row that is in the set
// but in no line's reach, which cannot happen in a projection either builder
// produced. It is the fail-closed half of the same rule Session and SessionAt
// keep: a target nothing can resolve is refused rather than approximated.
func (l ItemList) ResolveRowTarget(row int) (RowTarget, bool) {
	if row < 0 || row >= len(l.Rows) {
		return RowTarget{}, false
	}
	for i, it := range l.Items {
		if it.Kind == ItemSession && it.Row == row {
			return RowTarget{Item: i, Row: row, FamilyKey: it.FamilyKey}, true
		}
	}
	for i, it := range l.Items {
		if it.Kind != ItemFamilySummary || !it.Collapsed {
			continue
		}
		for _, member := range it.Members {
			if member == row {
				return RowTarget{Item: i, Row: row, FamilyKey: it.FamilyKey, Reveal: true}, true
			}
		}
	}
	return RowTarget{}, false
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

// countInGroup reports how many SESSIONS sit in the given group. This is the
// number the group heading renders.
//
// It counts rows rather than projected items, and the difference is the whole
// reason it is written this way. A folded family projects to one summary line and
// its members to none, so an item-counted heading would say `Needs you (1)` over
// a family hiding four sessions that do — the heading would shrink as the
// operator folded things away, which is the one thing a count above a folded
// list must not do. Rows is the filtered set, so the heading still answers for
// what the active filter kept and nothing more.
//
// It counts by band, so a pinned session is counted under Pinned, where it is
// listed, and not again under its urgency.
func (l ItemList) countInGroup(group int, lastActivity map[int]time.Time, threshold time.Duration) int {
	n := 0
	for i := range l.Rows {
		if bandOf(l.Rows[i], lastActivity, threshold) == group {
			n++
		}
	}
	return n
}
