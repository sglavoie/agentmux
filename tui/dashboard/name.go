// This file answers one question: when two live sessions show the same name,
// what does the queue add to tell them apart?
//
// It exists because the name a row shows is a PROJECT title, not a session
// identity. Two managed worktrees of one repository, the same project opened
// under two profiles, and two tabs launched from one configured project all
// render the same word — and the queue's one-line row has dropped the profile
// cell, the harness cell, and the directory that used to separate them. Adding
// nothing would leave three identical lines; adding the context to every row
// would make the common case (no collision at all) read like a debug dump.
//
// Two rules are load-bearing and are the reason this is a value built once per
// pass rather than a function over a row:
//
//   - Collisions are measured across the LIVE session set, not the filtered one.
//     Filtering is a lens, and a lens that renamed the rows under it would make
//     a session's name depend on what else the operator happened to be looking
//     at — the row would rename itself as chips were added and removed. The
//     disambiguator is therefore constructed from the live set and then applied
//     to whatever subset is being drawn.
//   - A qualifier is DISPLAY, never a key. Nothing here is stored on a session,
//     compared against a filter, or captured into a cursor identity: the filter
//     still matches the raw title, and CursorIdentity still travels on Base,
//     Key, and the Kitty IDs. See TestQualifiersAreDisplayOnly.

package dashboard

import (
	"path/filepath"
	"strconv"

	"github.com/sglavoie/agentmux/internal/session"
)

// qualifierSeparator joins the two halves of the one compound level. A middle
// dot rather than a slash or a comma: a branch name may contain either, and a
// separator a fact can also contain is a separator that sometimes reads as part
// of the fact.
const qualifierSeparator = " · "

// qualifierLevel is one way of telling colliding rows apart, and the levels are
// ordered by how much they mean to a reader.
type qualifierLevel int

const (
	// qualifierNone is the answer for a name that does not collide, and also the
	// honest answer for a collision nothing separates: two rows whose every
	// distinguishing fact is missing are two rows this package cannot tell apart,
	// and inventing a difference would be worse than admitting there is none.
	qualifierNone qualifierLevel = iota
	// qualifierBranch is the checkout's branch — the fact managed worktrees make
	// the ordinary discriminator, and the one an operator is already thinking in.
	// It is the RAW branch, not branchText's rendering of it: the dirty marker
	// belongs to a column about the checkout's condition, and carrying it here
	// would imply that two rows on one branch are told apart by which of them has
	// unsaved work.
	qualifierBranch
	// qualifierWorktree is the checkout's directory leaf, used when the branch is
	// unavailable: a non-repository, a detached HEAD, `git_probe = false`, a
	// failed or not-yet-run probe, or no daemon at all. internal/gitstate reports
	// every one of those as an empty branch, so this level is what keeps two
	// worktrees separable when Git said nothing about either.
	qualifierWorktree
	// qualifierProfile is the launch identity. It sits below the checkout levels
	// because two rows in one repository are the case the queue was redesigned
	// around, and because the profile is what the inspector shows anyway.
	qualifierProfile
	// qualifierCheckoutProfile is the last readable resort: checkout context and
	// profile together, for the set neither separates alone (three rows across two
	// branches and two profiles, say). It takes the branch when there is one and
	// the directory leaf otherwise, because by this level a readable qualifier is
	// worth more than a homogeneous vocabulary.
	qualifierCheckoutProfile
	// qualifierWindow is the stable session identifier — the Kitty window ID,
	// which is exact and unique but means nothing on sight. It is last for that
	// reason: it always works and never explains.
	qualifierWindow
)

// qualifierEscalation is the order the levels are tried in. The first level that
// gives every colliding row a non-empty, distinct qualifier wins for the whole
// group, so all the rows of one collision are told apart by the same KIND of
// fact — a group where one row was named by its branch and its neighbour by its
// profile would read as two unrelated annotations rather than as one comparison.
var qualifierEscalation = []qualifierLevel{
	qualifierBranch,
	qualifierWorktree,
	qualifierProfile,
	qualifierCheckoutProfile,
	qualifierWindow,
}

// NameDisambiguator decides what, if anything, a row's name needs beside it.
//
// The zero value is usable and qualifies nothing, which is exactly right for a
// caller with no live set: a dashboard that cannot see the other sessions has no
// evidence that any name collides.
type NameDisambiguator struct {
	// levels maps a colliding display name to the level that separates its rows.
	// A name absent from the map does not collide (or collides unsalvageably),
	// which is why the qualifier is derived from the session on demand rather than
	// stored per session: there is no session identity to key by that a caller
	// could not pair wrongly, and a level plus a row is enough to recompute the
	// answer.
	levels map[string]qualifierLevel
}

// NewNameDisambiguator measures collisions across the live session set.
//
// Names are compared exactly rather than case-insensitively: `api` and `API` are
// two different words on screen, and a reader who can see the difference does
// not need it explained. This is deliberately NOT the rule CanonicalProjectLabels
// uses, which folds case because it is aggregating one project's rows under one
// heading; here the question is whether two LINES look alike.
func NewNameDisambiguator(live []session.RunningSession) NameDisambiguator {
	groups := make(map[string][]session.RunningSession, len(live))
	for _, s := range live {
		name := SessionDisplayTitle(s)
		if name == "" {
			continue
		}
		groups[name] = append(groups[name], s)
	}

	var levels map[string]qualifierLevel
	for name, members := range groups {
		if len(members) < 2 {
			continue
		}
		level := separatingLevel(members)
		if level == qualifierNone {
			continue
		}
		if levels == nil {
			levels = make(map[string]qualifierLevel)
		}
		levels[name] = level
	}
	return NameDisambiguator{levels: levels}
}

// Qualifier returns the disambiguating context for a row, or "" when its name
// stands alone. A row whose name collides but whose own fact at the chosen level
// is missing also answers "" — the level was only chosen because every member
// had one, so this can only be reached by a caller applying a disambiguator to a
// session that was not in the live set it was built from.
func (d NameDisambiguator) Qualifier(s session.RunningSession) string {
	level, ok := d.levels[SessionDisplayTitle(s)]
	if !ok {
		return ""
	}
	return qualifierText(s, level)
}

// Label composes the name and its qualifier into the one string a surface with
// no styling of its own can print. Surfaces that style the two halves
// differently — the queue draws the name neutral and the qualifier muted — read
// SessionDisplayTitle and Qualifier separately instead.
func (d NameDisambiguator) Label(s session.RunningSession) string {
	name := SessionDisplayTitle(s)
	qualifier := d.Qualifier(s)
	if qualifier == "" {
		return name
	}
	return name + " (" + qualifier + ")"
}

// separatingLevel returns the first level in the escalation that separates the
// group, or qualifierNone when none does.
func separatingLevel(members []session.RunningSession) qualifierLevel {
	for _, level := range qualifierEscalation {
		if separates(members, level) {
			return level
		}
	}
	return qualifierNone
}

// separates reports whether a level gives every member a non-empty, distinct
// qualifier. Both halves are required. Distinctness is the point of the whole
// exercise; non-emptiness is what keeps a group from being "separated" by the
// absence of a fact, where one row would show context and its neighbour would
// show a bare name that reads as the only one of its kind.
func separates(members []session.RunningSession, level qualifierLevel) bool {
	seen := make(map[string]bool, len(members))
	for _, s := range members {
		text := qualifierText(s, level)
		if text == "" || seen[text] {
			return false
		}
		seen[text] = true
	}
	return true
}

// qualifierText renders one row's fact at one level, or "" when the row has no
// such fact.
func qualifierText(s session.RunningSession, level qualifierLevel) string {
	switch level {
	case qualifierBranch:
		return s.Branch
	case qualifierWorktree:
		return directoryLeaf(s.Directory)
	case qualifierProfile:
		return s.EffectiveProfile()
	case qualifierCheckoutProfile:
		checkout := checkoutContext(s)
		profile := s.EffectiveProfile()
		if checkout == "" || profile == "" {
			return ""
		}
		return checkout + qualifierSeparator + profile
	case qualifierWindow:
		// A session outside Kitty has no window number an operator could
		// recognize; its pane in its own backend is the fact that tells two
		// such rows apart.
		if !s.Pane.IsZero() && s.Backend != "" {
			return string(s.Backend) + " " + s.Pane.ID
		}
		if s.WindowID <= 0 {
			return ""
		}
		return "#" + strconv.Itoa(s.WindowID)
	}
	return ""
}

// checkoutContext is the branch when the daemon named one and the checkout's
// directory leaf otherwise. It is the compound level's first half only: the two
// facts are separate levels above it precisely so that a group is not told apart
// by a branch on one row and a folder name on the next.
func checkoutContext(s session.RunningSession) string {
	if s.Branch != "" {
		return s.Branch
	}
	return directoryLeaf(s.Directory)
}

// directoryLeaf is the last path element of a working directory — the folder a
// managed worktree was created as — or "" when the path names no such element.
// Root, empty, and relative-dot paths all answer "": a qualifier reading `/` or
// `.` would be a label that says nothing while looking like it says something.
func directoryLeaf(dir string) string {
	if dir == "" {
		return ""
	}
	switch base := filepath.Base(filepath.Clean(dir)); base {
	case ".", "..", string(filepath.Separator):
		return ""
	default:
		return base
	}
}
