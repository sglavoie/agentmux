package launch

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
)

// Queued is one project a launch batch will start, paired with the display
// title the batch's progress messages name it by.
type Queued struct {
	Session config.Session
	Title   string
}

// LaunchQueue returns what Enter in the launcher starts, in stable launcher
// order, each stamped with the profile it will actually launch under. Checked
// projects the current filter shows are queued; with none checked, the
// highlighted project is, as the footer's "enter launch" promises. A checked
// project the filter hides is never launched blind: it is left out and counted
// in hidden so the caller can say so. Running launcher rows are included: a
// checked row that already has a live instance is an explicit request for
// another same-key tab.
func (in PickerInput) LaunchQueue() (todo []Queued, hidden int) {
	visible := make(map[int]bool, len(in.FilteredIdx))
	for _, idx := range in.FilteredIdx {
		visible[idx] = true
	}
	checked := 0
	for i := range in.Sessions {
		if !in.Selected[i] {
			continue
		}
		checked++
		if !visible[i] {
			hidden++
			continue
		}
		sess := in.SessionWithResolvedProfile(i)
		todo = append(todo, Queued{Session: sess, Title: configSessionDisplayTitle(sess)})
	}
	if checked > 0 {
		return todo, hidden
	}
	if in.Cursor < 0 || in.Cursor >= len(in.FilteredIdx) {
		return nil, 0
	}
	origIdx := in.FilteredIdx[in.Cursor]
	if origIdx < 0 || origIdx >= len(in.Sessions) {
		return nil, 0
	}
	sess := in.SessionWithResolvedProfile(origIdx)
	return []Queued{{Session: sess, Title: configSessionDisplayTitle(sess)}}, 0
}

// HiddenSkippedText is the warning shown when checked projects were left out
// of a launch because the current filter hides them.
func HiddenSkippedText(hidden int) string {
	if hidden == 1 {
		return "Skipped 1 checked project hidden by the filter"
	}
	return fmt.Sprintf("Skipped %d checked projects hidden by the filter", hidden)
}

// NothingToLaunchText is the warning shown when Enter finds nothing to
// launch, which keeps the launcher open. It is short enough to survive a
// 60-column status bar.
func NothingToLaunchText(hidden int) string {
	if hidden > 0 {
		return fmt.Sprintf("Nothing to launch: the filter hides %d checked", hidden)
	}
	return "Nothing to launch: no project matches"
}

// OpenTargets returns what `o` in the launcher opens, which is exactly what
// Enter would launch (LaunchQueue): checked projects the current filter shows,
// in stable launcher order, or the highlighted project when none is checked.
// A checked project the filter hides is never opened blind: it is left out and
// counted in hidden so the caller can say so.
func (in PickerInput) OpenTargets() (targets []config.Session, hidden int) {
	todo, hidden := in.LaunchQueue()
	for _, q := range todo {
		targets = append(targets, q.Session)
	}
	return targets, hidden
}

// NothingToOpenText is NothingToLaunchText for `o`, which likewise keeps the
// launcher open with nothing to open.
func NothingToOpenText(hidden int) string {
	if hidden > 0 {
		return fmt.Sprintf("Nothing to open: the filter hides %d checked", hidden)
	}
	return "Nothing to open: no project matches"
}

// StartText is the progress line shown the moment a batch is dispatched,
// before any single launch has reported back.
func StartText(total int) string {
	return fmt.Sprintf("Launching %d session(s)…", total)
}

// ProgressText is the progress line shown after each launch reports, naming
// how far the batch has got and which project just finished.
func ProgressText(done, total int, title string) string {
	return fmt.Sprintf("Launched %d/%d · %s", done, total, title)
}

// FailureText is the error line shown for a single failed launch. The batch
// keeps going, so this names the project that failed rather than the batch.
func FailureText(title string, err error) string {
	return fmt.Sprintf("launch failed: %s — %s", title, err)
}
