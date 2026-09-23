// This file is what an action SAYS about itself while it runs and once it has
// finished.
//
// It lives beside the definitions for the same reason the availability rules do.
// Two surfaces report these operations now — the dashboard's toasts and the
// overview's single feedback row — and a wording table held privately by one of
// them would be copied into the other and then edited in one place. The four
// sentences are the same act described at four moments; which widget carries
// them is the surface's business, and what they say is not.

package sessionactions

import (
	"fmt"

	"github.com/sglavoie/agentmux/tui/commands"
)

// Feedback is what one action says about itself at each stage of one dispatch.
// The four are written side by side because the difference between them is the
// whole point: Succeeded is past tense and may only be said about an operation
// that reported success, Unconfirmed says what was asked for without claiming it
// happened, and Failed names the verb that did not work.
type Feedback struct {
	Pending     string
	Succeeded   string
	Unconfirmed string
	Failed      string
}

// FeedbackFor is the wording table. It covers the four actions that change a
// session's lifecycle and nothing else: focus, peek, lock, mute, copy, and the
// surfaces that open something are their own feedback, and a sentence for each of
// them would be noise over a surface that already shows the result.
//
// The clear-status wording is deliberately the odd one out. That action asks the
// daemon to drop its guard and the daemon's renderer owns the title, so no
// surface here ever learns whether the icon changed; it says what it requested
// rather than claiming a state it cannot see.
func FeedbackFor(action commands.ID, label, profile string) (Feedback, bool) {
	switch action {
	case commands.CloseSession:
		return Feedback{
			Pending:     fmt.Sprintf("closing %s…", label),
			Succeeded:   fmt.Sprintf("closed %s", label),
			Unconfirmed: fmt.Sprintf("close requested for %s · not confirmed", label),
			Failed:      fmt.Sprintf("close failed for %s", label),
		}, true
	case commands.RestartSession:
		return Feedback{
			Pending:     fmt.Sprintf("restarting %s…", label),
			Succeeded:   fmt.Sprintf("restarted %s", label),
			Unconfirmed: fmt.Sprintf("restart requested for %s · not confirmed", label),
			Failed:      fmt.Sprintf("restart failed for %s", label),
		}, true
	case commands.OverrideProfile:
		return Feedback{
			Pending:     fmt.Sprintf("restarting %s under %s…", label, profile),
			Succeeded:   fmt.Sprintf("restarted %s under %s", label, profile),
			Unconfirmed: fmt.Sprintf("restart requested for %s under %s · not confirmed", label, profile),
			Failed:      fmt.Sprintf("restart failed for %s under %s", label, profile),
		}, true
	case commands.ClearStatusIcon:
		requested := fmt.Sprintf("requested status clear on %s", label)
		return Feedback{
			Pending:     fmt.Sprintf("clearing the status icon on %s…", label),
			Succeeded:   requested,
			Unconfirmed: requested,
			Failed:      fmt.Sprintf("status clear failed for %s", label),
		}, true
	case commands.MarkActiveIcon:
		requested := fmt.Sprintf("requested active mark on %s", label)
		return Feedback{
			Pending:     fmt.Sprintf("marking %s active…", label),
			Succeeded:   requested,
			Unconfirmed: requested,
			Failed:      fmt.Sprintf("active mark failed for %s", label),
		}, true
	default:
		return Feedback{}, false
	}
}

// Outcome is the sentence one completed dispatch is reported with, and false
// when this action reports nothing. The refused and cancelled outcomes are
// decided before an effect runs and are reported by the step the controller
// returns, so they have no sentence here.
func (f Feedback) Outcome(outcome Outcome, detail string) (string, bool) {
	switch outcome {
	case OutcomeSucceeded:
		return f.Succeeded, true
	case OutcomeUnconfirmed:
		return f.Unconfirmed, true
	case OutcomeFailed:
		return FailureText(f.Failed, detail), true
	}
	return "", false
}

// FailureText appends the underlying reason to a failure verb when there is one.
func FailureText(verb, detail string) string {
	if detail == "" {
		return verb
	}
	return verb + ": " + detail
}
