// The queue's answer to the fields it dropped from every row.
//
// A queue row is Session / Status / Time at every width, so the facts that tell
// two checkouts of one repository apart — profile, harness, branch, directory,
// how long the current turn has been running, who is supervising it, whether it
// is locked or muted — are on no row at all. They are still the facts an operator
// narrows in on, so they live in a fixed strip under the list that describes the
// SELECTED item only.
//
// This is the interim inspector. It is deliberately the same shape as the one
// the redesign replaces it with — one selection, its whole context, nothing about
// the rows around it — so what moves later is the presentation and not the
// question. Until then it is the only place this information exists, which is why
// it is reserved at every width rather than only where a row ran out of room.
//
// Two lines, always, whether or not there is anything to put in them. A strip
// that grew and shrank with its content would move the list's bottom edge on
// every cursor move, and the height budget would have to be recomputed per
// selection to keep the cursor inside the viewport. A constant cost is worth
// more than a blank line saved.

package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// DetailStripLines is the strip's fixed height. Every budget that charges for
// the strip charges exactly this, and RenderDetailStrip returns exactly this
// many lines.
const DetailStripLines = 2

// DetailStripInput is every fact the strip reads. Like the list renderer it
// takes the whole projection plus the selected item, so a family's counts and
// members come from the same ItemList the row above it was drawn from.
type DetailStripInput struct {
	Width int

	Items ItemList
	Item  Item
	// Selected reports whether Item is meaningful at all. An empty list has no
	// selection, and the strip then renders as two blank lines rather than as a
	// description of the zero value.
	Selected bool

	Cfg           *config.Config
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	Watchdog      map[int]protocol.WatchdogState
	Now           time.Time

	// LoopMarker returns the supervision marker bound to a session, when one is
	// positively identified. A guessed association would put a wrong state next to
	// a session, so a session this cannot identify gets no marker at all. Nil means
	// nothing is supervised.
	//
	// It reads a marker here rather than on the row because supervision is
	// selected-session context: the queue row answers what the session is doing,
	// and who is driving it is one of the facts the redesign moves to the
	// inspector. This is the seam the inspector inherits.
	LoopMarker func(s session.RunningSession) (string, bool)
}

// marker is the nil-safe form of the LoopMarker seam.
func (in DetailStripInput) marker(s session.RunningSession) (string, bool) {
	if in.LoopMarker == nil {
		return "", false
	}
	return in.LoopMarker(s)
}

// RenderDetailStrip returns exactly DetailStripLines lines, each clamped to
// Width. Empty tokens are omitted rather than rendered as gaps, so a session
// with no branch and no timer does not read as one whose branch is blank.
func RenderDetailStrip(in DetailStripInput) []string {
	lines := make([]string, DetailStripLines)
	if in.Width > 0 && in.Selected {
		var first, second string
		if s, ok := in.Items.Session(in.Item); ok {
			first, second = sessionDetailLines(in, s)
		} else if in.Item.Kind == ItemFamilySummary {
			first, second = familyDetailLines(in)
		}
		lines[0] = layout.StyledTruncate(first, in.Width, "…")
		lines[1] = layout.StyledTruncate(second, in.Width, "…")
	}
	return lines
}

// sessionDetailLines splits a session's context across the strip's two lines:
// what the session IS on the first, where it is on the second.
func sessionDetailLines(in DetailStripInput, s session.RunningSession) (string, string) {
	// The supervision marker leads the line, attached to the title rather than
	// joined as a token of its own: it qualifies which session this is (one an
	// agent-session-loop run is driving), and a separated `🔁 · api` would read as
	// a fact about the project instead.
	title := SessionDisplayTitle(s)
	if marker, ok := in.marker(s); ok && marker != "" {
		title = marker + " " + title
	} else if s.Looping {
		title = loopMarkerGlyph + " " + title
	}

	identity := []string{title, s.EffectiveProfile(), harnessLabel(s.Harness)}
	if text, _ := activityCellText(s, in.Watchdog[s.WindowID], in.Now); text != "" && !s.BusySince.IsZero() {
		identity = append(identity, text)
	}
	// Lock and mute travel as one token rather than two. They are the row's
	// retired indicator columns, and they were adjacent glyphs there for the same
	// reason: each is a single bit about how the session may be acted on, and two
	// separators between two glyphs would spend more of the line on punctuation
	// than on facts.
	if flags := sessionFlagGlyphs(s); flags != "" {
		identity = append(identity, flags)
	}
	first := accountStyle(ProjectIdentityColor(s, ProfileColorMap(in.Cfg), in.Cfg)).
		Render(joinDetailTokens(identity...))

	// The directory is the widest token and the last one, so it takes whatever
	// the branch left and loses its shared prefix rather than the tail that names
	// the checkout — the same priority the row's directory column keeps.
	//
	// The emptiness tests are on the plain tokens, not on the rendered strings: a
	// style applied to an empty token still returns escape sequences, and a
	// second line that asked a rendered string whether it was empty would lead
	// with a separator for a branch that is not there.
	branch := branchText(s)
	second := ""
	room := in.Width
	if branch != "" {
		second = styleDim.Render(branch)
		room -= layout.Width(branch)
	}
	if s.Directory != "" {
		if second != "" {
			room -= layout.Width(metaSeparator)
			second += styleDim.Render(metaSeparator)
		}
		if room > 1 {
			second += styleDim.Render(layout.TruncateLeft(s.Directory, room, "…"))
		}
	}
	return first, second
}

// familyDetailLines describes a family summary: what it is and how many
// sessions are behind it, then the context of the member that decided its
// urgency — which is the member the operator is being asked to look at.
func familyDetailLines(in DetailStripInput) (string, string) {
	label := in.Item.FamilyLabel
	if label == "" {
		label = unnamedFamilyLabel
	}
	group := idleGroup
	lead, hasLead := in.Items.SummaryLead(in.Item, in.LastActivity, in.IdleThreshold)
	if hasLead {
		group = StateGroup(lead, in.LastActivity, in.IdleThreshold)
	}
	first := groupLabelStyle(group).Render(label)
	if tally := renderSummaryCounts(in.Items.SummaryCounts(in.Item, in.LastActivity, in.IdleThreshold)); tally != "" {
		first += styleDim.Render(metaSeparator) + tally
	}

	if !hasLead {
		return first, ""
	}
	second := styleDim.Render(joinDetailTokens(append(
		[]string{SessionDisplayTitle(lead)}, sessionContextTokens(lead)...)...))
	return first, second
}
