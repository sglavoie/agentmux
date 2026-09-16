// What order the tiles come in.
//
// At opening, the grid surfaces sessions needing attention, then Done sessions,
// then other non-Working sessions, then Working sessions. Within each class its identity
// order is deterministic. Subsequent reads retain the existing tile positions:
// a harness starting or finishing a turn must not move the pane being read.
//
// Within an opening tier, grouping is by resolved project identity so a
// repository's checkouts sit together when they share a tier. Groups are
// ordered by their display label case-insensitively,
// because `API` and `api` are one project to a reader and putting every
// capitalized name before every lowercase one is the ASCII sort's idea of
// alphabetical, not a person's.
package overview

import (
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// The two namespaces a group identity can come from. They are prefixed rather
// than compared bare so a family key can never collide with a project label that
// happens to spell the same string — the tie-breaker has to be deterministic,
// and two different groups sharing one identity is the one way it would not be.
const (
	familyIdentityPrefix = "family\x00"
	labelIdentityPrefix  = "label\x00"
)

// Order returns the identity tie-break order, leaving the caller's slice alone.
//
// The comparison is a total order on (lowercased label, label, identity, tab,
// window, key), so the result is the same on every call for the same input and
// contiguous per group falls out of the sort rather than needing a second pass.
// The last three components are the within-group order: the stable Kitty tab ID
// first, with the window ID and the logical key behind it so two rows that
// somehow share a tab still come out in one fixed order.
func Order(sessions []session.RunningSession) []session.RunningSession {
	out := make([]session.RunningSession, len(sessions))
	copy(out, sessions)
	sort.SliceStable(out, func(i, j int) bool {
		return less(out[i], out[j])
	})
	return out
}

// OpeningOrder ranks the first successful snapshot. Attention uses the same
// grouping as the dashboard; Done and Working use the agent turn that the
// overview's tile actually displays, which can differ from the aggregate ASL state.
func OpeningOrder(sessions []session.RunningSession, activity map[int]time.Time, idleThreshold time.Duration) []session.RunningSession {
	out := Order(sessions)
	sort.SliceStable(out, func(i, j int) bool {
		return openingRank(out[i], activity, idleThreshold) < openingRank(out[j], activity, idleThreshold)
	})
	return out
}

func openingRank(s session.RunningSession, activity map[int]time.Time, idleThreshold time.Duration) int {
	if dashboard.StateGroup(s, activity, idleThreshold) == 0 {
		return 0
	}
	state := s.State
	if s.AgentTurn != nil {
		state = s.AgentTurn.IconState
	}
	// Completed turns need inspection even if the daemon has already marked
	// them seen. Keep them ahead of mid-task states such as Compacting.
	if state == "stop" {
		return 1
	}
	if state != "bolt" {
		return 2
	}
	return 3
}

// RetainOrder refreshes the facts without moving surviving tabs. New sessions
// join at the end in identity order; closed tabs simply leave their slots.
func RetainOrder(previous, current []session.RunningSession) []session.RunningSession {
	ordered := Order(current)
	byTab := make(map[int][]session.RunningSession, len(ordered))
	for _, s := range ordered {
		byTab[s.TabID] = append(byTab[s.TabID], s)
	}
	out := make([]session.RunningSession, 0, len(ordered))
	for _, old := range previous {
		if rows := byTab[old.TabID]; len(rows) > 0 {
			out = append(out, rows[0])
			byTab[old.TabID] = rows[1:]
		}
	}
	for _, s := range ordered {
		if rows := byTab[s.TabID]; len(rows) > 0 {
			out = append(out, rows[0])
			byTab[s.TabID] = rows[1:]
		}
	}
	return out
}

// less is Order's comparison, extracted so the ordering rule is one readable
// cascade rather than a nest of returns inside a closure.
func less(a, b session.RunningSession) bool {
	ka, la := groupIdentity(a)
	kb, lb := groupIdentity(b)

	if folded := strings.ToLower(la); folded != strings.ToLower(lb) {
		return folded < strings.ToLower(lb)
	}
	// Same word to a reader, different bytes. The exact label decides, so two
	// groups that only differ in casing still have a fixed order between them.
	if la != lb {
		return la < lb
	}
	if ka != kb {
		return ka < kb
	}

	if a.TabID != b.TabID {
		return a.TabID < b.TabID
	}
	if a.WindowID != b.WindowID {
		return a.WindowID < b.WindowID
	}
	return a.Key < b.Key
}

// groupIdentity resolves a session onto the group it tiles with and the label
// that group is ordered by.
//
// The identity is the discovered repository family when there is one, which is
// what puts two worktrees of one repository side by side even though they are
// separate configured projects with separate directories. A session discovery
// could not resolve a family for — no config, or configured projects that
// disagreed about which family owns its directory — groups by its own label
// instead, which is the least a row can be grouped by without being grouped with
// something it is not.
//
// The label falls back through ProjectLabel to SessionDisplayTitle, so a row with
// no family and no configured project is still ordered by the name its tile
// actually shows rather than by an empty string that would pile every such row at
// the top in discovery order.
func groupIdentity(s session.RunningSession) (identity, label string) {
	label = dashboard.ProjectLabel(s)
	if label == "" {
		label = dashboard.SessionDisplayTitle(s)
	}
	if s.FamilyKey != "" {
		return familyIdentityPrefix + s.FamilyKey, label
	}
	return labelIdentityPrefix + label, label
}
