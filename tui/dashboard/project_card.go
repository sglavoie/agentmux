// This file paints what the inspector shows when the selection is a family
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
//
// It wears the inspector's GEOMETRY and keeps its own SEMANTICS. Six metadata
// lines, the same label column on the same cells, one divider, then a body: the
// pane does not jump when the cursor crosses from a family into one of its
// children. What changes is every value above the divider, because every one of
// them is an aggregate. `Agent` is one harness only when all the members agree
// and a count when they do not; `Branch` is one branch only when the family is
// checked out once; `Watch` is how MANY members are supervised, locked, or
// muted rather than whether this one is. A card that borrowed a member's answer
// for any of those would be the summary-to-arbitrary-session resolution again,
// wearing a different name. The divider never says Paused: there is no snapshot
// to hold because there was never a read.

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

	// LoopMarker is the same supervision seam the session inspector reads, asked
	// once per member. The card reports a COUNT rather than a marker, but it has
	// to ask the same question to get one: a run the daemon's own loop bit has
	// not caught up with is still a supervised member, and a family that
	// undercounted them would say nothing is holding a family that something is.
	// Nil means nothing is supervised.
	LoopMarker func(s session.RunningSession) (string, bool)
}

// marker is the nil-safe form of the LoopMarker seam.
func (in ProjectCardInput) marker(s session.RunningSession) (string, bool) {
	if in.LoopMarker == nil {
		return "", false
	}
	return in.LoopMarker(s)
}

// projectCardOverflowReserve is the line the card gives back to "+N more" when
// its members outnumber the space. One line, always charged before the members
// are laid out, so the count it reports is never itself the thing that got cut.
const projectCardOverflowReserve = 1

// RenderProjectCard renders exactly Height lines (no trailing newline), the same
// contract RenderInspector keeps, so the split view can swap one for the other
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

	lines := projectCardMetaLines(in)
	// The divider is only drawn when a member can be drawn under it. A rule with
	// nothing below it would spend the last line the card had left on the
	// announcement of a body it is not going to show.
	if in.Height <= len(lines)+inspectorDividerLines {
		return strings.Join(projectCardTruncated(in, lines), "\n")
	}
	// The rule is drawn through the inspector's own divider so the two panes
	// share one answer about which cell it starts on. A card is never paused, so
	// the zero viewport is passed rather than a flag.
	lines = append(lines, inspectorDivider(InspectorInput{Width: in.Width}))
	lines = append(lines, projectCardMemberLines(in, in.Height-len(lines))...)
	return strings.Join(lines[:in.Height], "\n")
}

// projectCardTruncated cuts the metadata block to a pane too short to hold it,
// spending the last surviving line on what the card could not draw.
//
// A card that named a family and then showed none of it would be the one thing
// the height bound must never produce: the pane would look like a family with no
// sessions, which is a claim the projection can also make truthfully. So the
// overflow notice outranks the last metadata line it displaces — a count of the
// members is what the reader needs before any aggregate about them.
//
// A single-line pane keeps the family's name instead. There is nothing left to
// trade: `+3 more` more than what, on a card that never said which family.
func projectCardTruncated(in ProjectCardInput, lines []string) []string {
	out := make([]string, in.Height)
	copy(out, lines)
	hidden := len(projectCardMembers(in))
	if hidden == 0 || in.Height < 2 {
		return out
	}
	out[len(out)-1] = styleDim.Render(layout.Truncate(fmt.Sprintf("+%d more", hidden), in.Width, "…"))
	return out
}

// projectCardMetaLines builds the card's fixed projectCardMetadataLines block: what
// this family is, how its sessions are distributed, and then the four labelled
// facts read across every member instead of off one.
func projectCardMetaLines(in ProjectCardInput) []string {
	lines := make([]string, projectCardMetadataLines)
	members := projectCardMembers(in)

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
	lines[0] = layout.StyledTruncate(groupLabelStyle(group).Render(chevron+" "+label), in.Width, "…")

	tally := renderSummaryCounts(in.Items.SummaryCounts(in.Item, in.LastActivity, in.IdleThreshold))
	if tally == "" {
		tally = styleDim.Render("(no visible sessions)")
	}
	lines[1] = layout.StyledTruncate(tally, in.Width, "…")

	lines[2] = inspectorLabelled(in.Width, "Agent", joinDetailTokens(
		aggregateToken(memberValues(members, func(s session.RunningSession) string {
			return harnessLabel(s.Harness)
		}), "harnesses"),
		aggregateToken(memberValues(members, func(s session.RunningSession) string {
			return s.EffectiveProfile()
		}), "profiles"),
	))
	lines[3] = inspectorLabelled(in.Width, "Branch", projectCardBranchText(members))
	lines[4] = inspectorDirectoryLine(in.Width, projectCardRoot(members))
	lines[5] = inspectorLabelled(in.Width, "Watch", projectCardWatchText(in, members))
	return lines
}

// projectCardMembers resolves the summary's members to sessions, in projection
// order — the order the expanded tree draws them in, so the card is a reading of
// the same list rather than a second opinion about it.
//
// Every index is bounds-checked rather than trusted, matching GroupAt,
// SummaryCounts, and HasUnreadNotice: a summary is built beside the row set it
// points into, but the two travel as separate fields and a caller may pair them
// wrongly.
func projectCardMembers(in ProjectCardInput) []session.RunningSession {
	members := make([]session.RunningSession, 0, len(in.Item.Members))
	for _, row := range in.Item.Members {
		if row >= 0 && row < len(in.Items.Rows) {
			members = append(members, in.Items.Rows[row])
		}
	}
	return members
}

// memberValues projects one fact off every member, dropping the empties. The
// caller's function is what makes this one helper serve harnesses, profiles, and
// branches without three near-identical loops that could drift apart.
func memberValues(members []session.RunningSession, of func(session.RunningSession) string) []string {
	values := make([]string, 0, len(members))
	for _, s := range members {
		if v := of(s); v != "" {
			values = append(values, v)
		}
	}
	return values
}

// aggregateToken is how a family answers a question a session answers with one
// value: the value itself when every member agrees, and how many distinct
// answers there are when they do not.
//
// Naming the count rather than listing the values is the aggregate rule said in
// one line. `2 profiles` is true of the family; `work` would be true of one
// member and silently wrong about the other, which is the failure mode a card
// over a summary exists to avoid.
func aggregateToken(values []string, plural string) string {
	seen := make(map[string]struct{}, len(values))
	distinct := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		distinct = append(distinct, v)
	}
	switch len(distinct) {
	case 0:
		return ""
	case 1:
		return distinct[0]
	default:
		return fmt.Sprintf("%d %s", len(distinct), plural)
	}
}

// projectCardBranchText is the family's checkout answer.
//
// The dirty marker is family-wide on purpose. A family checked out once carries
// its branch and a `*` when ANY member's working tree is modified — the members
// share that tree, so the bit is a fact about the family rather than about the
// session that happened to be asked. A family spanning several checkouts reports
// the count instead and drops the marker: `2 branches*` would attach one tree's
// state to a name that no longer refers to one tree.
func projectCardBranchText(members []session.RunningSession) string {
	branches := aggregateToken(memberValues(members, func(s session.RunningSession) string {
		return s.Branch
	}), "branches")
	if branches == "" || strings.HasSuffix(branches, "branches") {
		return branches
	}
	for _, s := range members {
		if s.Branch == branches && s.Dirty {
			return branches + dirtyMarker
		}
	}
	return branches
}

// projectCardRoot is the family's own directory: the main checkout root a
// repository family resolved at discovery, or the configured directory for a
// non-repository. It is the family's fact rather than a member's, which is why
// it is read off FamilyPrimaryRoot and not off the first member's Directory —
// the two differ for exactly the case the card exists to describe, a repository
// whose members sit in separate worktrees.
func projectCardRoot(members []session.RunningSession) string {
	for _, s := range members {
		if s.FamilyPrimaryRoot != "" {
			return s.FamilyPrimaryRoot
		}
	}
	return ""
}

// projectCardWatchText counts what the session inspector's watch line states:
// how many members an agent-session-loop run is holding, how many are locked,
// how many are muted. Zero of something contributes nothing rather than a `0`,
// so the line names only what is actually true of the family.
//
// There is no watchdog age here, and that is the one fact from the session line
// with no aggregate form. Silence is per window; the oldest member's would read
// as the family's, and the average of several would be a number describing
// nothing.
func projectCardWatchText(in ProjectCardInput, members []session.RunningSession) string {
	var supervised, locked, muted int
	for _, s := range members {
		if marker, ok := in.marker(s); (ok && marker != "") || s.Looping {
			supervised++
		}
		if s.Locked {
			locked++
		}
		if s.Muted {
			muted++
		}
	}
	var tokens []string
	if supervised > 0 {
		tokens = append(tokens, fmt.Sprintf("%s %d", loopMarkerGlyph, supervised))
	}
	if locked > 0 {
		tokens = append(tokens, fmt.Sprintf("%s %d", palette.LockIcon(), locked))
	}
	if muted > 0 {
		tokens = append(tokens, fmt.Sprintf("%s %d", palette.MuteIcon(), muted))
	}
	return joinDetailTokens(tokens...)
}

// projectCardMemberLines fills the card's body — the region the session
// inspector spends on terminal output — with the family's members, and says how
// many it could not draw.
//
// It returns exactly height lines, padded, which is what keeps the card's total
// equal to the pane it was given.
func projectCardMemberLines(in ProjectCardInput, height int) []string {
	lines := make([]string, height)
	if height <= 0 {
		return nil
	}
	members := projectCardMembers(in)

	shown := len(members)
	if shown > height {
		shown = height - projectCardOverflowReserve
		if shown < 0 {
			shown = 0
		}
	}
	for i, s := range members[:shown] {
		lines[i] = projectCardChildLine(in, s)
	}
	if hidden := len(members) - shown; hidden > 0 && shown < height {
		lines[shown] = styleDim.Render(layout.Truncate(fmt.Sprintf("+%d more", hidden), in.Width, "…"))
	}
	return lines
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
	// drop-priority order the session inspector lists them in, from the one
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
