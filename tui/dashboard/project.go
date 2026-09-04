// This file turns the filtered session list into the Projects arrangement:
// which sessions share a family, which families are worth a summary line, and
// what order the resulting lines come out in.
//
// It reads the filtered rows and nothing else. Every fact it groups by was
// stamped onto the session at discovery time, so building the projection costs
// no Git probe, no Kitty call, and no config lookup, and running it twice on the
// same input produces the same lines.

package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// BuildItemsFor projects the filtered rows onto the lines the given view draws.
// It is the one place that maps a view onto a builder, so the root never has to
// name a builder and cannot pair a view with the wrong projection.
//
// collapsed is read only by ViewProjects; ViewUrgency has nothing to collapse.
func BuildItemsFor(view ViewMode, rows []session.RunningSession, collapsed map[string]bool) ItemList {
	if view == ViewProjects {
		return BuildProjectItems(rows, collapsed)
	}
	return BuildItems(rows)
}

// BuildProjectItems projects the filtered rows onto the Projects arrangement.
//
// A family is a set of rows sharing a non-empty FamilyKey. Only a family with
// more than one row earns a summary line; a one-row family and a row with no
// family at all — the ambiguous and the orphaned, which session 1's resolver
// deliberately leaves unstamped — both come out as a single top-level leaf.
// That is what keeps a summary a claim about a group rather than decoration on
// a lone session.
//
// Ordering is taken entirely from the source, which is already urgency-sorted:
// a family sits where its earliest row sat, and its members keep the order they
// had. Nothing here re-sorts, and nothing here reads the clock beyond the
// activity facts its caller passes to the counting helpers, so the same rows
// always produce the same lines.
//
// collapsed is keyed by family key; an absent entry means expanded, so a caller
// may pass nil for "everything open". A collapsed family still emits its
// summary, and that summary still carries every member index — a folded family
// that forgot its members would also forget that one of them needs attention.
func BuildProjectItems(rows []session.RunningSession, collapsed map[string]bool) ItemList {
	// A unit is one thing the source order can name: either a family (recorded
	// at the position of its first row) or a single unfamilied row. Walking the
	// units in discovery order is what makes "families sit where their earliest
	// child sat" fall out of the walk instead of needing a sort of its own.
	type family struct {
		key     string
		label   string
		members []int
	}
	type unit struct {
		fam *family
		row int
	}

	byKey := make(map[string]*family)
	units := make([]unit, 0, len(rows))
	for i, s := range rows {
		if s.FamilyKey == "" {
			units = append(units, unit{row: i})
			continue
		}
		fam, seen := byKey[s.FamilyKey]
		if !seen {
			fam = &family{key: s.FamilyKey}
			byKey[s.FamilyKey] = fam
			units = append(units, unit{fam: fam, row: i})
		}
		// The first member carrying a label names the family. Members of one
		// family agree on their label in practice; taking the first non-empty one
		// means a row whose label was lost to a degraded probe cannot rename a
		// family its siblings already named.
		if fam.label == "" {
			fam.label = s.FamilyLabel
		}
		fam.members = append(fam.members, i)
	}

	items := make([]Item, 0, len(rows)+len(byKey))
	for _, u := range units {
		if u.fam == nil || len(u.fam.members) == 1 {
			items = append(items, sessionItem(rows, u.row))
			continue
		}
		folded := collapsed[u.fam.key]
		items = append(items, Item{
			Kind:        ItemFamilySummary,
			Row:         -1,
			Members:     append([]int(nil), u.fam.members...),
			FamilyKey:   u.fam.key,
			FamilyLabel: u.fam.label,
			Collapsed:   folded,
		})
		if folded {
			continue
		}
		for _, row := range u.fam.members {
			child := sessionItem(rows, row)
			child.Depth = 1
			items = append(items, child)
		}
	}
	return ItemList{Rows: rows, Items: items}
}

// sessionItem builds the leaf standing for one row. Both builders go through it
// so a session line means the same thing in either view.
func sessionItem(rows []session.RunningSession, row int) Item {
	return Item{
		Kind:        ItemSession,
		Row:         row,
		FamilyKey:   rows[row].FamilyKey,
		FamilyLabel: rows[row].FamilyLabel,
	}
}

// SummaryCounts reports how many of a summary's members sit in each urgency
// bucket, indexed by group. Because the projection is built from the already
// filtered rows, every member it counts is a match the operator can currently
// see: filtering a family down to one visible session leaves a leaf rather than
// a summary claiming a count nobody can reach, and filtering it away entirely
// removes the family from the list along with its rows.
//
// Every bucket decision is StateGroup's, so the blocked-reason and unseen-stop
// rules a family summarizes cannot drift from the ones its own rows render
// under. A non-summary item, and a member index pointing outside the row set,
// contribute nothing.
func (l ItemList) SummaryCounts(it Item, lastActivity map[int]time.Time, threshold time.Duration) [3]int {
	var counts [3]int
	if it.Kind != ItemFamilySummary {
		return counts
	}
	for _, row := range it.Members {
		if row < 0 || row >= len(l.Rows) {
			continue
		}
		g := StateGroup(l.Rows[row], lastActivity, threshold)
		if g < 0 || g >= len(counts) {
			continue
		}
		counts[g]++
	}
	return counts
}

// SummaryLead returns the member whose urgency the summary speaks with: the
// most urgent one, ties broken by source order so the answer is the same on
// every rebuild. It is the session whose state icon the summary paints, and its
// group is exactly the group GroupAt gives the summary.
//
// It reports false for a non-summary item and for a summary whose members are
// all out of range, which is the same "nothing to claim" answer GroupAt
// resolves to Idle.
func (l ItemList) SummaryLead(it Item, lastActivity map[int]time.Time, threshold time.Duration) (session.RunningSession, bool) {
	if it.Kind != ItemFamilySummary {
		return session.RunningSession{}, false
	}
	best := idleGroup + 1
	var lead session.RunningSession
	found := false
	for _, row := range it.Members {
		if row < 0 || row >= len(l.Rows) {
			continue
		}
		if g := StateGroup(l.Rows[row], lastActivity, threshold); g < best {
			best = g
			lead = l.Rows[row]
			found = true
		}
	}
	return lead, found
}
