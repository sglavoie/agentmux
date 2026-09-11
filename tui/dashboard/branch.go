package dashboard

import (
	"github.com/sglavoie/agentmux/internal/session"
)

// Which checkout a row is sitting in.
//
// It exists because managed worktrees are a headline feature, which makes "two
// sessions in the same repository" the ordinary case rather than the exceptional
// one. Those sessions share a project title, a profile, a harness and a directory
// PREFIX, and a directory truncates from the left — so the one fact that actually
// distinguishes them is the last thing to survive the truncation and the first
// thing to be lost. Naming the branch says it in ten cells instead of forty.
//
// The queue row no longer draws it: a row is Session / Status / Time, and where a
// session is belongs to the selected session's context. Two callers remain — the
// detail strip, which spends its second line on the branch and the directory
// together, and the name disambiguator, which reaches for the branch first when
// two live sessions show the same title. Neither reserves a column, so there is
// no cap here any more: a long branch costs the one line it is drawn on rather
// than every row in the view.
//
// The daemon supplies the facts (state.WindowState.GitBranch/GitDirty →
// protocol.WindowStateSnapshot.Branch/Dirty → session.RunningSession). Deciding
// what they LOOK like is a presentation question and lives here, exactly as the
// status vocabulary does in queue.go.

// dirtyMarker is appended to a branch whose checkout has modified tracked
// files. A trailing glyph rather than a color so it survives a left truncation,
// a monochrome terminal, and stripAnsi in the scenario harness — and so it costs
// one cell rather than a second word.
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
