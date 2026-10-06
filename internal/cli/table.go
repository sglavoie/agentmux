package cli

import (
	"io"
	"os"
	"strings"

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
