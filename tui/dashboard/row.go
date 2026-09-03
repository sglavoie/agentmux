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

// MinRowWidth is the narrowest width at which FormatRow can lay out its
// fixed columns before the directory column: tab number + lock + mute + state
// icon + a space + the "[X]" account badge + a space + the harness cell + a
// space + the title column + the two separator spaces that precede the
// directory. The dynamic columns (loop, activity, reason, branch, pulse, group
// border) are excluded because they are only reserved when a row needs them.
// Below this
// floor the row can no longer shrink, so the list renderer clamps it and the
// root's split view falls back to the plain full-width list.
//
// "Excluded" is a floor, not a budget, and that is why a new dynamic column
// leaves this function alone. What MinRowWidth answers is "below which width
// does the split view stop being worth drawing at all" — and the answer must
// not move with the contents of the current view, because tui/model_preview.go
// compares a pane width against it to decide whether to fall back to the plain
// list. Folding a conditionally-reserved column in would make that threshold
// depend on which rows happen to be on screen, so a view would gain and lose
// its preview pane as rows came and went. The cost of leaving it out is bounded
// and already paid by loop, activity, reason, branch, and pulse: between
// MinRowWidth and
// MinRowWidth + the reserved dynamic columns, the directory column shrinks to
// zero and RenderList's own layout.StyledTruncate clamp at the render boundary
// keeps the row inside the viewport. Confirmed against
// tui/cross_mode_width_regression_test.go, which drives the real list renderer
// at 80/100/120 cells and fails on any line wider than the viewport, and
// against the narrow-fit scenarios (list-preview-narrow-fit and the
// blocked-reason pair), which assert no layout issue below the floor.
func MinRowWidth() int {
	return minimumTabNumberColumnWidth + lockColumnWidth + muteColumnWidth + layout.IconCellWidth + 1 + // tab + lock + mute + icon + space
		3 + 1 + // "[X]" badge + space
		harnessColWidth + 1 + // harness cell + space
		titleColWidth + 2 // title + separators before dir
}

// FormatRow formats a single session row.
// Format: tab_number | [loop] | state_icon | [reason] | account_prefix | <emoji> harness | title | [activity] | [branch] | directory
// tabNumberCell is a fixed-width, pre-rendered cell such as "1: " or
// " 1: ". It is blank-padded when the tab number is unavailable.
// activeLoopCell is empty when the loop column is not reserved; otherwise it is
// a fixed-width cell containing either 🔁 or spaces.
// reasonCell is empty when no row in the current view is in the attention group
// with a glossable blocked reason; otherwise it is a pre-rendered cell
// (reasonColumnWidth cells for the whole render pass, trailing space included)
// sitting immediately after the icon, and blank-padded for every row that has no
// reason of its own.
// activityCell is empty when no row in the current view is running a busy
// stretch; otherwise it is a pre-rendered cell (activityColumnWidth cells for
// the whole render pass, trailing space included) inserted immediately before
// the branch column.
// branchCell is empty when no row in the current view sits in a named checkout;
// otherwise it is a pre-rendered cell (branchColumnWidth cells for the whole
// render pass, trailing space included) sitting between the activity cell and
// the directory, and blank-padded for every row with no branch of its own.
// cfg supplies the [harness_emojis] lookup so the harness cell can render as
// "<emoji> <label>". harnessColor supplies the optional [harness_colors]
// foreground for that cell. When the configured emoji is empty, the cell
// collapses to just "<label>" so unconfigured users don't see a stray leading
// space.
func FormatRow(s session.RunningSession, acctColor, harnessColor map[string]string, cfg *config.Config, width int, tabNumberCell, activeLoopCell, reasonCell, activityCell, branchCell string) string {
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

	// Account prefix badge (first rune of account, uppercased). Use the first
	// rune rather than the first byte so a multibyte account name produces a
	// valid UTF-8 badge instead of a truncated invalid byte.
	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}

	// Project-identity color tracks the project's configured default profile so
	// override-launched rows keep the project's visual identity on the title and
	// badge. The badge letter and harness label still reflect the running
	// profile/harness. Falls back to the running account color when the project
	// is not configured (orphan tab).
	identityColor := ProjectIdentityColor(s, acctColor, cfg)
	style := accountStyle(identityColor)

	badge := style.Render("[" + acctBadge + "]")

	// Harness cell, always reserved so rows stay aligned regardless of which
	// harness (or none) the session uses. Its color is keyed by harness, not by
	// profile, so the column is stable across profile overrides.
	var harnessCell string
	if label := harnessLabel(s.Harness); label != "" {
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
		harnessCell = accountStyle(harnessColor[strings.ToLower(s.Harness)]).Render(text) + strings.Repeat(" ", pad)
	} else {
		harnessCell = strings.Repeat(" ", harnessColWidth)
	}

	// Title: project title, with a leading pictographic grapheme padded to the
	// fixed 2-cell title-icon column so emoji-prefixed titles align with each
	// other regardless of whether the source glyph measures as 1 or 2 cells.
	// Truncated to titleColWidth (grapheme-atomic) and right-padded to fill the
	// column.
	titleText := layout.RenderTitleIcon(SessionDisplayTitle(s))
	titleText = layout.Truncate(titleText, titleColWidth, "…")
	paddedTitle := layout.PadRight(titleText, titleColWidth)
	title := accountStyle(identityColor).Render(paddedTitle)

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		used := layout.Width(tabNumberCell) + lockColumnWidth + muteColumnWidth + layout.Width(activeLoopCell) + 3 + layout.StyledWidth(reasonCell) + 4 + harnessColWidth + 1 + titleColWidth + 3 + layout.Width(activityCell) + layout.StyledWidth(branchCell)
		// This enumeration is hand-maintained and MUST be walked against the
		// return statement below every time a cell is added, moved, or removed;
		// nothing checks the two against each other but
		// TestFormatRowWidthArithmeticMatchesTheComposedRow, which is why that
		// test exists. Term by term, in the order the row concatenates them:
		//
		//   tab number (measured) + lock (2) + mute (2) + optional loop (measured)
		//   + icon + space (3)
		//   + optional reason cell, whose width is set per render pass
		//   + "[X] " badge (4)
		//   + harness (10) + space (1)
		//   + title (20) + separators before the next cell (3)
		//   + optional activity cell, whose width is set per render pass
		//   + optional branch cell, whose width is set per render pass.
		//
		// The 3 charged after the title is 2 literal separator cells plus one
		// cell of deliberate slack, which is why it disagrees with MinRowWidth's
		// `titleColWidth + 2`. The slack is load-bearing in one direction only:
		// it makes the composed row one cell NARROWER than the budget, never
		// wider, so a terminal whose emoji metrics disagree with layout.Width by
		// a cell still cannot draw past the edge. Do not "fix" it to 2 without
		// re-running the cross-mode width regression tests at 80/100/120.
		//
		// The reason and branch cells are measured with StyledWidth because
		// renderReasonCell colors its tag and renderBranchCell dims its text, and
		// layout.Width would count their ANSI bytes as visible cells; the
		// activity cell's own styling wraps the cell only after this arithmetic
		// has already read it.
		available := width - used
		if available < 0 {
			available = 0
		}
		maxDir = available
	}
	dir = layout.TruncateLeft(dir, maxDir, "…")
	dir = styleDim.Render(dir)

	return tabNumberCell + lockCol + muteCol + activeLoopCell + iconCol + " " + reasonCell + badge + " " + harnessCell + " " + title + "  " + activityCell + branchCell + dir
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
