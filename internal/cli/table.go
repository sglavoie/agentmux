package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/sglavoie/agentmux/tui/layout"
)

// tableGap is the space between two columns.
const tableGap = "  "

// WriteTable writes header and rows as aligned columns for a human reader.
//
// Columns are measured in terminal cells, not runes: text/tabwriter counts a
// title like "🐱amx" as four wide when a terminal draws it five, and every row
// carrying an emoji title pushed the rest of its line out of alignment. The
// measurement is tui/layout's, which is what the dashboard itself draws with,
// so a title is as wide here as it is there. The last column is not padded.
func WriteTable(w io.Writer, header []string, rows [][]string) error {
	widths := make([]int, len(header))
	measure := func(row []string) {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], layout.Width(cell))
			}
		}
	}
	measure(header)
	for _, row := range rows {
		measure(row)
	}
	var b strings.Builder
	line := func(row []string) {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if i == len(row)-1 || i == len(widths)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(layout.PadRight(cell, widths[i]))
			b.WriteString(tableGap)
		}
		b.WriteByte('\n')
	}
	line(header)
	for _, row := range rows {
		line(row)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// IsTerminal reports whether w is a terminal a person is reading. Commands
// whose plain-text output predates a human-friendly layout use it to keep
// their tab-separated form for pipes and scripts; each takes it through its
// own seam, since a test cannot hand a command a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// TerminalWidth is how many cells wide the terminal w writes to is, or zero
// when w is not a terminal or will not say.
func TerminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(f.Fd())
	if err != nil || width < 0 {
		return 0
	}
	return width
}

// minLastColumn is the narrowest last column LastColumnWidth will fit a row
// into; a terminal with less room than that left over gets the fallback.
const minLastColumn = 20

// LastColumnWidth is how wide the last column of a WriteTable table may be
// for every row to fit in a terminal width cells wide. Only the cells before
// the last one are measured, so rows may carry their untruncated last cell.
// With no width, or too little left over for the last column to read as
// anything, it answers fallback and lets the terminal wrap the rare row.
func LastColumnWidth(header []string, rows [][]string, width, fallback int) int {
	if width <= 0 || len(header) == 0 {
		return fallback
	}
	used := 0
	for i := range len(header) - 1 {
		column := layout.Width(header[i])
		for _, row := range rows {
			if i < len(row) {
				column = max(column, layout.Width(row[i]))
			}
		}
		used += column + len(tableGap)
	}
	if room := width - used; room >= minLastColumn {
		return room
	}
	return fallback
}

// DisplayPath is how a human-facing table shows a directory: cleaned, so
// "dir/" and "dir" read alike, and with the home directory written as "~",
// which is how the config file usually spells it anyway. JSON output and
// tab-separated output for pipes keep the path as it was.
func DisplayPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == string(filepath.Separator) {
		return cleaned
	}
	if cleaned == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(cleaned, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return cleaned
}
