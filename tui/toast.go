// This file is the root's transient feedback: the toast queue that reports what
// an action is doing and what became of it, and the close recovery those reports
// offer.
//
// What a toast says is tied to an operation's outcome rather than to its
// dispatch. A key used to push "closed api" and a recovery entry the moment the
// close command was handed to Bubble Tea, before Kitty had been asked anything —
// so a close that failed, and a close nobody could confirm, both read as done,
// and both left an undo that would have launched a second copy of a session that
// was still running. Every lifecycle action here now says what it is doing while
// it is in flight and what happened when it reports back, on one addressable
// toast channel per dispatch.
//
// The recovery stack itself lives in tui/sessionactions, not here: the overview
// is the second surface that will offer it, and a stack held in this model's own
// fields could not be handed over.

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// toastSeverity classifies a toast for styling and ordering.
type toastSeverity int

const (
	toastInfo toastSeverity = iota
	toastSuccess
	toastWarning
	toastError
)

// maxToasts caps the number of transient status messages retained. Older
// toasts are dropped when a push would exceed this — newest-on-top means the
// sticky status bar always sees the most recent active action first.
const maxToasts = 3

// defaultToastTTL is the lifetime of a transient toast. Progress toasts
// reuse this so a stalled launch chain still clears itself eventually.
const defaultToastTTL = 4 * time.Second

// progressToastTTL is the lifetime of in-flight progress toasts — short
// enough that an abandoned launch doesn't linger, long enough that the
// per-launch tick (kitten @ launch is sub-second) won't TTL mid-flight.
const progressToastTTL = 10 * time.Second

// toastTickInterval drives the TTL sweeper. Coarse enough to avoid waking
// the runloop too often, fine enough that visible expiry is snappy.
const toastTickInterval = 250 * time.Millisecond

// Toast is a transient message shown through the sticky status bar.
type Toast struct {
	Severity toastSeverity
	Text     string
	Expires  time.Time
	// id ties a toast to a logical channel (e.g., a launch batch) so a
	// follow-up push can replace the existing toast instead of stacking
	// alongside it. Zero means "not addressable" — the toast is purely
	// transient.
	id int
}

// toastTickMsg fires on toastTickInterval while at least one toast is
// alive. Handler sweeps expired toasts and re-arms the ticker if any
// remain.
type toastTickMsg struct{}

// launchProgressMsg reports the result of a single picker-launched
// session. The picker dispatches one of these per selected session via
// tea.Batch so the toast subsystem can show 1/N, 2/N progress.
type launchProgressMsg struct {
	batchID int
	total   int
	title   string
	err     error
}

// toastTickCmd schedules the next toast TTL sweep.
func toastTickCmd() tea.Cmd {
	return tea.Tick(toastTickInterval, func(time.Time) tea.Msg {
		return toastTickMsg{}
	})
}

// pushToast appends t to m.toasts, capping at maxToasts (newest on top).
// Returns a cmd that arms the toast tick when the list transitions from
// empty to non-empty; otherwise nil (the existing tick handles TTL).
func (m *Model) pushToast(t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = modelNow(*m).Add(defaultToastTTL)
	}
	wasEmpty := len(m.toasts) == 0
	// Newest on top: prepend and trim.
	m.toasts = append([]Toast{t}, m.toasts...)
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[:maxToasts]
	}
	if wasEmpty {
		return toastTickCmd()
	}
	return nil
}

// replaceToastByID replaces (or pushes) a toast carrying the given id.
// Used for progress toasts so successive "Launched 1/N · …" updates
// share one logical channel rather than stacking. Replacements move to the
// top so the sticky status bar treats the update as the latest transient.
// id == 0 means "not addressable" so the toast is appended as a fresh
// transient entry instead of merging with whatever happens to sit in the slot.
func (m *Model) replaceToastByID(id int, t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = modelNow(*m).Add(defaultToastTTL)
	}
	t.id = id
	if id != 0 {
		for i := range m.toasts {
			if m.toasts[i].id == id {
				if i == 0 {
					m.toasts[0] = t
					return nil
				}
				copy(m.toasts[1:i+1], m.toasts[0:i])
				m.toasts[0] = t
				return nil
			}
		}
	}
	return m.pushToast(t)
}

func (m *Model) clearToastByID(id int) {
	if id == 0 || len(m.toasts) == 0 {
		return
	}
	kept := make([]Toast, 0, len(m.toasts))
	for _, t := range m.toasts {
		if t.id != id {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
}

// sweepToasts drops every toast whose TTL has elapsed. Returns a cmd
// re-arming the tick when any toast is still alive; nil otherwise so the
// ticker quiesces when nothing is on screen.
//
// Both this and clearToastByID filter into a fresh array rather than compacting
// with `m.toasts[:0]`. The model is copied by value on every update, so a
// compaction writes through every copy that shares the backing array — the
// stale one a pending command still holds included, which would see a toast it
// never dropped replaced by a later one.
func (m *Model) sweepToasts() tea.Cmd {
	now := modelNow(*m)
	kept := make([]Toast, 0, len(m.toasts))
	for _, t := range m.toasts {
		if now.Before(t.Expires) {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
	if len(m.toasts) == 0 {
		return nil
	}
	return toastTickCmd()
}

// undoCloseToastID is the addressable id shared by every close-recovery toast.
// Successive recoverable closes collapse into a single "closed N sessions"
// message via replaceToastByID.
const undoCloseToastID = -1

// undoWindow is how long a close remains undoable. It is the shared vocabulary's
// window rather than a second declaration of it: the deadline the recovery stack
// enforces and the lifetime of the toast advertising it have to be the same
// number or the affordance outlives what it offers.
const undoWindow = sessionactions.RecoveryWindow

// undoWindowText is how the window is spelled on the toast. It is derived from
// the window rather than typed beside it, so a toast cannot go on promising five
// seconds after the stack starts granting some other number.
var undoWindowText = fmt.Sprintf("%ds", int(undoWindow/time.Second))

// actionFeedbackToastID gives one dispatch its own addressable toast channel, so
// the in-flight message can be replaced by the outcome instead of stacking
// beneath it, and so two actions in flight at once do not overwrite each other.
//
// The ids are negative and offset by one past the recovery channel, which keeps
// them clear of both undoCloseToastID and the positive launch-batch ids. A
// dispatch with no request identity is not addressable and gets id zero, which
// pushToast treats as an ordinary transient.
func actionFeedbackToastID(request sessionactions.RequestID) int {
	if !request.Valid() {
		return 0
	}
	return -int(request) - 1
}

// The wording itself lives in tui/sessionactions, beside the definitions the
// palette and the overlay already read. The overview reports the same four
// operations on its single feedback row, and a table held here would have been
// copied there and then edited in one place.

// beginActionFeedback reports that a dispatched action is in flight. The toast
// lives on the dispatch's own channel, so the outcome replaces it rather than
// piling on top of it, and it carries the progress TTL so an operation that
// never reports back still clears itself.
func (m *Model) beginActionFeedback(request sessionactions.RequestID, action commands.ID, label, profile string) tea.Cmd {
	wording, ok := sessionactions.FeedbackFor(action, label, profile)
	if !ok {
		return nil
	}
	ttl := defaultToastTTL
	if request.Valid() {
		ttl = progressToastTTL
	}
	return m.replaceToastByID(actionFeedbackToastID(request), Toast{
		Severity: toastInfo,
		Text:     wording.Pending,
		Expires:  modelNow(*m).Add(ttl),
	})
}

// withActionFeedback is what the three lifecycle runners that need no
// bookkeeping of their own return: the effect, plus the in-flight report on the
// dispatch's own channel. Close is the exception and is spelled out in
// closeWithToast, because it also reserves the recovery.
func (m *Model) withActionFeedback(d sessionactions.Dispatch, cmd tea.Cmd) tea.Cmd {
	feedback := m.beginActionFeedback(d.Request, d.Action, sessionLabel(d.Target), d.Profile)
	if feedback == nil {
		return cmd
	}
	return tea.Batch(cmd, feedback)
}

// reportActionOutcome replaces a dispatch's in-flight toast with what became of
// it. A result for an action with no wording clears the channel and says
// nothing, which is what an action that never announced itself should do.
func (m *Model) reportActionOutcome(result sessionactions.Result) tea.Cmd {
	id := actionFeedbackToastID(result.Request)
	wording, ok := sessionactions.FeedbackFor(result.Action, sessionLabel(result.Target), result.Profile)
	if !ok {
		m.clearToastByID(id)
		return nil
	}
	// What the toast SAYS comes from the shared wording — the overview reports the
	// same outcomes on its feedback row, and a second switch here is how the two
	// used to be able to describe one close differently. What it LOOKS like is
	// this surface's own business, which is why the severity is decided beside it
	// rather than carried in the vocabulary.
	text, ok := wording.Outcome(result.Outcome, result.Detail)
	if !ok {
		// Refused and cancelled never reach here: they are decided before an
		// effect runs and reported by the step the controller returns.
		m.clearToastByID(id)
		return nil
	}
	return m.replaceToastByID(id, Toast{
		Severity: outcomeSeverity(result.Outcome),
		Text:     text,
		Expires:  modelNow(*m).Add(defaultToastTTL),
	})
}

// outcomeSeverity is how loudly each reportable outcome is drawn. It covers
// exactly the three Feedback.Outcome has a sentence for; anything else never
// reaches a toast, and info is the harmless answer if one ever did.
func outcomeSeverity(outcome sessionactions.Outcome) toastSeverity {
	switch outcome {
	case sessionactions.OutcomeSucceeded:
		return toastSuccess
	case sessionactions.OutcomeUnconfirmed:
		return toastWarning
	case sessionactions.OutcomeFailed:
		return toastError
	default:
		return toastInfo
	}
}

// sessionLabel is what to call a target in a sentence.
func sessionLabel(target sessionactions.Target) string {
	return dashboard.SessionDisplayTitle(target.Session)
}

// closeWithToast dispatches the close, reserves the recovery, and reports that
// the close is in flight.
//
// The reservation is not yet an offer: it captures the snapshot while the row
// still exists, and applyCloseOutcome promotes it only if the close is confirmed.
// Capturing later is not an option — the row is by then deliberately gone — and
// offering earlier is what used to advertise an undo for closes that failed.
func (m *Model) closeWithToast(rs session.RunningSession, request sessionactions.RequestID) tea.Cmd {
	cmds := []tea.Cmd{commands.Close(m.client, rs.TabID)}
	if snapshot, ok := m.recoverySnapshot(rs); ok {
		m.recovery = m.recovery.Reserve(request, snapshot)
	}
	if c := m.beginActionFeedback(request, commands.CloseSession, dashboard.SessionDisplayTitle(rs), ""); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// recoverySnapshot reconstructs what a relaunch of rs would need, before the
// close that makes it unreconstructible.
//
// It answers false in two cases, and both mean "there will be no recovery to
// offer". A profile that is not in the loaded configuration has no launch path.
// And the tab this dashboard is itself running in takes the dashboard with it:
// the surface that would show the undo, and the five seconds it would last, both
// end with the close.
func (m Model) recoverySnapshot(rs session.RunningSession) (sessionactions.Snapshot, bool) {
	if m.actionHost.Hosts(sessionactions.Capture(rs)) {
		return sessionactions.Snapshot{}, false
	}
	var accounts []config.Account
	if m.cfg != nil {
		accounts = m.cfg.Accounts
	}
	account, ok := commands.FindAccountByName(accounts, rs.EffectiveProfile())
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
// the operator's feedback. It is the one action with bookkeeping of its own, so
// it is spelled out rather than routed through reportActionOutcome.
//
// Only a confirmed close publishes a recovery. An unconfirmed one — the tab was
// still listed when the polls ran out, or the listing could not be read — and a
// failed one both discard the reservation, because relaunching a session that is
// still running produces a second copy of it rather than an undo.
func (m *Model) applyCloseOutcome(result sessionactions.Result) tea.Cmd {
	if result.Outcome != sessionactions.OutcomeSucceeded {
		m.recovery = m.recovery.Discard(result.Request)
		return m.reportActionOutcome(result)
	}

	now := modelNow(*m)
	recovery, offered := m.recovery.Confirm(result.Request, now.Add(undoWindow))
	m.recovery = recovery
	if !offered {
		return m.reportActionOutcome(result)
	}
	// The recovery toast says "closed X" itself, with the undo beside it, so the
	// per-dispatch channel has nothing left to report.
	m.clearToastByID(actionFeedbackToastID(result.Request))
	return m.refreshRecoveryToast(now)
}

// The "session is locked — press L to unlock" denial toast is gone with the
// keys that raised it. It existed because a direct shortcut had no way to say
// "not on this row" before it was pressed; the three destructive commands are
// reached through the actions overlay and the palette now, and both draw an
// unavailable entry dimmed with its reason beside it. The toast's own text was
// the tell: it named `L`, which is no longer the unlock key, and a refusal that
// has to name a key is a refusal arriving too late.

// refreshRecoveryToast rewrites the one toast that names the recoverable closes,
// or clears it when there are none left. It expires with the last entry it
// names, so the affordance never outlives what it offers.
func (m *Model) refreshRecoveryToast(now time.Time) tea.Cmd {
	text := m.undoToastText()
	deadline, ok := m.recovery.Deadline(now)
	if text == "" || !ok {
		m.clearToastByID(undoCloseToastID)
		return nil
	}
	return m.replaceToastByID(undoCloseToastID, Toast{
		Severity: toastInfo,
		Text:     text,
		Expires:  deadline,
	})
}

// undoToastText returns the user-facing message for the recovery toast. A single
// entry shows its label; several collapse into a count with "undo last" so the
// affordance stays accurate.
//
// It names `u`, which is bound at the dashboard again, and the palette command
// remains for anyone who reaches for it by name. The window is worth printing
// either way: both routes expire with the entries, so a toast that outlived them
// would be offering something already gone.
func (m *Model) undoToastText() string {
	active := m.recovery.Available(modelNow(*m))
	switch len(active) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("closed %s · %s undo (%s)", active[0].Label, keyUndoClose, undoWindowText)
	default:
		return fmt.Sprintf("closed %d sessions · %s undo last (%s)", len(active), keyUndoClose, undoWindowText)
	}
}

// activeUndoEntries reports the closes that may still be undone. It is what the
// Undo Last Close command's availability reads, so an expired entry is never
// silently revived by a palette that had not looked recently.
func (m Model) activeUndoEntries() []sessionactions.Snapshot {
	return m.recovery.Available(modelNow(m))
}

// sweepRecovery drops entries past their deadline and keeps the recovery toast's
// wording in step with what is left. Called from the toast tick, so the stack
// cannot accumulate after repeated closes nobody undid.
func (m *Model) sweepRecovery() {
	now := modelNow(*m)
	m.recovery = m.recovery.Sweep(now)
	if !m.hasToastID(undoCloseToastID) {
		return
	}
	// The toast list is non-empty here, so refreshRecoveryToast replaces in
	// place and returns no command; a second tick command would double the
	// sweeper.
	m.refreshRecoveryToast(now)
}

func (m Model) hasToastID(id int) bool {
	for _, t := range m.toasts {
		if t.id == id {
			return true
		}
	}
	return false
}

// recoveryDoneMsg reports one relaunch attempted from the recovery stack.
//
// It is the root's own message rather than a commands.ActionDoneMsg because its
// request identity is minted by the recovery stack, and a request numbered 1
// there names a different operation from a request numbered 1 in the session
// action controller. Sharing the message would have the controller correlate the
// two by accident.
type recoveryDoneMsg struct {
	request sessionactions.RequestID
	label   string
	err     error
}

// undoLastCmd claims the most recent recoverable close and relaunches it.
//
// The entry is claimed rather than consumed: it is off the offer while the
// relaunch is in flight, so a second `u` takes the close below it instead of
// launching the same session twice, and a failed relaunch puts it back with the
// time it had left.
func (m *Model) undoLastCmd() tea.Cmd {
	now := modelNow(*m)
	recovery, claimed, ok := m.recovery.Claim(now)
	m.recovery = recovery
	if !ok {
		return m.refreshRecoveryToast(now)
	}

	sess := claimed.Snapshot.Session
	label := claimed.Snapshot.Label
	request := claimed.Request
	cfg := m.cfg
	client := m.client
	launch := func() tea.Msg {
		_, err := commands.Registry.Execute(context.Background(), actions.Dependencies{Kitty: client, Config: cfg}, actions.Launch, actions.Request{Project: &sess})
		return recoveryDoneMsg{request: request, label: label, err: err}
	}

	cmds := []tea.Cmd{launch}
	if c := m.replaceToastByID(undoCloseToastID, Toast{
		Severity: toastInfo,
		Text:     fmt.Sprintf("restoring %s…", label),
		Expires:  now.Add(progressToastTTL),
	}); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

// handleRecoveryDone applies one relaunch's outcome. A success consumes the
// entry; a failure releases it, keeping the deadline it already had, so the
// operator may retry for as long as the close was going to stay undoable and no
// longer.
func (m Model) handleRecoveryDone(msg recoveryDoneMsg) (tea.Model, tea.Cmd) {
	now := modelNow(m)
	var cmds []tea.Cmd
	if msg.err != nil {
		m.recovery = m.recovery.Release(msg.request)
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     fmt.Sprintf("restore failed for %s: %v", msg.label, msg.err),
		}); c != nil {
			cmds = append(cmds, c)
		}
		if c := m.refreshRecoveryToast(now); c != nil {
			cmds = append(cmds, c)
		}
	} else {
		m.recovery = m.recovery.Restored(msg.request)
		restored := Toast{Severity: toastSuccess, Text: fmt.Sprintf("restored %s", msg.label)}
		if m.undoToastText() == "" {
			// Nothing is left to undo, so the recovery channel carries the
			// result rather than being cleared and immediately replaced.
			if c := m.replaceToastByID(undoCloseToastID, restored); c != nil {
				cmds = append(cmds, c)
			}
		} else {
			if c := m.refreshRecoveryToast(now); c != nil {
				cmds = append(cmds, c)
			}
			if c := m.pushToast(restored); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	m.actionErr = msg.err
	cmds = append(cmds, m.requestTopologyRefresh())
	return m, tea.Batch(cmds...)
}

// copyExcerptWidth bounds the copied text echoed back in the success toast.
const copyExcerptWidth = 48

// copyExcerpt is the first non-blank line of a copy, trimmed to a toast-sized
// width and marked when anything was left out.
func copyExcerpt(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	first := strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		first += " …"
	}
	return layout.Truncate(first, copyExcerptWidth, "…")
}
