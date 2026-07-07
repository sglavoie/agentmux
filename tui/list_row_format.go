package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
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

// formatRow formats a single session row.
// Format: [loop] | state_icon | account_prefix | <emoji> harness | title | [age] | directory
// activeLoopCell is empty when the loop column is not reserved; otherwise it is
// a fixed-width cell containing either 🔁 or spaces.
// ageCell is empty when no row in the current view carries a watchdog entry;
// otherwise it is a pre-rendered fixed-width cell (ageColumnWidth cells,
// trailing space included) inserted immediately before the directory column.
// cfg supplies the [harness_emojis] lookup so the harness cell can render as
// "<emoji> <label>". harnessColor supplies the optional [harness_colors]
// foreground for that cell. When the configured emoji is empty, the cell
// collapses to just "<label>" so unconfigured users don't see a stray leading
// space.
// minListRowWidth is the narrowest width at which formatRow can lay out its
// fixed columns before the directory column: lock + mute + state icon + a
// space + the "[X]" account badge + a space + the harness cell + a space +
// the title column + the two separator spaces that precede the directory.
// The dynamic columns (loop, age, pulse, group border) are excluded because
// they are only reserved when a row needs them. Below this floor the row can
// no longer shrink, so renderListAt clamps it and renderSplit falls back to
// the plain full-width list.
func minListRowWidth() int {
	return lockColumnWidth + muteColumnWidth + titleIconCellWidth + 1 + // lock + mute + icon + space
		3 + 1 + // "[X]" badge + space
		harnessColWidth + 1 + // harness cell + space
		titleColWidth + 2 // title + separators before dir
}

func formatRow(s session.RunningSession, acctColor, harnessColor map[string]string, cfg *config.Config, width int, activeLoopCell, ageCell string) string {
	// Lock indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is locked.
	lockCol := "  "
	if s.Locked {
		lockCol = displayPadRight(palette.LockIcon(), lockColumnWidth)
	}

	// Mute indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is muted.
	muteCol := "  "
	if s.Muted {
		muteCol = displayPadRight(palette.MuteIcon(), muteColumnWidth)
	}

	// State icon at the start for instant scanning. displayPadIconCell pads
	// the empty-state placeholder to the same 2-cell column reserved for
	// glyph rows.
	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State)
	}
	iconCol := displayPadIconCell(icon)

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
	identityColor := projectIdentityColor(s, acctColor, cfg)
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
		pad := harnessColWidth - displayWidth(text)
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
	titleText := displayRenderTitleIcon(runningSessionDisplayTitle(s))
	titleText = displayTruncate(titleText, titleColWidth, "…")
	paddedTitle := displayPadRight(titleText, titleColWidth)
	title := accountStyle(identityColor).Render(paddedTitle)

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		used := lockColumnWidth + muteColumnWidth + displayWidth(activeLoopCell) + 3 + 4 + harnessColWidth + 1 + titleColWidth + 3 + displayWidth(ageCell)
		// lock (2) + mute (2) + optional loop + icon+space (3) + "[X] " badge (4)
		//   + harness (10) + space (1) + title (20) + separators before dir (3)
		//   + optional age cell.
		available := width - used
		if available < 0 {
			available = 0
		}
		maxDir = available
	}
	dir = displayTruncateLeft(dir, maxDir, "…")
	dir = styleDim.Render(dir)

	return lockCol + muteCol + activeLoopCell + iconCol + " " + badge + " " + harnessCell + " " + title + "  " + ageCell + dir
}
