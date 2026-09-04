package dashboard

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	titleColWidth = 20
	dirMaxWidth   = 40

	// rowFieldSeparatorWidth is the gap between the title column and the tail
	// columns (activity, branch, directory) that follow it.
	rowFieldSeparatorWidth = 2

	// rowSafetySlack is one cell the allocator never spends. It is load-bearing
	// in one direction only: it makes the composed row one cell NARROWER than
	// the budget, never wider, so a terminal whose emoji metrics disagree with
	// layout.Width by a cell still cannot draw past the edge. The list
	// renderer's own StyledTruncate clamp cannot cover this, because it measures
	// with the same helper that was wrong.
	rowSafetySlack = 1

	// The per-band title floors. A title is the last thing a row gives up: the
	// activity, branch, and directory columns are reduced and then dropped
	// entirely before the title is allowed below the floor for its band, because
	// a row whose title is gone no longer says which session it is.
	//
	// The floor is only breached when the pane cannot pay for the fixed
	// operational prefix (tab number, lock, mute, loop, state icon, reason) plus
	// the floor — at which point the row is already narrower than anything it
	// could usefully draw, and the renderer's clamp takes over.
	compactTitleMinWidth  = 8
	standardTitleMinWidth = 12
	spaciousTitleMinWidth = 16

	// spaciousTitleMaxWidth caps how far a spacious title grows toward its
	// content before the surplus goes to the directory instead. Past this the
	// title stops being the thing scanning benefits from.
	spaciousTitleMaxWidth = 40

	// profileNameMaxWidth caps the full profile name a spacious row shows. The
	// name is a launch identity chosen by the operator and has no bound of its
	// own; the column has to have one.
	profileNameMaxWidth = 14

	// profileInitialCellWidth is the rendered width of the standard band's
	// profile cell: "[" + one uppercased rune + "]".
	profileInitialCellWidth = 3

	// lockColumnWidth is the visual width reserved for the per-row lock
	// indicator (🔒 = 2 cells, or 2 spaces when unlocked) prepended to every
	// row so columns stay aligned regardless of which rows in the view are
	// locked.
	lockColumnWidth = 2

	// muteColumnWidth is the visual width reserved for the per-row mute
	// indicator (🔇 = 2 cells, or 2 spaces when unmuted) rendered alongside
	// the lock column so rows stay aligned regardless of which sessions in
	// the view are muted.
	muteColumnWidth = 2

	// minimumTabNumberColumnWidth fits a one-digit tab number, a colon, and
	// one trailing space (for example, "1: "). Wider result sets expand this
	// column dynamically so multi-digit numbers remain aligned.
	minimumTabNumberColumnWidth = 3

	// harnessColWidth is the visual width reserved for the harness cell.
	// Sized to fit the widest possible text — a 2-cell emoji + a 1-cell
	// space + a 7-cell label (`copilot`) — so rows stay aligned regardless of
	// which harness (or none) a session uses and regardless of the configured
	// emoji's cell width.
	harnessColWidth = 10
)

// harnessLabel returns the lowercase harness display name used in the
// badge column, or "" when the harness is unknown/empty.
func harnessLabel(h string) string {
	switch strings.ToLower(h) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "copilot":
		return "copilot"
	}
	return ""
}

// MinRowWidth is the compact band's floor: the narrowest width at which a row
// can still say which session it is. It is the fixed operational prefix — tab
// number, lock, mute, state icon and its trailing space — plus the compact
// title floor and the row's one cell of slack. Below it a row is nothing but
// indicators, so the root's split view falls back to the plain full-width list
// rather than pairing a preview with a column of glyphs.
//
// It is deliberately the *compact* floor and not the standard one. Above this
// width the row does not stop laying out; it changes band, drops the columns
// that band does not carry, and keeps drawing. So the only question left for
// this function is the one tui/model_preview.go asks it: below which width does
// the split stop being worth drawing at all.
//
// The dynamic columns (loop, activity, reason, branch, pulse, group border) are
// excluded, and that exclusion is what makes this a floor rather than a budget.
// The threshold must not move with the contents of the current view, because a
// threshold that did would make a view gain and lose its preview pane as rows
// came and went — and, worse, a resize could oscillate across it. The cost is
// bounded and paid by the allocator: between this floor and the width at which
// every reserved column fits, the allocator reduces and then drops the tail
// columns, and RenderList's own layout.StyledTruncate clamp at the render
// boundary keeps the composed line inside the viewport. Confirmed against
// tui/cross_mode_width_regression_test.go, which drives the real list renderer
// across the band boundaries and fails on any line wider than the viewport, and
// against the narrow-fit scenarios (list-preview-narrow-fit and the
// blocked-reason pair), which assert no layout issue below the floor.
func MinRowWidth() int {
	return minimumTabNumberColumnWidth + lockColumnWidth + muteColumnWidth + layout.IconCellWidth + 1 + // tab + lock + mute + icon + space
		compactTitleMinWidth + rowSafetySlack
}

// RowInput is everything one dashboard row is composed from. It is a struct
// rather than a parameter list because the row now takes two kinds of fact that
// read identically as positional strings and ints — the pre-rendered cells the
// caller measured, and the per-render-pass column widths the caller scanned —
// and a twelve-argument call could put one where the other belongs without the
// compiler noticing.
//
// Row format: tab_number | [loop] | state_icon | [reason] | profile |
// <emoji> harness | title | [activity] | [branch] | directory, with everything
// after the title omitted entirely in the compact band.
type RowInput struct {
	Session session.RunningSession

	// Band is the layout preset the whole render pass is drawn under. It is
	// passed in rather than derived from Width because a row is laid out against
	// a budget already reduced by the group border, the pulse column, and the
	// child indent, while the band is a property of the list pane those
	// reductions happen inside. Deriving it here would let two rows in one pass
	// disagree about which band they are in.
	Band Band
	// Width is this row's own budget in cells. Non-positive means unbounded, and
	// renders the historical fixed columns.
	Width int

	AcctColor    map[string]string
	HarnessColor map[string]string
	Cfg          *config.Config

	// TabNumberCell is a fixed-width, pre-rendered cell such as "1: " or " 1: ".
	// It is blank-padded when the tab number is unavailable.
	TabNumberCell string
	// ActiveLoopCell is empty when the loop column is not reserved; otherwise a
	// fixed-width cell containing either 🔁 or spaces.
	ActiveLoopCell string
	// ReasonCell is empty when no row in the current view is in the attention
	// group with a glossable blocked reason; otherwise a pre-rendered cell
	// (reasonColumnWidth cells for the whole pass, trailing space included)
	// sitting immediately after the icon, blank-padded for rows with no reason.
	ReasonCell string
	// ActivityCell is empty when no row in the current view is running a busy
	// stretch; otherwise a pre-rendered cell (activityColumnWidth cells for the
	// whole pass, trailing space included) placed before the branch column.
	ActivityCell string
	// BranchCell is empty when no row in the current view sits in a named
	// checkout; otherwise a pre-rendered cell (branchColumnWidth cells for the
	// whole pass, trailing space included) between activity and directory,
	// blank-padded for rows with no branch of their own.
	BranchCell string

	// ProfileWidth is the width of the profile *name* the spacious band's cell
	// is sized to, scanned once per render pass so every row's harness column
	// starts at the same column. Non-positive means "measure this row alone",
	// which is what a direct caller with a single row wants.
	ProfileWidth int
	// TitleWidth is the title column the pass settled on, likewise scanned once
	// so the tail columns align. Non-positive falls back to the band's default.
	TitleWidth int
}

// rowAlloc is the width every variable field of one row was granted. Zero means
// the field is not drawn at all: either its band does not carry it, or the pane
// could not pay for it. Every row in a render pass gets the same allocation,
// because every input the allocator reads is a per-pass constant — which is what
// keeps the columns aligned without any row knowing about the others.
type rowAlloc struct {
	profile  int // rendered width of the profile cell, brackets included
	harness  int // harnessColWidth, or 0 when dropped
	title    int
	sep      int // separator cells between the title and the tail columns
	activity int
	branch   int
	dir      int
}

// allocateRow spends the row's width on its fields in priority order.
//
// The order is the policy, and it is the same one the session's brief states:
// protect the operational prefix and a useful title first, then identity
// (profile, harness), then the tail columns, then grow the title toward its
// content, and only then give whatever is left to the directory. Reading the
// steps top to bottom reads the priority list; nothing else in the row decides
// what to drop.
//
// Each optional field is taken whole or not at all, so a field never appears as
// a stub too narrow to read. The title is the one exception: it takes what it
// can when even its floor does not fit, because a row with a cut title is still
// a row and a row with none is not.
func allocateRow(in RowInput, prefixWidth, profileCellWidth int) rowAlloc {
	var a rowAlloc

	titleWant := in.TitleWidth
	if titleWant <= 0 {
		titleWant = defaultTitleWidth(in.Band)
	}
	activityWidth := layout.Width(in.ActivityCell)
	branchWidth := layout.StyledWidth(in.BranchCell)

	if in.Width <= 0 {
		// Unbounded: the historical fixed columns, which is what a caller with no
		// viewport (a test, or a pre-WindowSizeMsg first paint) means to ask for.
		a.title = titleWant
		if in.Band != BandCompact {
			a.profile = profileCellWidth
			a.harness = harnessColWidth
			a.sep = rowFieldSeparatorWidth
			a.activity = activityWidth
			a.branch = branchWidth
			a.dir = dirMaxWidth
		}
		return a
	}

	rem := in.Width - prefixWidth - rowSafetySlack
	if rem < 0 {
		rem = 0
	}

	// 1. The title floor, taken partially when that is all there is.
	a.title = min(titleFloor(in.Band), rem)
	rem -= a.title

	if in.Band == BandCompact {
		// Compact carries no other field, so the title is flexible rather than a
		// column: it takes the whole remaining budget and is not padded.
		a.title += rem
		return a
	}

	take := func(want int) int {
		if want <= 0 || want > rem {
			return 0
		}
		rem -= want
		return want
	}

	// 2. Identity: the profile cell, then the harness cell, each with the single
	//    space that separates it from what follows.
	if got := take(profileCellWidth + 1); got > 0 {
		a.profile = profileCellWidth
	}
	if got := take(harnessColWidth + 1); got > 0 {
		a.harness = harnessColWidth
	}

	// 3. The tail, which only exists if the separator plus at least one cell of
	//    content fits.
	if rem > rowFieldSeparatorWidth {
		a.sep = take(rowFieldSeparatorWidth)
	}
	if a.sep > 0 {
		a.activity = take(activityWidth)
		a.branch = take(branchWidth)

		// 4. Grow the title toward the pass's content width before the directory
		//    gets anything, so a spacious pane spends its surplus on the field
		//    that identifies the row rather than on a longer path.
		if grow := min(titleWant-a.title, rem); grow > 0 {
			a.title += grow
			rem -= grow
		}

		// 5. Whatever is left is the directory's, left-truncated so the tail of
		//    the path — the half that names the checkout — survives.
		a.dir = rem
		rem = 0
	}
	if a.activity == 0 && a.branch == 0 && a.dir == 0 {
		// Nothing followed the title after all; give the separator back rather
		// than pad the row out with trailing blanks.
		rem += a.sep
		a.sep = 0
		if grow := min(titleWant-a.title, rem); grow > 0 {
			a.title += grow
		}
	}
	return a
}

// titleFloor is the narrowest title a band will draw before it starts dropping
// other fields instead.
func titleFloor(b Band) int {
	switch b {
	case BandCompact:
		return compactTitleMinWidth
	case BandSpacious:
		return spaciousTitleMinWidth
	default:
		return standardTitleMinWidth
	}
}

// defaultTitleWidth is the title column a caller that did not scan the pass
// gets. Compact has no column at all — its title is whatever is left — so the
// floor stands in as the value the unbounded branch grows from.
func defaultTitleWidth(b Band) int {
	if b == BandCompact {
		return compactTitleMinWidth
	}
	return titleColWidth
}

// FormatRow formats a single session row under the band its render pass is in.
//
// Two properties hold across every band. The composed row is at most Width-1
// cells wide, never more; and every field is measured in display cells through
// tui/layout, so an ANSI-styled cell is never counted by its bytes and a
// grapheme cluster is never cut in half.
func FormatRow(in RowInput) string {
	s := in.Session

	// Lock indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is locked.
	lockCol := "  "
	if s.Locked {
		lockCol = layout.PadRight(palette.LockIcon(), lockColumnWidth)
	}

	// Mute indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is muted.
	muteCol := "  "
	if s.Muted {
		muteCol = layout.PadRight(palette.MuteIcon(), muteColumnWidth)
	}

	// State icon at the start for instant scanning. layout.PadIconCell pads
	// the empty-state placeholder to the same 2-cell column reserved for
	// glyph rows.
	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State)
	}
	iconCol := layout.PadIconCell(icon)

	// The operational prefix: what the row says about the session's state
	// regardless of how narrow the pane is. It is charged before anything else
	// and is never reduced, which is the one case where a row may be drawn
	// without a readable title.
	//
	// This enumeration is hand-maintained and MUST be walked against the
	// composition below every time a cell is added, moved, or removed; nothing
	// checks the two against each other but
	// TestFormatRowWidthArithmeticMatchesTheComposedRow, which is why that test
	// exists. Term by term, in the order the row concatenates them: tab number
	// (measured) + lock (2) + mute (2) + optional loop (measured) + icon (2) +
	// its trailing space (1) + the optional reason cell, whose width is set per
	// render pass.
	//
	// The reason cell is measured with StyledWidth because renderReasonCell
	// colors its tag and layout.Width would count its ANSI bytes as visible
	// cells; the same applies to the branch cell in the allocator. The tab
	// number, loop, and activity cells carry no styling of their own at this
	// point — the activity cell's styling wraps it only after the arithmetic has
	// read it — so plain Width is correct for them.
	prefix := in.TabNumberCell + lockCol + muteCol + in.ActiveLoopCell + iconCol + " " + in.ReasonCell
	prefixWidth := layout.Width(in.TabNumberCell) + lockColumnWidth + muteColumnWidth +
		layout.Width(in.ActiveLoopCell) + layout.IconCellWidth + 1 + layout.StyledWidth(in.ReasonCell)

	// Project-identity color tracks the project's configured default profile so
	// override-launched rows keep the project's visual identity on the title and
	// profile cell. The profile text and harness label still reflect the running
	// profile/harness. Falls back to the running account color when the project
	// is not configured (orphan tab).
	identityColor := ProjectIdentityColor(s, in.AcctColor, in.Cfg)
	style := accountStyle(identityColor)

	profileName, profileCellWidth := profileCellFor(in)
	a := allocateRow(in, prefixWidth, profileCellWidth)

	// Title: project title, with a leading pictographic grapheme padded to the
	// fixed 2-cell title-icon column so emoji-prefixed titles align with each
	// other regardless of whether the source glyph measures as 1 or 2 cells.
	// Truncation is grapheme-atomic.
	titleText := layout.Truncate(layout.RenderTitleIcon(SessionDisplayTitle(s)), a.title, "…")

	if in.Band == BandCompact {
		// Compact: the prefix and one flexible, unpadded title. Profile, harness,
		// activity, branch, and directory are dropped by the band rather than by
		// the arithmetic, and the selected-item detail strip carries them instead.
		return prefix + style.Render(titleText)
	}

	row := prefix
	if a.profile > 0 {
		row += style.Render("["+layout.PadRight(profileName, a.profile-2)+"]") + " "
	}
	if a.harness > 0 {
		row += renderHarnessCell(s, in.HarnessColor, in.Cfg) + " "
	}
	row += style.Render(layout.PadRight(titleText, a.title))
	if a.sep > 0 {
		row += strings.Repeat(" ", a.sep)
	}
	if a.activity > 0 {
		row += in.ActivityCell
	}
	if a.branch > 0 {
		row += in.BranchCell
	}
	if a.dir > 0 {
		row += styleDim.Render(layout.TruncateLeft(s.Directory, a.dir, "…"))
	}
	return row
}

// profileCellFor returns the profile text the row's band shows and the width of
// the cell that holds it, brackets included.
//
// Standard shows the initial: one uppercased rune, taken as a rune rather than a
// byte so a multibyte profile name produces a valid UTF-8 badge instead of a
// truncated invalid one. Spacious shows the whole name, bounded by the column
// the pass scanned. Compact shows nothing.
func profileCellFor(in RowInput) (string, int) {
	name := in.Session.EffectiveProfile()
	switch in.Band {
	case BandCompact:
		return "", 0
	case BandSpacious:
		width := in.ProfileWidth
		if width <= 0 {
			width = layout.Width(name)
		}
		if width > profileNameMaxWidth {
			width = profileNameMaxWidth
		}
		if width < 1 {
			width = 1
		}
		return layout.Truncate(name, width, "…"), width + 2
	default:
		initial := " "
		if r, sz := utf8.DecodeRuneInString(name); sz > 0 && r != utf8.RuneError {
			initial = strings.ToUpper(string(r))
		}
		return initial, profileInitialCellWidth
	}
}

// renderHarnessCell paints the fixed-width harness cell. Its color is keyed by
// harness, not by profile, so the column is stable across profile overrides.
// cfg supplies the [harness_emojis] lookup so the cell can render as
// "<emoji> <label>"; when the configured emoji is empty the cell collapses to
// just "<label>" so unconfigured users don't see a stray leading space.
func renderHarnessCell(s session.RunningSession, harnessColor map[string]string, cfg *config.Config) string {
	label := harnessLabel(s.Harness)
	if label == "" {
		return strings.Repeat(" ", harnessColWidth)
	}
	emoji := ""
	if cfg != nil {
		emoji = cfg.HarnessEmoji(s.Harness)
	}
	text := label
	if emoji != "" {
		text = emoji + " " + label
	}
	pad := harnessColWidth - layout.Width(text)
	if pad < 0 {
		pad = 0
	}
	return accountStyle(harnessColor[strings.ToLower(s.Harness)]).Render(text) + strings.Repeat(" ", pad)
}

// profileColumnWidth is the width of the profile name column for one render
// pass: the widest name any row carries, bounded by profileNameMaxWidth. Like
// the tab-number, activity, reason, and branch scans, it runs once per pass so
// every row's following column starts at the same cell.
func profileColumnWidth(rows []session.RunningSession) int {
	widest := 1
	for _, s := range rows {
		if w := layout.Width(s.EffectiveProfile()); w > widest {
			widest = w
		}
	}
	if widest > profileNameMaxWidth {
		widest = profileNameMaxWidth
	}
	return widest
}

// titleColumnWidth is the title column for one render pass.
//
// Standard keeps the historical fixed column: at that width the surplus a
// content-sized title would claim is the directory's only source of room.
// Spacious grows the column to the widest title on screen — never below the
// standard column, never past spaciousTitleMaxWidth — which is what "expand
// titles to content" means when the rows still have to line up with each other.
// Compact has no column, so it reports none.
func titleColumnWidth(rows []session.RunningSession, band Band) int {
	switch band {
	case BandCompact:
		return 0
	case BandSpacious:
		widest := titleColWidth
		for _, s := range rows {
			if w := layout.Width(layout.RenderTitleIcon(SessionDisplayTitle(s))); w > widest {
				widest = w
			}
		}
		if widest > spaciousTitleMaxWidth {
			widest = spaciousTitleMaxWidth
		}
		return widest
	default:
		return titleColWidth
	}
}

// tabNumberColumnWidth returns a stable width for every row in the current
// filtered result set: the widest decimal tab number plus ':' and one trailing
// space. One digit is reserved even when all rows lack a known number.
func tabNumberColumnWidth(sessions []session.RunningSession) int {
	digits := 1
	for _, s := range sessions {
		if s.TabNumber <= 0 {
			continue
		}
		if n := len(strconv.Itoa(s.TabNumber)); n > digits {
			digits = n
		}
	}
	return digits + 2
}

// renderTabNumberCell right-aligns a known tab number within the dashboard's
// shared number column. Unknown numbers render as spaces to preserve alignment.
func renderTabNumberCell(tabNumber, width int) string {
	if width < minimumTabNumberColumnWidth {
		width = minimumTabNumberColumnWidth
	}
	if tabNumber <= 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d: ", width-2, tabNumber)
}
