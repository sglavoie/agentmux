// Package asl owns the TUI's agent-session-loop workflow: the Runs list and
// its availability verdict, the per-run detail and supervision status display,
// the response modal that presents one question and carries an operator's
// answer or closure back, the run-level control confirmations, the bounded
// body-free receipt window, and the fixed refusal vocabulary every declined
// action is reported through.
//
// This is the operator-facing half of the agent-session-loop integration. The
// daemon's half — reading what an ASL run publishes, the lease and mailbox
// evidence, and the bridge subprocess itself — stays in internal/daemon/asl,
// which this package never imports; it reads an already-projected snapshot
// through internal/aslbridge instead.
//
// The leaf reads its facts from explicit input values rather than from the root
// Bubble Tea model, so it never imports the root tui package. Deciding what a
// keypress DOES — mutating model state, dispatching tea.Cmds, driving the
// toast list and the mode stack — stays in the root, which owns the program
// model.
//
// One rule governs everything here: no bridge-authored text and no operator
// answer may reach a status line, a toast, an error string, or a log. A
// question body and a draft are revealed only by the renderer that paints them,
// and a refusal is mapped from a stable code to a sentence this package wrote.
package asl

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The styles below are the ASL domain's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root theme
// is a ~50-style private surface shared by every TUI mode, and exporting it to
// serve a leaf would give every leaf a theme edge that the intended import
// graph does not draw. Only the attributes this package itself renders are
// duplicated here.
var (
	styleSelected     = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError        = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim          = lipgloss.NewStyle().Faint(true)
	styleHeader       = lipgloss.NewStyle().Bold(true)
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleBanner       = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)

	// styleOverlayBox wraps the response modal in a rounded border with the
	// de-facto accent teal. Padding adds a single column of breathing room on
	// each side; combined with the border, the box adds 2 columns + 2 rows of
	// chrome to inner content.
	styleOverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4ec9b0")).
			Padding(0, 1)
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves. The selected copy therefore keeps
// only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}
