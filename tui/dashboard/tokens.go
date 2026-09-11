// The small shared vocabulary the dashboard's descriptive surfaces are written
// in: the separator that joins secondary facts, the loop marker, the lock and
// mute glyphs, and the drop-priority order the context tokens are listed in.
//
// It sits in its own file because three surfaces now speak it — the session
// inspector, the project card it swaps with, and the detail strip the inspector
// is replacing — and the strip is the one of the three that is going away. A
// vocabulary that lived in the file being retired would make retiring it a
// rewrite of the two surfaces that outlive it, which is exactly the kind of
// coupling that gets a replacement half-finished.

package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// metaSeparator joins the secondary facts on one line. It is dimmed with the
// tokens rather than on its own so a line reads as one run of secondary text.
const metaSeparator = " · "

// loopMarkerGlyph is the daemon's own "this session is inside a loop" marker,
// used when nothing supplied a more specific supervision marker for it.
const loopMarkerGlyph = "🔁"

// sessionFlagGlyphs renders the session's lock and mute bits, or "" when neither
// is set. These are indicators, not labels: an unset bit contributes nothing
// rather than a placeholder, because the surfaces that draw them describe one
// selected session and have no column to keep aligned with anything.
func sessionFlagGlyphs(s session.RunningSession) string {
	var glyphs []string
	if s.Locked {
		glyphs = append(glyphs, palette.LockIcon())
	}
	if s.Muted {
		glyphs = append(glyphs, palette.MuteIcon())
	}
	return strings.Join(glyphs, " ")
}

// sessionContextTokens is the context that tells two checkouts of one
// repository apart, in drop-priority order: profile, harness, branch,
// directory. Shared with the project card, which orders them the same way for
// the same reason.
func sessionContextTokens(s session.RunningSession) []string {
	return []string{s.EffectiveProfile(), harnessLabel(s.Harness), branchText(s), s.Directory}
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
