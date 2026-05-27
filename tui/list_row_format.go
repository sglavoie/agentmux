package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

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
// Format: state_icon | account_prefix | [harness] | title | [age] | directory
// ageCell is empty when no row in the current view carries a watchdog entry;
// otherwise it is a pre-rendered fixed-width cell (ageColumnWidth cells,
// trailing space included) inserted immediately before the directory column.
func formatRow(s session.RunningSession, acctColor, harnessColor map[string]string, width int, ageCell string) string {
	// Lock indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is locked.
	lockCol := "  "
	if s.Locked {
		lockCol = palette.LockIcon()
		if w := runewidth.StringWidth(lockCol); w < lockColumnWidth {
			lockCol += strings.Repeat(" ", lockColumnWidth-w)
		}
	}

	// Mute indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is muted.
	muteCol := "  "
	if s.Muted {
		muteCol = palette.MuteIcon()
		if w := runewidth.StringWidth(muteCol); w < muteColumnWidth {
			muteCol += strings.Repeat(" ", muteColumnWidth-w)
		}
	}

	// State icon at the start for instant scanning.
	iconCol := "  "
	if s.State != "" {
		icon := palette.StateIcon(s.State)
		iconW := iconDisplayWidth(icon)
		if pad := 2 - iconW; pad > 0 {
			icon += strings.Repeat(" ", pad)
		}
		iconCol = icon
	}

	// Account prefix badge (first rune of account, uppercased). Use the first
	// rune rather than the first byte so a multibyte account name produces a
	// valid UTF-8 badge instead of a truncated invalid byte.
	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}

	colorName := acctColor[s.Account]
	style := accountStyle(colorName)

	badge := style.Render("[" + acctBadge + "]")

	// Harness badge column, always reserved so rows stay aligned regardless
	// of which harness (or none) the session uses.
	var harnessCell string
	if label := harnessLabel(s.Harness); label != "" {
		text := "[" + label + "]"
		pad := harnessColWidth - runewidth.StringWidth(text)
		if pad < 0 {
			pad = 0
		}
		harnessCell = accountStyle(harnessColor[s.Account]).Render(text) + strings.Repeat(" ", pad)
	} else {
		harnessCell = strings.Repeat(" ", harnessColWidth)
	}

	// Title: use the project title padded to titleColWidth.
	title := runewidth.Truncate(runningSessionDisplayTitle(s), titleColWidth, "…")
	paddedTitle := title + strings.Repeat(" ", titleColWidth-runewidth.StringWidth(title))
	title = accountStyle(acctColor[s.Account]).Render(paddedTitle)

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		used := lockColumnWidth + muteColumnWidth + 3 + 4 + harnessColWidth + 1 + titleColWidth + 3
		// lock (2) + mute (2) + icon+space (3) + "[X] " badge (4) + harness (9)
		//   + space (1) + title (20) + separators before dir (3)
		available := width - used
		if available > 10 {
			maxDir = available
		}
	}
	if runewidth.StringWidth(dir) > maxDir {
		runes := []rune(dir)
		for runewidth.StringWidth(string(runes))+1 > maxDir {
			runes = runes[1:]
		}
		dir = "…" + string(runes)
	}
	dir = styleDim.Render(dir)

	return lockCol + muteCol + iconCol + " " + badge + " " + harnessCell + " " + title + "  " + ageCell + dir
}

// iconDisplayWidth returns the visual cell width of a state icon. We rely
// on go-runewidth's measurement; FE0F variation selectors are reported as
// zero-width which matches how Kitty actually renders these glyphs as a
// single cell. The caller (padIconCell / formatRow) is responsible for
// padding to the 2-cell icon column.
func iconDisplayWidth(icon string) int {
	return runewidth.StringWidth(icon)
}

// padIconCell returns icon padded to exactly 2 visual cells so the icon
// column stays a stable width regardless of which state glyph is rendered.
func padIconCell(icon string) string {
	w := iconDisplayWidth(icon)
	if w >= 2 {
		return icon
	}
	return icon + strings.Repeat(" ", 2-w)
}

func centerInWidth(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	pad := (width - w) / 2
	return strings.Repeat(" ", pad) + s
}
