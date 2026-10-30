package notification

// Notification backends and click-to-focus contract.
//
// macOS notifications require Alerter 26.x or newer. A pane-specific
// notification also carries the reusable Kitty socket that can focus its exact
// source after Alerter reports a click. Notifications are suppressed when
// either half of that interactive contract is unavailable.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// MaybeNotify fires a notification for the given window if:
//   - the action is notifyable (stop/permission/failure)
//   - the corresponding config flag is enabled
//   - neither configured quiet hours nor the in-memory snooze covers now
//   - the 10s cooldown has elapsed since the last notification for this window
//   - pipeMode is false (non-interactive Claude/Copilot runs suppress notifications)
//
// Permission notifications are delayed by the configured debounce window and
// only fire if that permission remains the latest relevant event for the window
// when the debounce expires. Another permission event refreshes the timer;
// CancelPending discards the pending notification when a later non-permission
// event makes it stale.
//
// This compatibility entry point has no captured Kitty socket, so its
// notifications are informational. Production pane notifications use
// MaybeNotifyTarget with a reusable socket and Navigable set to true.
func (n *Notifier) MaybeNotify(windowID int, sessionTitle, account, action string, pipeMode bool) {
	n.MaybeNotifyTarget(NotificationTarget{
		WindowID: windowID, Account: account,
	}, sessionTitle, action, pipeMode)
}

// MaybeNotifyTarget is MaybeNotify with an explicit socket-aware click target.
func (n *Notifier) MaybeNotifyTarget(target NotificationTarget, sessionTitle, action string, pipeMode bool) {
	n.MaybeNotifyTracedTarget(target, sessionTitle, action, pipeMode, NotificationTrace{})
}

// MaybeNotifyTraced is MaybeNotify with causal identity retained across every
// suppression, debounce, dispatch, and focus outcome.
func (n *Notifier) MaybeNotifyTraced(windowID int, sessionTitle, account, action string, pipeMode bool, trace NotificationTrace) {
	n.MaybeNotifyTracedTarget(NotificationTarget{
		WindowID: windowID, Account: account,
	}, sessionTitle, action, pipeMode, trace)
}

// MaybeNotifyTracedTarget retains both causal identity and the exact Kitty
// focus target across debounce, dispatch, and click processing.
func (n *Notifier) MaybeNotifyTracedTarget(target NotificationTarget, sessionTitle, action string, pipeMode bool, trace NotificationTrace) {
	windowID := target.WindowID
	account := target.Account
	if trace.TraceID == "" {
		trace.TraceID = diagnostics.NewTraceID()
	}
	if trace.Profile == "" {
		trace.Profile = account
	}
	emit := func(stage, outcome, reason string) {
		n.emitDisposition(NotificationDisposition{
			Trace: trace, WindowID: windowID, Action: action,
			Stage: stage, Outcome: outcome, Reason: reason,
		})
	}
	if pipeMode {
		emit("decision", "suppressed", "pipe_mode")
		return
	}
	if n.mutedWindow(windowID) {
		emit("decision", "suppressed", "muted")
		return
	}
	if !notifyableActions[action] {
		emit("decision", "suppressed", "unsupported_action")
		return
	}
	if !n.actionEnabled(action) {
		emit("decision", "suppressed", "config_disabled")
		return
	}
	if n.quietSet && n.quiet.Contains(n.now()) {
		emit("decision", "suppressed", "quiet_hours")
		return
	}
	if n.snoozed(n.now()) {
		emit("decision", "suppressed", "snoozed")
		return
	}
	n.mu.Lock()
	dispatchAvailable := n.dispatch != nil
	focusAvailable := n.focus != nil
	n.mu.Unlock()
	// Either backend can carry the notification; runDispatch chooses between
	// them when it runs, since the app may come or go during a debounce.
	if !dispatchAvailable && !n.appReady() {
		emit("decision", "suppressed", "backend_unavailable")
		return
	}
	// Production wires focus before accepting requests. The focusAvailable
	// guard preserves isolated reducer tests whose intentionally incomplete
	// handler topology has no Kitty client at all.
	// A runtime session's click needs no Kitty socket: it is shown by session.
	if target.Navigable && !target.sessionFocus() && focusAvailable && !kitty.IsReusableListenAddress(target.KittyListenOn) {
		emit("decision", "suppressed", "focus_unavailable")
		return
	}

	n.mu.Lock()
	var dispositions []NotificationDisposition

	title := sessionTitle
	if title == "" {
		title = "Agent Mux"
	}
	subtitle := notificationLabels[action]

	if action != "permission" {
		if disposition, ok := n.cancelPendingLocked(windowID, "superseded"); ok {
			dispositions = append(dispositions, disposition)
		}
	}

	key := notifyKey{windowID: windowID, action: action}
	now := n.now()
	if action == "permission" && n.debounce > 0 {
		if last, ok := n.last[key]; ok && now.Sub(last) < n.cooldown {
			n.mu.Unlock()
			for _, disposition := range dispositions {
				n.emitDisposition(disposition)
			}
			emit("decision", "suppressed", "cooldown")
			return
		}
		if disposition, ok := n.schedulePermissionLocked(target, title, subtitle, trace); ok {
			dispositions = append(dispositions, disposition)
		}
		n.mu.Unlock()
		for _, disposition := range dispositions {
			n.emitDisposition(disposition)
		}
		emit("decision", "scheduled", "debounce")
		return
	}

	if last, ok := n.last[key]; ok && now.Sub(last) < n.cooldown {
		n.mu.Unlock()
		for _, disposition := range dispositions {
			n.emitDisposition(disposition)
		}
		emit("decision", "suppressed", "cooldown")
		return
	}
	n.last[key] = now
	// The notice about to be shown replaces any app notice still showing for
	// this window. It is retracted only here, past every gate, so a newer
	// event that is itself suppressed never leaves the operator with nothing.
	app, superseded := n.takeAppNoticesLocked(windowID)
	n.mu.Unlock()
	n.retractAppNotices(app, superseded, "superseded")
	for _, disposition := range dispositions {
		n.emitDisposition(disposition)
	}
	emit("decision", "scheduled", "dispatch")
	// Deliberately uncapped: the cooldown above bounds how many dispatches can
	// be blocked at once, and a cap would have to drop either the newest
	// notice or a banner the operator may be about to click. See "In-flight
	// dispatches" in docs/daemon.md.
	go n.runDispatch(target, title, subtitle, action, trace)
}

// runDispatch chooses a backend for one notification and records its
// outcome. A ready companion app is offered the notification first (see
// app_backend.go); when it does not confirm delivery the app_fallback
// disposition is recorded and the Alerter path runs as if no app existed.
//
// The Alerter dispatcher runs under a bounded context. The dispatcher's
// underlying process (or test stub) is responsible for honoring ctx.Done — the
// real Alerter backend does via exec.CommandContext. The complete target is
// retained so a click can use the exact captured socket and window ID after
// either backend reports it.
func (n *Notifier) runDispatch(target NotificationTarget, title, subtitle, action string, trace NotificationTrace) {
	windowID := target.WindowID
	account := target.Account
	n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "attempted", Reason: "backend_started"})
	n.mu.Lock()
	parent := n.ctx
	dispatch := n.dispatch
	app := n.app
	n.mu.Unlock()
	if app != nil && app.Ready() {
		if n.dispatchToApp(parent, app, target, title, subtitle, action, trace) {
			return
		}
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "fallback", Reason: "app_fallback", Backend: BackendApp})
	}
	if dispatch == nil {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "suppressed", Reason: "backend_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(parent, n.dispatchTimeout)
	defer cancel()
	result, err := dispatch(ctx, windowID, account, title, subtitle)
	if err != nil {
		outcome, reason := "failed", "send_failed"
		switch {
		case parent.Err() != nil:
			outcome, reason = "cancelled", "daemon_shutdown"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			reason = "backend_timeout"
		}
		slog.Warn("notifier: dispatch did not complete", "err", err, "outcome", outcome, "reason", reason, "title", title, "subtitle", subtitle)
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: outcome, Reason: reason, Backend: BackendAlerter})
		return
	}
	if result.delivery != deliveryDelivered {
		slog.Warn("notifier: backend returned success without delivery", "delivery", result.delivery, "title", title, "subtitle", subtitle)
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "failed", Reason: "send_failed", Backend: BackendAlerter})
		return
	}
	n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "sent", Reason: "send_success", Backend: BackendAlerter})
	n.settleInteraction(target, action, trace, result.interaction, BackendAlerter)
}

// settleInteraction records the terminal interaction with a delivered
// notification, whichever backend showed it. Only a click focuses anything;
// every other interaction, including the companion's safe actions, is
// recorded as a suppressed focus naming what the operator did instead. The
// actions themselves are carried out by the companion, not here.
func (n *Notifier) settleInteraction(target NotificationTarget, action string, trace NotificationTrace, interaction interactionOutcome, backend string) {
	windowID := target.WindowID
	emit := func(outcome, reason string) {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: outcome, Reason: reason, Backend: backend})
	}
	if interaction != interactionClicked {
		reason := interaction
		switch reason {
		case interactionTimeout, interactionDismissed, interactionReplied, interactionUnavailable, interactionUnknown,
			interactionActionMute, interactionActionSnooze, interactionActionAnswer, interactionActionRespond, interactionRetracted:
		default:
			reason = interactionUnknown
		}
		emit("suppressed", string(reason))
		return
	}
	slog.Debug("notifier: clicked", "window_id", windowID, "account", target.Account, "backend", backend)
	if !target.Navigable || windowID <= 0 {
		emit("suppressed", "focus_target_missing")
		return
	}
	n.mu.Lock()
	focus := n.focus
	sessionFocus := n.sessionFocus
	n.mu.Unlock()
	if target.sessionFocus() {
		// The companion shows a runtime session itself and asks with
		// focus_session; doing it here as well would show it twice.
		if backend == BackendApp {
			emit("suppressed", "focus_delegated")
			return
		}
		if sessionFocus == nil {
			emit("suppressed", "focus_unavailable")
			return
		}
		switch err := sessionFocus(target.RuntimeSessionID); {
		case err == nil:
			emit("focused", "focus_success")
		case errors.Is(err, ErrFocusTargetStale):
			emit("suppressed", "focus_target_stale")
		case errors.Is(err, ErrFocusNoViewer):
			emit("suppressed", "focus_no_viewer")
		default:
			emit("failed", "focus_failed")
		}
		return
	}
	if focus == nil {
		emit("suppressed", "focus_unavailable")
		return
	}
	if !kitty.IsReusableListenAddress(target.KittyListenOn) {
		emit("suppressed", "focus_unavailable")
		return
	}
	if err := n.focusForClick(focus, target); err != nil {
		if errors.Is(err, ErrFocusTargetStale) {
			emit("suppressed", "focus_target_stale")
			return
		}
		emit("failed", "focus_failed")
		return
	}
	emit("focused", "focus_success")
}

// focusForClick brings the source of a clicked notification back to the
// foreground through FocusKittyWindow. It returns errors so runDispatch can
// record a precise terminal focus disposition after the notification
// subprocess exits.
func (n *Notifier) focusForClick(focus FocusClient, target NotificationTarget) error {
	_, err := FocusKittyWindow(focus, target.KittyListenOn, target.focusWindowID())
	return err
}

// ErrFocusTargetStale reports that the window to focus is no longer in the
// tree its Kitty instance lists.
var ErrFocusTargetStale = errors.New("kitty focus target is no longer live")

// KittyWindowLocation names where a focused window lives: the tab that was
// brought forward and the OS window that holds it.
type KittyWindowLocation struct {
	OSWindowID int
	TabID      int
	WindowID   int
}

// FocusKittyWindow focuses windowID in the Kitty instance at socket. It lists
// that instance first and returns ErrFocusTargetStale without writing anything
// when the window is gone, then applies the two-step focus pattern — FocusTab
// before FocusWindow, so a window in a background tab is actually shown. Both
// steps are attempted even when the first fails; their errors are joined. The
// location is the one the verifying list reported, and is returned with any
// focus error so a caller can still name what it tried to focus.
//
// It is shared by click-to-focus and the daemon's focus_window request, so the
// two cannot disagree about what focusing a window means. The caller decides
// which sockets are acceptable; this function dials the one it is given.
func FocusKittyWindow(focus FocusClient, socket string, windowID int) (KittyWindowLocation, error) {
	windows, err := focus.ListWindows(socket)
	if err != nil {
		return KittyWindowLocation{}, err
	}
	osWindow, tab, _, ok := kitty.FindWindowLocationByID(windows, windowID)
	if !ok {
		return KittyWindowLocation{}, ErrFocusTargetStale
	}
	loc := KittyWindowLocation{OSWindowID: osWindow.ID, TabID: tab.ID, WindowID: windowID}
	var errs []error
	if err := focus.FocusTab(socket, kitty.MatchTabByWindowID(windowID)); err != nil {
		slog.Debug("notifier: focus tab failed", "err", err, "window_id", windowID)
		errs = append(errs, err)
	}
	if err := focus.FocusWindow(socket, kitty.MatchWindowByID(windowID)); err != nil {
		slog.Debug("notifier: focus window failed", "err", err, "window_id", windowID)
		errs = append(errs, err)
	}
	return loc, errors.Join(errs...)
}

func (n *Notifier) actionEnabled(action string) bool {
	switch action {
	case "stop":
		return n.cfg.Stop
	case "permission":
		return n.cfg.Permission
	case "elicitation":
		return n.cfg.ElicitationEnabled()
	case "failure":
		return n.cfg.Error
	case "stop_failure":
		return n.cfg.StopFailure == nil || *n.cfg.StopFailure
	case "stale":
		return n.cfg.StaleActive == nil || *n.cfg.StaleActive
	case "hook_silence":
		return n.cfg.HookSilence == nil || *n.cfg.HookSilence
	}
	return false
}

// MarkNotified records a dispatched notification for (windowID, action) at
// `at`, starting its cooldown. It is the write half of the cooldown
// bookkeeping PurgeWindow releases when Kitty stops reporting a window.
func (n *Notifier) MarkNotified(windowID int, action string, at time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.last[notifyKey{windowID: windowID, action: action}] = at
}

// HasCooldown reports whether a cooldown entry is recorded for
// (windowID, action), regardless of whether it has already elapsed.
func (n *Notifier) HasCooldown(windowID int, action string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.last[notifyKey{windowID: windowID, action: action}]
	return ok
}

// ResetCooldown clears all per-action cooldowns for windowID (for testing).
func (n *Notifier) ResetCooldown(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k := range n.last {
		if k.windowID == windowID {
			delete(n.last, k)
		}
	}
}

// ResetActionCooldown permits an independently identified user-attention event
// to notify even when an earlier event of the same class was recent.
func (n *Notifier) ResetActionCooldown(windowID int, action string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.last, notifyKey{windowID: windowID, action: action})
}

// PurgeWindow drops every cooldown entry and any pending permission timer for
// windowID. Called by the daemon's per-sweep closed-window GC so the notifier's
// bookkeeping cannot accumulate dead entries (or fire a long-debounced
// permission notification for a window that no longer exists).
func (n *Notifier) PurgeWindow(windowID int) {
	n.mu.Lock()
	for k := range n.last {
		if k.windowID == windowID {
			delete(n.last, k)
		}
	}
	disposition, cancelled := n.cancelPendingLocked(windowID, "window_closed")
	app, closed := n.takeAppNoticesLocked(windowID)
	n.mu.Unlock()
	n.retractAppNotices(app, closed, "window_closed")
	if cancelled {
		n.emitDisposition(disposition)
	}
}
