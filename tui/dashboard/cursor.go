package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// CursorIdentity is everything the dashboard remembers about the item under the
// cursor so it can find that item again after the list is rebuilt. The fields
// are ordered by how exactly they identify a live instance: the Kitty tab and
// window IDs are exact, the session key is exact for a keyed session, and the
// base name is a last resort that is disambiguated by the launch identity
// fields below it.
//
// Kind and FamilyKey sit above them because they say what was selected at all.
// A summary has no tab and no window of its own, so without the kind a captured
// summary would restore through the session chain, find nothing, and fall
// through to the attention fallback — losing a selection that is still on
// screen.
type CursorIdentity struct {
	Kind      ItemKind
	FamilyKey string
	TabID     int
	WindowID  int
	Key       string
	Base      string
	Profile   string
	Project   string
	Harness   string
	Directory string
}

// CaptureCursorIdentity snapshots a running session before a refresh replaces
// the row set.
func CaptureCursorIdentity(s session.RunningSession) CursorIdentity {
	return CursorIdentity{
		Kind:      ItemSession,
		FamilyKey: s.FamilyKey,
		TabID:     s.TabID,
		WindowID:  s.WindowID,
		Key:       s.Key,
		Base:      s.Base,
		Profile:   s.EffectiveProfile(),
		Project:   s.EffectiveProject(),
		Harness:   s.Harness,
		Directory: s.Directory,
	}
}

// CaptureItemIdentity snapshots the item at index i. An out-of-range index
// yields the zero identity, which restores to the ordinary fallback — an empty
// list has no prior selection worth preserving.
func CaptureItemIdentity(list ItemList, i int) CursorIdentity {
	it, ok := list.At(i)
	if !ok {
		return CursorIdentity{}
	}
	if s, isSession := list.Session(it); isSession {
		return CaptureCursorIdentity(s)
	}
	return CursorIdentity{Kind: it.Kind, FamilyKey: it.FamilyKey}
}

// RestoreCursor picks the cursor row after the session list is rebuilt.
// Preference order: (1) the row whose Base matches priorBase, (2) the first
// attention-state row (orange band), (3) row 0. Returns 0 for an empty list so
// callers can use the result directly without clamping.
func RestoreCursor(rows []session.RunningSession, priorBase string, lastActivity map[int]time.Time, idle time.Duration) int {
	return RestoreCursorByIdentity(rows, CursorIdentity{Base: priorBase}, lastActivity, idle)
}

// RestoreCursorByIdentity resolves a captured identity against a rebuilt row
// set. It is the row-shaped face of RestoreItemCursor: every row projects to one
// item at the same index, so resolving against the projection answers with an
// index into rows unchanged.
func RestoreCursorByIdentity(rows []session.RunningSession, prior CursorIdentity, lastActivity map[int]time.Time, idle time.Duration) int {
	return RestoreItemCursor(BuildItems(rows), prior, lastActivity, idle)
}

// RestoreItemCursor resolves a captured identity against a rebuilt projection,
// walking the identity fields from exact to approximate and falling back to the
// first attention item, then item 0.
//
// The two kinds cross over in one direction each. A session that has since been
// folded into a family summary restores onto that summary, and a summary that
// has since expanded restores onto one of its members: in both cases the row the
// operator was looking at is still on screen, just under a different kind, and
// landing on it beats falling through to the attention fallback.
func RestoreItemCursor(list ItemList, prior CursorIdentity, lastActivity map[int]time.Time, idle time.Duration) int {
	if list.Len() == 0 {
		return 0
	}
	if prior.Kind == ItemFamilySummary {
		if prior.FamilyKey != "" {
			if i := findFamilySummary(list, prior.FamilyKey); i >= 0 {
				return i
			}
			if i := findFamilyMember(list, prior.FamilyKey); i >= 0 {
				return i
			}
		}
		return firstAttentionItem(list, lastActivity, idle)
	}
	if prior.TabID != 0 {
		if i := findSession(list, func(s session.RunningSession) bool { return s.TabID == prior.TabID }); i >= 0 {
			return i
		}
	}
	if prior.WindowID != 0 {
		if i := findSession(list, func(s session.RunningSession) bool { return s.WindowID == prior.WindowID }); i >= 0 {
			return i
		}
	}
	if prior.Key != "" {
		if i := findSession(list, func(s session.RunningSession) bool { return s.Key == prior.Key }); i >= 0 {
			return i
		}
	}
	if prior.Base != "" {
		if i := findSession(list, func(s session.RunningSession) bool {
			return s.Base == prior.Base &&
				s.EffectiveProfile() == prior.Profile &&
				s.EffectiveProject() == prior.Project &&
				s.Harness == prior.Harness &&
				s.Directory == prior.Directory
		}); i >= 0 {
			return i
		}
		if i := findSession(list, func(s session.RunningSession) bool { return s.Base == prior.Base }); i >= 0 {
			return i
		}
	}
	if prior.FamilyKey != "" {
		if i := findFamilySummary(list, prior.FamilyKey); i >= 0 {
			return i
		}
	}
	return firstAttentionItem(list, lastActivity, idle)
}

// findSession returns the index of the first session item whose row satisfies
// match, or -1. Non-session items are skipped rather than tested, so a summary
// can never answer a question asked about a session.
func findSession(list ItemList, match func(session.RunningSession) bool) int {
	for i := range list.Items {
		s, ok := list.SessionAt(i)
		if !ok {
			continue
		}
		if match(s) {
			return i
		}
	}
	return -1
}

// findFamilySummary returns the index of the summary standing for key, or -1.
func findFamilySummary(list ItemList, key string) int {
	for i, it := range list.Items {
		if it.Kind == ItemFamilySummary && it.FamilyKey == key {
			return i
		}
	}
	return -1
}

// findFamilyMember returns the index of the first session item belonging to
// key, or -1.
func findFamilyMember(list ItemList, key string) int {
	for i, it := range list.Items {
		if it.Kind == ItemSession && it.FamilyKey == key {
			return i
		}
	}
	return -1
}

// firstAttentionItem returns the index of the first item in the Needs Attention
// group, or 0 when no item is in it.
func firstAttentionItem(list ItemList, lastActivity map[int]time.Time, idle time.Duration) int {
	for i := range list.Items {
		if list.GroupAt(i, lastActivity, idle) == 0 {
			return i
		}
	}
	return 0
}
