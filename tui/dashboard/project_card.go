// This file paints what the preview pane shows when the selection is a family
// summary rather than a session: a project card.
//
// It exists because the pane's usual content has no meaning here. Scrollback is
// one window's, and a summary owns no window — the pane would have to pick a
// member and show its terminal, which is exactly the "resolve a summary to some
// arbitrary session" the item layer refuses everywhere else. The card answers
// the question the selection actually asked instead: what is in this family.
//
// Every fact on it was already stamped on the rows at discovery time, so drawing
// it costs no Kitty call, no Git probe, and no config lookup beyond the colors —
// which is the second reason it is a card and not a terminal read.

package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ProjectCardInput is everything the card is drawn from. It takes the whole
// projection plus the selected item rather than a pre-extracted member list so
// the card counts and orders its children through the same SummaryCounts and
// Members the summary row does — a card that disagreed with the line above it
// about how many sessions a family has would be worse than no card.
type ProjectCardInput struct {
	Width, Height int
	Items         ItemList
	Item          Item
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	Cfg           *config.Config
}

// projectCardOverflowReserve is the line the card gives back to "+N more" when
// its members outnumber the space. One line, always charged before the members
// are laid out, so the count it reports is never itself the thing that got cut.
const projectCardOverflowReserve = 1

// RenderProjectCard renders exactly Height lines (no trailing newline), the same
// contract RenderPreview keeps, so the split view can swap one for the other
// without re-measuring the pane.
//
// The card is bounded by the pane rather than by the family: a family with forty
// checkouts fills the height it was given and says how many members it did not
// draw. It never scrolls and never grows, because the pane's size is the root's
// decision and a card that overflowed it would push the list off the screen.
func RenderProjectCard(in ProjectCardInput) string {
	if in.Width <= 0 || in.Height <= 0 {
		return ""
	}

	lines := make([]string, 0, in.Height)

	label := in.Item.FamilyLabel
	if label == "" {
		label = unnamedFamilyLabel
	}
	group := idleGroup
	if lead, ok := in.Items.SummaryLead(in.Item, in.LastActivity, in.IdleThreshold); ok {
		group = StateGroup(lead, in.LastActivity, in.IdleThreshold)
	}
	// The heading wears the family's worst-child group color and its collapse
	// chevron, so the card reads as the same object as the row that raised it.
	chevron := summaryChevronExpanded
	if in.Item.Collapsed {
		chevron = summaryChevronCollapsed
	}
	lines = append(lines, layout.StyledTruncate(groupLabelStyle(group).Render(chevron+" "+label), in.Width, "…"))

	if len(lines) < in.Height {
		counts := in.Items.SummaryCounts(in.Item, in.LastActivity, in.IdleThreshold)
		tally := renderSummaryCounts(counts)
		if tally == "" {
			tally = styleDim.Render("(no visible sessions)")
		}
		lines = append(lines, layout.StyledTruncate(tally, in.Width, "…"))
	}
	if len(lines) < in.Height {
		lines = append(lines, "")
	}

	// Members in projection order, which is the order the expanded tree draws
	// them in, so the card is a reading of the same list rather than a second
	// opinion about it.
	rows := make([]int, 0, len(in.Item.Members))
	for _, row := range in.Item.Members {
		if row >= 0 && row < len(in.Items.Rows) {
			rows = append(rows, row)
		}
	}

	budget := in.Height - len(lines)
	shown := len(rows)
	if shown > budget {
		shown = budget - projectCardOverflowReserve
		if shown < 0 {
			shown = 0
		}
	}
	for _, row := range rows[:shown] {
		lines = append(lines, projectCardChildLine(in, in.Items.Rows[row]))
	}
	if hidden := len(rows) - shown; hidden > 0 && len(lines) < in.Height {
		lines = append(lines, styleDim.Render(layout.Truncate(fmt.Sprintf("+%d more", hidden), in.Width, "…")))
	}

	for len(lines) < in.Height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:in.Height], "\n")
}

// projectCardChildLine renders one member: its state icon and title, then the
// context that tells two checkouts of one repository apart — profile, harness,
// branch, directory.
//
// The identity half is truncated first and styled after, and the context half is
// laid out in whatever the identity left. That ordering is the priority: a
// narrow pane loses the directory before it loses the branch, and loses the
// branch before it loses the title, because the last thing worth keeping about a
// row inside a family is which row it is.
func projectCardChildLine(in ProjectCardInput, s session.RunningSession) string {
	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State)
	}
	head := layout.Truncate(layout.PadIconCell(icon)+" "+SessionDisplayTitle(s), in.Width, "…")
	line := accountStyle(ProjectIdentityColor(s, ProfileColorMap(in.Cfg), in.Cfg)).Render(head)

	// Two cells for the gap, one for an ellipsis, and at least one for content.
	remaining := in.Width - layout.Width(head) - 2
	if remaining < 2 {
		return line
	}

	// Profile, harness, branch, directory — the same tokens in the same
	// drop-priority order the compact band's detail strip uses, from the one
	// helper, so the two surfaces cannot start describing a child differently.
	parts := joinDetailTokens(sessionContextTokens(s)...)
	if parts == "" {
		return line
	}
	// The directory is the last part and the widest, so cutting from the right
	// takes the tail of the path — which is the half that names the checkout.
	// TruncateLeft keeps it and drops the shared prefix instead, exactly as the
	// list row's directory column does.
	return line + "  " + styleDim.Render(layout.TruncateLeft(parts, remaining, "…"))
}
