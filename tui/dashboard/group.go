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
	case 0:
		return styleGroupLabelAttention
	case 1:
		return styleGroupLabelActive
	default:
		return styleGroupLabelIdle
	}
}

// renderGroupLabel emits the left-aligned tag above the first row of a group.
// The label is indented to line up with the row content (border + space).
func renderGroupLabel(group, count int) string {
	label := fmt.Sprintf("%s (%d)", groupLabels[group], count)
	return strings.Repeat(" ", groupBorderWidth) + groupLabelStyle(group).Render(label)
}

var groupLabels = [3]string{"Needs Attention", "Active", "Idle"}

// SortRows applies a stable sort so attention rows come first, then active,
// then idle — preserving existing order within each group. Sessions with no
// state icon are bucketed with the activity map and idle threshold.
//
// It names no state of its own: every bucket decision is StateGroup's, and the
// only state name it reads is the empty one, inside the Active tie-break. That
// is why the unseen demotion needed no change here — a seen stop simply comes
// back as group 2 and sorts with the idle rows.
func SortRows(rows []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) {
	sort.SliceStable(rows, func(i, j int) bool {
		gi := StateGroup(rows[i], lastActivity, threshold)
		gj := StateGroup(rows[j], lastActivity, threshold)
		if gi != gj {
			return gi < gj
		}
		// Within Active (group 1), sessions with an explicit state icon sort
		// before those without.
		if gi == 1 {
			si := rows[i].State != ""
			sj := rows[j].State != ""
			if si != sj {
				return si
			}
		}
		return false
	})
}
