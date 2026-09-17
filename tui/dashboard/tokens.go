// The small shared vocabulary the dashboard's descriptive surfaces are written
// in: the separator that joins secondary facts, the loop marker, the lock and
// mute glyphs, and the drop-priority order the context tokens are listed in.
//
// It sits in its own file because the two surfaces that speak it — the session
// inspector and the project card it swaps with — are drawn from separate files
// and neither owns the vocabulary. It outlived a third speaker, the retired
// detail strip; keeping the words out of any one surface's file is what let
// that surface go away without rewriting the two that remain.

package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/session"
)

// metaSeparator joins the secondary facts on one line. It is dimmed with the
// tokens rather than on its own so a line reads as one run of secondary text.
const metaSeparator = " · "

// loopMarkerGlyph is the daemon's own "this session is inside a loop" marker,
// used when nothing supplied a more specific supervision marker for it.
const loopMarkerGlyph = "🔁"

// sessionContextTokens is the context that tells two checkouts of one
// repository apart, in drop-priority order: profile, harness, branch,
// directory. Shared with the project card, which orders them the same way for
// the same reason.
func sessionContextTokens(s session.RunningSession) []string {
	return []string{s.EffectiveProfile(), harnessLabel(s.Harness), branchText(s), s.Directory}
}

// SessionContextLine is the one-line secondary identity of a session: profile,
// harness, branch, directory, in the drop-priority order above and joined by the
// shared separator.
//
// It is exported for a surface that draws one session outside this package and
// has to say which one — the per-row action menu, whose header carries a title
// that two live rows can share. Nothing here decides how wide the line may be:
// the caller truncates, because only it knows what it is competing with.
func SessionContextLine(s session.RunningSession) string {
	return joinDetailTokens(sessionContextTokens(s)...)
}

// joinDetailTokens joins the non-empty tokens. An omitted token leaves no
// separator behind, so a session with no harness does not read as one whose
// harness is blank.
func joinDetailTokens(tokens ...string) string {
	kept := tokens[:0:0]
	for _, t := range tokens {
		if t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, metaSeparator)
}
