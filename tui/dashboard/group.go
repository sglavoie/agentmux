package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/session"
)

const (
	// GroupBorderGlyph is the 1-cell left border drawn for each row of a group.
	GroupBorderGlyph = "▌"
	// groupBorderWidth is the visual width consumed by the border + trailing space.
	groupBorderWidth = 2
)

// The three urgency buckets StateGroup answers with, named here rather than
// spelled as literals at each site. They are declared beside StateGroup and
// groupLabels because this file is where a bucket is decided and where it is
// given the name the operator reads: the `!attention` filter, the status bar's
// tally, and the group heading all have to mean the same three things, and a
// bare 0 in three packages' worth of call sites is how they would stop.
//
// idleGroup doubles as the answer a projection gives when it has no answer to
// give — it is the bucket that claims the least.
const (
	attentionGroup = 0
	activeGroup    = 1
	idleGroup      = 2
)

// pinnedBand is the heading pinned sessions are listed under, above Needs you.
// It is a band and never a bucket: StateGroup does not answer it, so a pinned
// session still counts, filters, navigates, and wears its border by its
// urgency. Only the urgency view's order and headings know it (bandOf).
const pinnedBand = 3

// bandOf is the heading a session is listed under in the urgency view: Pinned
// for a pinned session (the ★ in front of its title), else its urgency bucket.
// A pinned session that needs the operator stays under Pinned, which already
// leads the list, as it does in the workspace sidebar.
func bandOf(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	if s.Starred {
		return pinnedBand
	}
	return StateGroup(s, lastActivity, threshold)
}

// StateGroup classifies a session into one of three buckets:
//
//	0 → Needs Attention (key, error, question, or an UNSEEN stop)
//	1 → Active (bolt, warning, subagent, compact, or empty state within the
//	    idle threshold, or empty state with no activity record)
//	2 → Idle (a SEEN stop, or empty state with stale activity beyond the
//	    threshold)
//
// The warning state is grouped under Active so a session whose last tool
// call failed (carrying the ⚠️ icon) stays visible alongside bolt rows
// instead of hiding under Idle. The terminal error state (❗) stays under
// Needs Attention so stop failures remain high-signal. subagent and compact
// are mid-task states the daemon emits while work is in progress, so they
// classify alongside bolt; question is the elicitation prompt, which requires
// user input and so groups under Needs Attention.
//
// stop is the one state whose bucket is not decided by its name alone, and it
// is the reason Needs Attention now drains. A finished turn is worth the top of
// the list exactly once — until the user has looked at it. Unseen carries that,
// daemon-owned and inverted so that its zero value means seen: a row with no
// daemon behind it, a client too old to read the field, and every non-stop
// state all demote quietly rather than pinning the group open. A seen stop
// therefore lands in Idle beside the stale rows, where a session that finished
// hours ago belongs, instead of sitting above one on a live permission prompt.
//
// Without a daemon-recorded activity timestamp a session is treated as
// Active rather than Idle so a fresh session does not flicker into the
// Idle bucket the moment it appears.
func StateGroup(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	switch s.State {
	case "key", "error", "question":
		return 0
	case "stop":
		// Decided inside the switch rather than in the tail below, so an
		// unknown/legacy state name keeps reaching the tail's own Idle rule
		// instead of being answered by this one.
		if s.Unseen {
			return 0
		}
		return 2
	case "bolt", "warning", "subagent", "compact":
		return 1
	}
	if s.State != "" {
		// Unknown/legacy state names — keep the previous behavior (Idle).
		return 2
	}
	ts, ok := lastActivity[s.WindowID]
	if !ok {
		return 1
	}
	if threshold > 0 && time.Since(ts) >= threshold {
		return 2
	}
	return 1
}

// CountGroups returns how many distinct state groups the given rows span.
// The list renders group labels and borders only when this is greater than
// one, so pagination and scroll math must agree with it.
func CountGroups(sessions []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	seen := make(map[int]bool)
	for _, s := range sessions {
		seen[StateGroup(s, lastActivity, threshold)] = true
	}
	return len(seen)
}

// groupBorder returns the colored 1-cell left border glyph for the given
// group (0 = attention, 1 = active, 2 = idle).
func groupBorder(group int) string {
	switch group {
	case 0:
		return styleGroupBorderAttention.Render(GroupBorderGlyph)
	case 1:
		return styleGroupBorderActive.Render(GroupBorderGlyph)
	default:
		return styleGroupBorderIdle.Render(GroupBorderGlyph)
	}
}

// groupLabelStyle returns the emphasis a group's name is written in. It is
// shared by the urgency heading and the family summary's tally so a bucket
// looks the same wherever it is named.
func groupLabelStyle(group int) lipgloss.Style {
	switch group {
	case pinnedBand:
		return styleGroupLabelPinned
	case 0:
		return styleGroupLabelAttention
	case 1:
		return styleGroupLabelActive
	default:
		return styleGroupLabelIdle
	}
}

// renderGroupLabel emits the left-aligned heading above the first row of a
// group, with the number of SESSIONS in it.
//
// indent is the width of whatever the walk prefixes its rows with — the group
// border and its space, or nothing when the pass draws no borders — passed in
// rather than assumed, so the heading starts on the same cell as the row content
// under it in both cases. A heading hard-coded to the border's indent would hang
// two cells to the right of its own rows whenever the borders were not drawn.
func renderGroupLabel(group, count, indent int) string {
	label := fmt.Sprintf("%s (%d)", groupLabels[group], count)
	if indent < 0 {
		indent = 0
	}
	return strings.Repeat(" ", indent) + groupLabelStyle(group).Render(label)
}

// groupLabels name the three urgency buckets in the queue's own voice: what the
// operator is being asked to DO about the rows under the heading, not what state
// the harness reported.
//
// `Needs you` rather than `Needs Attention` because the queue exists to answer
// "where do I go next", and the answer is a person's turn rather than a
// property of the session. `Running` rather than `Active` because a row in that
// bucket is one the harness is working on and the operator is not — `Active` was
// ambiguous between the two in exactly the moment the distinction matters.
//
// `Pinned` is not a bucket but the band above them (pinnedBand), named here so
// the heading is spelled where the others are.
var groupLabels = [4]string{"Needs you", "Running", "Idle", "Pinned"}

// compareRowPriority compares the operational priority of two rows. A negative
// result means a should precede b; zero deliberately leaves their incoming
// order untouched. The caller supplies that final order (normally
// session.SortRunning), so configured sorting and pins remain the deterministic
// fallback without outranking urgency, severity, or presentation recency.
//
// PresentationChangedAt is the only recency signal consulted here. In
// particular, BusySince and the activity map are not recency substitutes: the
// activity map is used only by StateGroup to decide whether an iconless row is
// Active or Idle.
func compareRowPriority(a, b session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	ga := StateGroup(a, lastActivity, threshold)
	gb := StateGroup(b, lastActivity, threshold)
	if ga != gb {
		if ga < gb {
			return -1
		}
		return 1
	}

	ra := rowSubtypeRank(a, ga)
	rb := rowSubtypeRank(b, gb)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}

	az := a.PresentationChangedAt.IsZero()
	bz := b.PresentationChangedAt.IsZero()
	if az != bz {
		if az {
			return 1
		}
		return -1
	}
	if !az && !a.PresentationChangedAt.Equal(b.PresentationChangedAt) {
		if a.PresentationChangedAt.After(b.PresentationChangedAt) {
			return -1
		}
		return 1
	}
	return 0
}

// rowSubtypeRank orders the state variants inside one urgency group. Idle has
// no subtype priority: its seen stops, stale iconless rows, and unknown legacy
// states all reach the presentation clock together.
func rowSubtypeRank(s session.RunningSession, group int) int {
	switch group {
	case attentionGroup:
		switch s.State {
		case "error":
			return 0
		case "key", "question":
			return 1
		case "stop":
			return 2
		}
	case activeGroup:
		switch s.State {
		case "warning":
			return 0
		case "bolt", "subagent", "compact":
			return 1
		case "":
			return 2
		}
	}
	return 0
}

// SortRows applies the shared priority relation as a stable in-place sort,
// after putting pinned sessions first: they lead the list whatever their
// urgency, and keep the session order among themselves. Sessions with no
// state icon are bucketed with the activity map and idle threshold; ties
// preserve the incoming session.SortRunning order.
//
// The pin is applied here rather than in compareRowPriority, which also picks
// a family's lead: a pin places a row, it does not make it more urgent.
func SortRows(rows []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Starred != rows[j].Starred {
			return rows[i].Starred
		}
		if rows[i].Starred {
			return false
		}
		return compareRowPriority(rows[i], rows[j], lastActivity, threshold) < 0
	})
}
