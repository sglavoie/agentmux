// This file names the two ways the dashboard can arrange the same filtered
// sessions. The filtered, urgency-sorted row set stays the single source of
// truth in both; a view decides only which lines that row set projects onto.

package dashboard

import "time"

// ViewMode names which projection the dashboard list is showing.
//
// ViewUrgency is the zero value, and deliberately so: every Model, every test
// fixture, and every scenario that never mentions a view gets the historical
// list back without saying anything. A view that had to be set to behave as it
// always has would make "which view is this" a question every construction path
// could answer differently.
type ViewMode int

const (
	// ViewUrgency is the historical list: one line per filtered session, ordered
	// by urgency group and labelled with the group headings.
	ViewUrgency ViewMode = iota
	// ViewProjects arranges the same filtered sessions by the family they were
	// discovered in. Deterministic multi-session families become a summary line
	// with their sessions indented beneath it; singletons, ambiguous sessions,
	// and orphans stay top-level leaves.
	ViewProjects
)

// String is the operator-facing name of the view, used wherever the current
// arrangement is reported back.
func (v ViewMode) String() string {
	if v == ViewProjects {
		return "Projects"
	}
	return "Urgency"
}

// ShowsGroupHeadings reports whether RenderList will draw urgency group labels
// and their separators for this projection. The renderer, the fit walk, and the
// root's scroll math all ask here rather than each deciding for itself, because
// a caller that charged different chrome than the renderer draws would let the
// cursor select a line the renderer then drops.
//
// Projects omits the headings entirely: its lines are ordered by family, so a
// heading would claim to introduce a run of same-group rows that the ordering
// does not actually produce. The per-row group border stays in both views — it
// qualifies one row rather than introducing a run of them.
func ShowsGroupHeadings(view ViewMode, items ItemList, lastActivity map[int]time.Time, threshold time.Duration) bool {
	if view == ViewProjects {
		return false
	}
	return items.CountGroups(lastActivity, threshold) > 1
}
