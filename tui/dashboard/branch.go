package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// The branch column: which checkout a row is sitting in, between the activity
// timer and the directory.
//
// It exists because managed worktrees are a headline feature, which makes "two
// rows in the same repository" the ordinary case rather than the exceptional
// one. Those rows share a project title, a profile, a harness and a directory
// PREFIX, and the directory column truncates from the left — so the one fact
// that actually distinguishes them is the last thing to survive the truncation
// and the first thing to be lost. Naming the branch says it in ten cells
// instead of forty.
//
// The daemon supplies the facts (state.WindowState.GitBranch/GitDirty →
// protocol.WindowStateSnapshot.Branch/Dirty → session.RunningSession). Deciding
// what they LOOK like is a presentation question and lives here, exactly as the
// reason tags do in reason.go.

// branchMaxWidth caps the branch column. Without a cap the column is not a
// column: one row on `release/2026-q3-migration-rollback` would silently charge
// its full width to every other row's directory, because the width is measured
// across the whole filtered view. Twenty cells fits the conventional
// `<prefix>/<topic>` shape whole — `amx/feature-name` plus its dirty marker is
// seventeen — and anything longer left-truncates onto the topic, which is the
// half that distinguishes two worktrees of one repository.
const branchMaxWidth = 20

// dirtyMarker is appended to a branch whose checkout has modified tracked
// files. A trailing glyph rather than a color so it survives the left
// truncation, a monochrome terminal, and stripAnsi in the scenario harness —
// and so it costs the column one cell rather than a second word.
const dirtyMarker = "*"

// branchText returns what a row's branch cell says before truncation, or "" when
// there is nothing to say.
//
// Several distinct situations collapse into that empty string, and the honest
// rendering of every one of them is the same: say nothing. The directory is not
// a repository; `git_probe = false` left the daemon with no prober; git is
// missing, slow, or timed out; HEAD is detached; no sweep has reached the window
// yet; or the dashboard is running with no daemon at all. internal/gitstate
// deliberately reports all of them as an empty branch rather than as an error,
// because this state decorates a row and a row that grew an error message would
// be worse than one that said nothing.
//
// The dirty bit is only rendered ON a branch. It is independently probed — a
// detached HEAD still reports it — but a bare `*` in a column whose header is
// the branch name would be a mark with nothing to mark, so an unnamed checkout
// stays blank.
func branchText(s session.RunningSession) string {
	if s.Branch == "" {
		return ""
	}
	if s.Dirty {
		return s.Branch + dirtyMarker
	}
	return s.Branch
}

// branchColumnWidth returns a stable width for every row in the current filtered
// result set: the widest branch text, capped at branchMaxWidth, plus one
// trailing space. Zero means no row in the view has a branch and the column is
// not reserved at all.
//
// Mirrors reasonColumnWidth, activityColumnWidth and tabNumberColumnWidth — one
// scan per render pass, so the column cannot jitter between rows of the same
// frame — and, like reasonColumnWidth, the zero return IS the reservation
// predicate. There is no separate "any row has a branch" boolean to keep in step
// with the measurement, so the two cannot disagree.
func branchColumnWidth(rows []session.RunningSession) int {
	widest := 0
	for _, s := range rows {
		if w := layout.Width(branchText(s)); w > widest {
			widest = w
		}
	}
	if widest == 0 {
		return 0
	}
	if widest > branchMaxWidth {
		widest = branchMaxWidth
	}
	return widest + 1
}

// renderBranchCell builds one branch cell padded to width. The trailing space is
// part of the cell so callers can concatenate it before the directory without an
// extra separator, exactly as renderActivityCell does.
//
// The truncation is layout.TruncateLeft with the directory's own ellipsis
// prefix, for the directory's own reason: the tail of `amx/feature-name` is what
// tells two worktrees apart, and cutting from the right would leave every branch
// under one prefix reading `amx/featu…`. Truncating the branch and its dirty
// marker together is what keeps the marker — it is the last grapheme, and
// TruncateLeft drops leading ones.
//
// A row with no branch pads rather than collapsing, for the reason the reason
// column pads: the non-repository row keeps its directory in the same column as
// the worktree row beside it, so a missing branch reads as a gap in one column
// rather than as a differently-shaped row.
func renderBranchCell(s session.RunningSession, width int) string {
	if width <= 0 {
		return ""
	}
	text := layout.TruncateLeft(branchText(s), width-1, "…")
	pad := width - layout.Width(text)
	if pad < 0 {
		pad = 0
	}
	if text == "" {
		return strings.Repeat(" ", pad)
	}
	return styleDim.Render(text) + strings.Repeat(" ", pad)
}
