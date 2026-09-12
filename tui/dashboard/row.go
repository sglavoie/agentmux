// This file is how much of a queue line fits, and nothing about what it says.
// queue.go already decided that: a QueueRow carries the name, the qualifier, the
// status sentence, its emphasis, and the elapsed time. What is left is geometry.
//
// The row is three columns — Session, Status, Time — and they are aligned across
// the whole render pass rather than measured per row, because the operator reads
// the queue down a column: a status that started at a different cell on every
// line would have to be found before it could be read. The pass scans its widths
// once (ScanQueueColumns) and every row is laid out against the same answer,
// which is the same discipline the retired per-band column scans kept.
//
// Two properties survive from the layout this replaced and are the reason
// TestQueueRowWidthArithmeticMatchesTheComposedRow exists. Every field is
// measured in display cells through tui/layout, so an ANSI-styled cell is never
// counted by its bytes and a grapheme cluster is never cut in half. And the
// composed row is at most Width-1 cells, never more — see rowSafetySlack.
//
// What is NOT here is the whole point of the redesign. The tab number, the lock
// and mute indicators, the supervision marker, the state icon, the blocked-reason
// tag, the profile cell, the harness cell, the branch column, and the directory
// are all gone from the row. They are not lost: they are the selected session's
// context, and the session inspector is where they live.

package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// queueColumnGap separates two adjacent columns. Two cells rather than one:
	// the Session column's own content can end in a space (a padded short name
	// beside a long one), and a single-cell gap would then read as no gap at all
	// on the rows that need it most.
	queueColumnGap = 2

	// rowSafetySlack is one cell the allocator never spends. It is load-bearing
	// in one direction only: it makes the composed row one cell NARROWER than
	// the budget, never wider, so a terminal whose emoji metrics disagree with
	// layout.Width by a cell still cannot draw past the edge. The list
	// renderer's own StyledTruncate clamp cannot cover this, because it measures
	// with the same helper that was wrong.
	rowSafetySlack = 1

	// queueNameMinWidth is the floor the Session column is never reduced below
	// while anything else is still on the row. A name is the last thing a row
	// gives up: Status is dropped before the name reaches this, and Time before
	// Status. Eight cells is a truncated word plus its ellipsis — enough to tell
	// two rows apart when the operator already knows what is running, which is
	// the only claim a pane this narrow can make good on.
	queueNameMinWidth = 8

	// queueNameMaxWidth caps how far the Session column grows toward its widest
	// content. Past this the column stops helping anyone scan and starts pushing
	// Status and Time off to the right of where the eye is looking.
	queueNameMaxWidth = 44

	// queueStatusMinWidth is the narrowest Status column worth reserving, sized
	// to the shortest full sentence in the vocabulary that is not also a shorter
	// one's prefix (`Working`). Below this the column is dropped whole rather
	// than drawn as a stub: `Needs …` and `Needs p…` are the same four rows
	// wearing the same non-answer.
	queueStatusMinWidth = 7

	// queueStatusMaxWidth bounds the column at the longest status the vocabulary
	// can produce (`Needs trust approval`). It is a ceiling on the scan rather
	// than a truncation rule — no status is ever cut to it — so a vocabulary that
	// grew a longer phrase would widen the column instead of silently losing its
	// tail.
	queueStatusMaxWidth = 20

	// queueTimeWidth is the Time column: formatRunElapsed is bounded to
	// runTimerMaxCells by construction, so the column is fixed rather than
	// scanned and every row's time ends on the same cell.
	queueTimeWidth = runTimerMaxCells
)

// MinRowWidth is the narrowest width at which a queue row still answers both of
// the questions it exists to answer: which session this is, and what it is
// doing. It is the Session floor, the gap, and the Status floor, plus the row's
// one cell of slack.
//
// Below it a row is a truncated name and nothing else, which is why the root's
// split view falls back to the plain full-width list rather than pairing a
// preview with a column of clipped words. Above it the row does not stop laying
// out: it drops Time, then Status, and keeps drawing — so this is a threshold
// for the split, not a lower bound the renderer enforces.
//
// It is deliberately a constant expression over the floors and not a function of
// the current pass's scanned columns. A threshold that moved with the contents of
// the view would make a view gain and lose its preview pane as rows came and
// went, and a resize could oscillate across it.
func MinRowWidth() int {
	return queueNameMinWidth + queueColumnGap + queueStatusMinWidth + rowSafetySlack
}

// QueueColumns is the width each of the three columns was scanned to for one
// render pass. It travels as a value rather than as three ints for the reason
// RowInput does: three widths in a row are three positional ints nothing would
// stop a caller from permuting.
//
// A zero column means the pass found nothing to put in it — no row carried a
// status this build could name, or none carried a clock — and the column is then
// not reserved at all rather than padded around an empty slot.
type QueueColumns struct {
	// Name is the widest rendered Session cell in the pass: the icon-padded name
	// plus its parenthesized qualifier when it has one, bounded by
	// queueNameMaxWidth.
	Name int
	// Status is the widest status sentence in the pass, bounded by
	// queueStatusMaxWidth.
	Status int
	// Time is queueTimeWidth when any row has a clock to report, and zero when
	// none does.
	Time int
}

// ScanQueueColumns measures one render pass's columns.
//
// It scans the whole queue rather than the visible window, for the same reason
// the notice gutter is reserved across the whole projection: a column whose
// width depended on which rows happened to be scrolled into view would shift
// every line sideways as the operator paged through the list.
func ScanQueueColumns(queue []QueueRow) QueueColumns {
	var cols QueueColumns
	for _, q := range queue {
		if w := layout.Width(queueNameText(q)) + layout.Width(queueQualifierText(q)); w > cols.Name {
			cols.Name = w
		}
		if w := layout.Width(q.Status); w > cols.Status {
			cols.Status = w
		}
		if q.Elapsed != "" {
			cols.Time = queueTimeWidth
		}
	}
	if cols.Name > queueNameMaxWidth {
		cols.Name = queueNameMaxWidth
	}
	if cols.Status > queueStatusMaxWidth {
		cols.Status = queueStatusMaxWidth
	}
	return cols
}

// queueNameText is the Session cell's identity half: the display title with a
// leading pictographic grapheme padded to the fixed two-cell icon column, so a
// project emoji that measures as one cell in one terminal and two in another
// still leaves every name starting on the same cell.
//
// This is where "retain custom project emoji" is kept. The emoji is part of the
// title the operator configured; nothing here adds, substitutes, or strips one.
func queueNameText(q QueueRow) string {
	return layout.RenderTitleIcon(q.Name)
}

// queueQualifierText is the Session cell's disambiguating half, parenthesized
// and with its leading space, or "" when the name stands alone.
func queueQualifierText(q QueueRow) string {
	if q.Qualifier == "" {
		return ""
	}
	return " (" + q.Qualifier + ")"
}

// RowInput is everything one queue row is composed from.
//
// It is deliberately small. Every fact about what the row SAYS was already
// decided by BuildQueue, and every fact about how wide its columns are was
// already decided by ScanQueueColumns; what is left is this row's own budget.
// A renderer that still took the session, the config, and two color maps would be
// a renderer that could disagree with the queue about its own row.
type RowInput struct {
	// Queue is the projected line: name, qualifier, status, emphasis, elapsed.
	Queue QueueRow
	// Width is this row's own budget in cells, already reduced by the group
	// border, the notice gutter, and any child indent. Non-positive means
	// unbounded, which is what a caller with no viewport — a test, or a
	// pre-WindowSizeMsg first paint — means to ask for.
	Width int
	// Columns are the pass's scanned column widths. The zero value means "measure
	// this row alone", which is what a direct caller with a single row wants.
	Columns QueueColumns
}

// rowAlloc is the width each column of one row was granted. Zero means the
// column is not drawn at all. Every row in a pass gets the same allocation,
// because every input the allocator reads is a per-pass constant — which is what
// keeps the columns aligned without any row knowing about the others.
type rowAlloc struct {
	name   int
	status int
	time   int
}

// allocateRow spends the row's width on its three columns in priority order.
//
// The order IS the policy the session's brief states — "drop time before
// sacrificing essential identity or status" — and reading the steps top to
// bottom reads it:
//
//  1. the Session floor, taken partially when that is all there is, because a row
//     with a cut name is still a row and a row with none is not;
//  2. Status, whole or not at all;
//  3. Time, whole or not at all, and only behind a Status that was affordable —
//     a row showing an age with nothing to say what it is the age OF is a number
//     the operator has to guess at;
//  4. whatever is left grows the Session column toward its content.
//
// Nothing here wraps, and nothing can: the three columns are composed on one
// line and the total is bounded by Width-rowSafetySlack at every step.
func allocateRow(in RowInput) rowAlloc {
	cols := in.Columns
	if cols == (QueueColumns{}) {
		cols = ScanQueueColumns([]QueueRow{in.Queue})
	}

	var a rowAlloc
	if in.Width <= 0 {
		return rowAlloc{name: cols.Name, status: cols.Status, time: cols.Time}
	}

	rem := in.Width - rowSafetySlack
	if rem < 0 {
		rem = 0
	}

	a.name = min(queueNameMinWidth, rem)
	rem -= a.name

	if cols.Status > 0 && rem >= queueColumnGap+cols.Status {
		a.status = cols.Status
		rem -= queueColumnGap + cols.Status

		if cols.Time > 0 && rem >= queueColumnGap+cols.Time {
			a.time = cols.Time
			rem -= queueColumnGap + cols.Time
		}
	}

	if grow := min(cols.Name-a.name, rem); grow > 0 {
		a.name += grow
	}
	return a
}

// FormatRow composes one queue line: Session, Status, Time.
//
// The palette rules the redesign states are applied here and only here, mapped
// from QueueRow.Emphasis rather than re-derived from the session's state — which
// is what keeps "amber means the operator's turn" a single decision in queue.go
// instead of a switch every surface keeps its own copy of. The name is drawn
// neutral (unstyled, so nothing competes with the status beside it), the
// qualifier and the time muted, and the status in whatever weight its emphasis
// names.
func FormatRow(in RowInput) string {
	q := in.Queue
	a := allocateRow(in)

	// The Session cell is padded only when something follows it. Padding the last
	// cell on a line would spend the row's remaining budget on trailing blanks —
	// invisible, but wide enough to push the composed row past a clamp that had
	// nothing left to cut.
	row := renderQueueName(q, a.name, a.status > 0)

	if a.status > 0 {
		text := layout.Truncate(q.Status, a.status, "…")
		if a.time > 0 {
			text = layout.PadRight(text, a.status)
		}
		row += strings.Repeat(" ", queueColumnGap) + emphasisStyle(q.Emphasis).Render(text)
	}
	if a.time > 0 {
		row += strings.Repeat(" ", queueColumnGap) + styleDim.Render(padLeft(q.Elapsed, a.time))
	}
	return row
}

// renderQueueName draws the Session cell: the name, and the qualifier when both
// the row has one and the cell can pay for all of it.
//
// The qualifier is taken whole or not at all. A cut qualifier is worse than none:
// its entire job is to separate two rows that read identically, and `(feature/a…`
// beside `(feature/a…` separates nothing while looking as though it does.
func renderQueueName(q QueueRow, width int, pad bool) string {
	if width <= 0 {
		return ""
	}
	name := queueNameText(q)
	qualifier := queueQualifierText(q)

	var text string
	var used int
	if qualifier != "" && layout.Width(name)+layout.Width(qualifier) <= width {
		text = styleText.Render(name) + styleDim.Render(qualifier)
		used = layout.Width(name) + layout.Width(qualifier)
	} else {
		cut := layout.Truncate(name, width, "…")
		text = styleText.Render(cut)
		used = layout.Width(cut)
	}
	if pad && used < width {
		text += strings.Repeat(" ", width-used)
	}
	return text
}

// padLeft right-aligns a time in its column. The Time column is the one column
// whose values are read as magnitudes rather than as words, and right-aligning
// them is what lets `0:07` and `12h07` be compared down the column at a glance.
func padLeft(text string, width int) string {
	if pad := width - layout.Width(text); pad > 0 {
		return strings.Repeat(" ", pad) + text
	}
	return layout.Truncate(text, width, "…")
}

// harnessLabel returns the lowercase harness display name, or "" when the
// harness is unknown or empty. The queue row no longer carries a harness cell;
// this survives for the selected-session context the inspector renders.
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
