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

// LaunchQueue returns the launcher's checked projects in stable launcher
// order, each stamped with the profile it will actually launch under. Running
// launcher rows are included: a checked row that already has a live instance
// is an explicit request for another same-key tab.
func (in PickerInput) LaunchQueue() []Queued {
	var todo []Queued
	for i := range in.Sessions {
		if !in.Selected[i] {
			continue
		}
		sess := in.SessionWithResolvedProfile(i)
		todo = append(todo, Queued{Session: sess, Title: configSessionDisplayTitle(sess)})
	}
	return todo
}

// OpenTargets returns every selected launcher project in stable launcher
// order. With no explicit selection, it preserves the launcher's historical
// behavior by returning only the highlighted project.
func (in PickerInput) OpenTargets() []config.Session {
	targets := make([]config.Session, 0, len(in.Selected))
	for i := range in.Sessions {
		if in.Selected[i] {
			targets = append(targets, in.SessionWithResolvedProfile(i))
		}
	}
	if len(targets) > 0 {
		return targets
	}
	if in.Cursor < 0 || in.Cursor >= len(in.FilteredIdx) {
		return nil
	}
	origIdx := in.FilteredIdx[in.Cursor]
	if origIdx < 0 || origIdx >= len(in.Sessions) {
		return nil
	}
	return []config.Session{in.SessionWithResolvedProfile(origIdx)}
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
