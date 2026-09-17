// Undoing a close from the grid.
//
// A close is the one action here that destroys something the surface cannot get
// back by refreshing, so the shared recovery stack is offered on this surface
// for the same reason it is offered on the dashboard: for a few seconds after a
// CONFIRMED close, `u` relaunches what was closed.
//
// The three-stage lifecycle is the stack's, not this file's. A dispatch RESERVES
// a snapshot while the row still exists, a confirmed completion PROMOTES it to an
// offer with a deadline, and `u` CLAIMS it so a second press takes the close
// below it rather than launching the same session twice. What is here is the two
// ends of that: what a snapshot of one of these rows contains, and what a
// relaunch's outcome does to the offer.
//
// One refusal is specific to this surface and is worth stating plainly: the tab
// this overview is running in is never recoverable. Closing it takes the grid,
// the feedback row, and the five seconds the offer would have lasted with it, so
// an offer made there would be an affordance that dies before it can be used.
package overview

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// recoveryDoneMsg reports one relaunch attempted from the recovery stack.
//
// It is this package's own message rather than a commands.ActionDoneMsg because
// its request identity is minted by the recovery stack, and a request numbered 1
// there names a different operation from a request numbered 1 in the session
// action controller. Sharing the message would have the controller correlate the
// two by accident.
type recoveryDoneMsg struct {
	request sessionactions.RequestID
	label   string
	err     error
}

// recoverySnapshot reconstructs what a relaunch of rs would need, before the
// close that makes it unreconstructible.
//
// It answers false in two cases and both mean "there will be nothing to offer":
// the host tab, for the reason at the top of this file, and a profile that is not
// in the loaded configuration, which has no launch path at all.
func (m model) recoverySnapshot(rs session.RunningSession) (sessionactions.Snapshot, bool) {
	if actionHost(m).Hosts(sessionactions.Capture(rs)) {
		return sessionactions.Snapshot{}, false
	}
	account, ok := commands.FindAccountByName(m.profileAccounts(), rs.EffectiveProfile())
	if !ok {
		return sessionactions.Snapshot{}, false
	}
	return sessionactions.Snapshot{
		Session: session.SessionFromRunning(rs),
		Account: account,
		Label:   dashboard.SessionDisplayTitle(rs),
	}, true
}

// applyCloseOutcome is what a reported close does to the recovery stack and to
// the feedback row.
//
// Only a CONFIRMED close publishes a recovery. An unconfirmed one — the tab was
// still listed when the polls ran out, or the listing could not be read — and a
// failed one both discard the reservation, because relaunching a session that is
// still running produces a second copy of it rather than an undo.
//
// A published offer clears the pending report instead of replacing it: the offer
// itself says "closed X", with the undo beside it, so reporting the close again
// underneath would be the same sentence twice on a row that holds one.
func (m model) applyCloseOutcome(result sessionactions.Result) model {
	if result.Outcome != sessionactions.OutcomeSucceeded {
		m.acts.recovery = m.acts.recovery.Discard(result.Request)
		return m.reportActionOutcome(result)
	}
	now := m.deps.now()
	recovery, offered := m.acts.recovery.Confirm(result.Request, now.Add(sessionactions.RecoveryWindow))
	m.acts.recovery = recovery
	if !offered {
		return m.reportActionOutcome(result)
	}
	m.focusErr = nil
	return m.clearReport()
}

// recoverable reports whether `u` would do anything.
//
// It used to be what the footer drew a key's availability from, and it is not
// any more: `u` is off the footer, because a close is undoable for five seconds
// after a confirmed close and a permanent entry would spend a slot on a key that
// is dim almost always. What is left is the model's own predicate for the state
// the offer above describes, which is the vocabulary the recovery tests state
// their conditions in rather than each reaching into the stack for itself.
func (m model) recoverable() bool {
	_, ok := m.acts.recovery.Latest(m.deps.now())
	return ok
}

// recoveryOffer is the sentence the feedback row carries while a close can still
// be undone, or "" when none can.
//
// A single entry names itself; several collapse into a count with "undo last" so
// the affordance stays accurate. The window is printed because the offer expires
// with the entries it names.
func (m model) recoveryOffer() string {
	active := m.acts.recovery.Available(m.deps.now())
	switch len(active) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("closed %s · u undo (%s)", active[0].Label, undoWindowText)
	default:
		return fmt.Sprintf("closed %d sessions · u undo last (%s)", len(active), undoWindowText)
	}
}

// undoLastClose claims the most recent recoverable close and relaunches it.
//
// The seam is checked BEFORE the claim. A claim is a state change — it takes the
// entry off the offer — so claiming against a model that cannot relaunch would
// consume a recovery and return no command, which reads as the undo having
// silently failed.
func (m *model) undoLastClose() tea.Cmd {
	if m.deps.relaunch == nil {
		return nil
	}
	now := m.deps.now()
	recovery, claimed, ok := m.acts.recovery.Claim(now)
	m.acts.recovery = recovery
	if !ok {
		return nil
	}
	m.acts.report = fmt.Sprintf("restoring %s…", claimed.Snapshot.Label)
	m.acts.reportUntil = now.Add(reportPendingWindow)
	m.focusErr = nil
	return m.deps.relaunch(claimed.Request, claimed.Snapshot)
}

// handleRecoveryDone applies one relaunch's outcome.
//
// A success consumes the entry; a failure releases it with the deadline it
// already had, so the operator may retry for exactly as long as the close was
// going to stay undoable and no longer.
func (m model) handleRecoveryDone(msg recoveryDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.acts.recovery = m.acts.recovery.Release(msg.request)
		// The retry is named in the same sentence because the released entry is
		// back on the offer and the offer is the thing this sentence displaces.
		m = m.withRecoveryReport(fmt.Sprintf("restore failed for %s: %v · u retry (%s)", msg.label, msg.err, undoWindowText), reportWindow)
	} else {
		m.acts.recovery = m.acts.recovery.Restored(msg.request)
		m = m.withReport(fmt.Sprintf("restored %s", msg.label), reportWindow)
	}
	return m, m.requestRefresh()
}

// relaunchCmd is the default relaunch seam. Like actionCommand it is a package
// function over the configuration and the client, so nothing it does can depend
// on where the cursor has moved since the close.
func relaunchCmd(cfg *config.Config, client kitty.KittyClient, request sessionactions.RequestID, snapshot sessionactions.Snapshot) tea.Cmd {
	sess := snapshot.Session
	label := snapshot.Label
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = recoveryDoneMsg{request: request, label: label, err: fmt.Errorf("restore panicked: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(
			context.Background(),
			actions.Dependencies{Kitty: client, Config: cfg},
			actions.Launch,
			actions.Request{Project: &sess},
		)
		return recoveryDoneMsg{request: request, label: label, err: err}
	}
}
