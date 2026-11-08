package overview

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// sessionLabel is what to call a target in a sentence.
func sessionLabel(target sessionactions.Target) string {
	return dashboard.SessionDisplayTitle(target.Session)
}

// withReport puts one sentence on the feedback row for a bounded time.
//
// It clears the focus error, which is the other thing that row reports about
// something the operator asked for. Exactly one of the two can be true at a
// time by construction: a new operation is newer news than a focus that failed
// before it, and leaving both would make the row's ranking decide which of two
// current facts is hidden.
func (m model) withReport(text string, ttl time.Duration) model {
	m.acts.report = text
	m.acts.reportUntil = m.deps.now().Add(ttl)
	m.acts.retryUntil = time.Time{}
	m.acts.reportSticky = false
	m.acts.reportPending = false
	m.focusErr = nil
	return m
}

// withFailureReport is withReport for a failure, which stays until replaced.
func (m model) withFailureReport(text string) model {
	m = m.withReport(text, reportWindow)
	m.acts.reportSticky = true
	return m
}

// withPendingReport is withReport for an operation still in flight.
func (m model) withPendingReport(text string) model {
	m = m.withReport(text, reportPendingWindow)
	m.acts.reportPending = true
	return m
}

// withRecoveryReport is withReport for the one sentence that must be shown
// INSTEAD of the recovery offer rather than beneath it, and it is the only
// report that names a retry.
//
// The offer normally outranks every report, because it is the only thing on that
// row which expires into something unrecoverable. A relaunch that FAILED is the
// exception, and it is the exception precisely because the stack releases the
// entry back onto the offer: without this, pressing `u` and having the launch
// fail would put the row back to the same "closed X · u undo" it showed before
// the keystroke, which is what a dropped keypress looks like. The failure and the
// retry are one sentence about one entry, so they take the row together.
//
// retryUntil is the deadline that released entry KEPT, and it is what makes the
// exception end when its reason does. The report's own ttl is about how long a
// failure is worth reading; the entry's deadline is about how long `u` will take
// it, and the two are independent — a relaunch can fail a moment before expiry,
// or after it, and in both the sentence would otherwise go on advertising a
// retry the stack has already stopped granting. Past that deadline the failure
// drops the retry and stops displacing the offer, which by then may name only
// OTHER closes that are still recoverable. It does not extend the window: the
// deadline is carried in, never recomputed here.
func (m model) withRecoveryReport(text string, retryUntil time.Time) model {
	m = m.withFailureReport(text)
	m.acts.retryUntil = retryUntil
	return m
}

func (m model) clearReport() model {
	m.acts.report = ""
	m.acts.reportUntil = time.Time{}
	m.acts.retryUntil = time.Time{}
	m.acts.reportSticky = false
	m.acts.reportPending = false
	return m
}

// offerOwningReport is the failed-relaunch report together with the retry it is
// still granting, or "" when there is no current such report or its entry has
// stopped being claimable. It is the only report the feedback row places above
// the offer; see withRecoveryReport.
//
// The retry is appended HERE rather than baked into the stored sentence, because
// whether `u` would take the entry is a question about the current time and the
// row is repainted several times a second. A failure whose retry has lapsed
// still says what failed — it falls to the rank every other action outcome sits
// at, under an offer that may still name something restorable.
func (m model) offerOwningReport() string {
	remaining := m.retryRemaining()
	if remaining <= 0 {
		return ""
	}
	report := m.activeReport()
	if report == "" {
		return ""
	}
	return fmt.Sprintf("%s · u retry (%s)", report, retryWindowText(remaining))
}

// retryRemaining is what is left of the released entry's own window, or zero
// when no failed relaunch is current or its entry is no longer claimable.
//
// It is measured against the deadline the report carried in rather than against
// the stack, because Claim walks to the most RECENT recoverable entry and this
// sentence is about one particular one: a later close can be claimable while the
// entry this failure named is not.
func (m model) retryRemaining() time.Duration {
	if m.acts.retryUntil.IsZero() {
		return 0
	}
	return m.acts.retryUntil.Sub(m.deps.now())
}

// retryWindowText spells what is LEFT of a window rather than the window itself,
// rounded UP so a retry that is still claimable never reads "0s".
//
// A retry can arrive partway through the recovery window. Its countdown uses
// the original deadline and never grants a fresh window after failure.
func retryWindowText(remaining time.Duration) string {
	seconds := int((remaining + time.Second - 1) / time.Second)
	return fmt.Sprintf("%ds", max(seconds, 1))
}

// activeReport is the report if it has not expired. Expiry is evaluated on read
// rather than swept by a timer of its own: the interval and capture tickers
// already repaint this surface several times a second, so a third ticker would
// buy nothing but a third thing to stop on shutdown.
//
// A failure is retained and an in-flight sentence outlives its window as
// "still waiting: …", so the row never goes blank under an operator whose
// action has not answered or did not work.
func (m model) activeReport() string {
	switch {
	case m.acts.report == "":
		return ""
	case m.acts.reportSticky || m.deps.now().Before(m.acts.reportUntil):
		return m.acts.report
	case m.acts.reportPending:
		return "still waiting: " + m.acts.report
	default:
		return ""
	}
}

// beginActionReport says that a dispatched action is in flight, for the four
// actions the shared vocabulary has wording for. The rest announce nothing on
// the way out: focus, peek, copy, lock, mute and the two open-somewhere actions
// are their own feedback, and a sentence for each would be noise over a surface
// that shows the result.
func (m model) beginActionReport(d sessionactions.Dispatch) model {
	wording, ok := sessionactions.FeedbackFor(d.Action, sessionLabel(d.Target), d.Profile)
	if !ok {
		return m
	}
	return m.withPendingReport(wording.Pending)
}

// reportActionOutcome replaces the in-flight sentence with what became of it.
//
// An action with no wording normally says nothing, which is right for one that
// never announced itself — except when it FAILED. A failure nobody reports is
// the grid silently declining to do what was asked, so the menu's own label is
// borrowed to name it rather than adding a second wording table beside the
// shared one.
func (m model) reportActionOutcome(result sessionactions.Result) model {
	label := sessionLabel(result.Target)
	if wording, ok := sessionactions.FeedbackFor(result.Action, label, result.Profile); ok {
		if text, said := wording.Outcome(result.Outcome, result.Detail); said {
			if result.Outcome == sessionactions.OutcomeFailed || result.Outcome == sessionactions.OutcomeUnconfirmed {
				return m.withFailureReport(text)
			}
			return m.withReport(text, reportWindow)
		}
		return m.clearReport()
	}
	if result.Outcome != sessionactions.OutcomeFailed {
		return m.clearReport()
	}
	verb := string(result.Action)
	if definition, found := sessionactions.Lookup(result.Action); found && definition.MenuLabel != "" {
		verb = definition.MenuLabel
	}
	return m.withFailureReport(
		sessionactions.FailureText(fmt.Sprintf("%s failed for %s", verb, label), result.Detail))
}

// handleActionDone settles one correlated completion and refreshes the topology,
// because most of these actions changed the window tree the grid is drawn from.
func (m model) handleActionDone(msg commands.ActionDoneMsg) (tea.Model, tea.Cmd) {
	updated, result, correlated := actionController(m).Complete(
		m,
		sessionactions.RequestID(msg.Request),
		sessionactions.Completion{Err: msg.Err, Unconfirmed: msg.Unconfirmed},
	)
	if !correlated {
		return m, nil
	}
	m = updated
	if result.Action == commands.CloseSession {
		m = m.applyCloseOutcome(result)
	} else {
		m = m.reportActionOutcome(result)
	}
	return m, m.requestRefresh()
}

// handlePickerOpenDone reports the result of opening a shell or editor. The
// operator returns to the overview after either view closes.
func (m model) handlePickerOpenDone(msg commands.PickerOpenDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.withFailureReport("open failed: " + msg.Err.Error()), nil
	}
	return m, nil
}

// handlePeekDone stays put either way. The pager is an overlay over the session's
// own window and the operator returns from it to this grid.
func (m model) handlePeekDone(msg commands.PeekDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m.withFailureReport("peek failed: " + msg.Err.Error()), nil
	}
	return m, nil
}
