package tui

import tuiplans "github.com/sglavoie/agentmux/tui/plans"

// planViewerRows wraps the loaded plan lines to the model's current width.
// The row model itself lives in tui/plans; this is the one place the Model
// supplies it with the two fields it needs.
func planViewerRows(m Model) []tuiplans.ViewerRow {
	return tuiplans.ViewerRows(m.planViewerLines, m.width)
}
