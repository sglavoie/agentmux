// The compact band's answer to the fields it dropped from every row.
//
// A compact row carries the operational prefix and a title and nothing else, so
// the facts that tell two checkouts of one repository apart — profile, harness,
// branch, directory, how long the current turn has been running — are not on
// screen at all. They are still the facts an operator narrows the window to look
// at, so they move to a fixed strip under the list that describes the SELECTED
// item only.
//
// Two lines, always, whether or not there is anything to put in them. A strip
// that grew and shrank with its content would move the list's bottom edge on
// every cursor move, and the height budget would have to be recomputed per
// selection to keep the cursor inside the viewport. A constant cost is worth
// more than a blank line saved.

package dashboard

import (
	"strings"
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

// detailStripSeparator joins the tokens on a strip line. It is dimmed with the
// tokens rather than on its own so a line reads as one run of secondary text.
const detailStripSeparator = " · "

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
	identity := []string{SessionDisplayTitle(s), s.EffectiveProfile(), harnessLabel(s.Harness)}
	if text, _ := activityCellText(s, in.Watchdog[s.WindowID], in.Now); text != "" && !s.BusySince.IsZero() {
		identity = append(identity, text)
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
			room -= layout.Width(detailStripSeparator)
			second += styleDim.Render(detailStripSeparator)
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
		first += styleDim.Render(detailStripSeparator) + tally
	}

	if !hasLead {
		return first, ""
	}
	second := styleDim.Render(joinDetailTokens(append(
		[]string{SessionDisplayTitle(lead)}, sessionContextTokens(lead)...)...))
	return first, second
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
	return strings.Join(kept, detailStripSeparator)
}
