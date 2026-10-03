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
	n.mu.Lock()
	dispatchAvailable := n.dispatch != nil
	focusAvailable := n.focus != nil
	n.mu.Unlock()
	if !dispatchAvailable {
		emit("decision", "suppressed", "backend_unavailable")
		return
	}
	// Production wires focus before accepting requests. The focusAvailable
	// guard preserves isolated reducer tests whose intentionally incomplete
	// handler topology has no Kitty client at all.
	if target.Navigable && focusAvailable && !kitty.IsReusableListenAddress(target.KittyListenOn) {
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
	n.mu.Unlock()
	for _, disposition := range dispositions {
		n.emitDisposition(disposition)
	}
	emit("decision", "scheduled", "dispatch")
	go n.runDispatch(target, title, subtitle, action, trace)
}

// runDispatch invokes the configured dispatcher under a bounded context. The
// dispatcher's underlying process (or test stub) is responsible for honoring
// ctx.Done — the real Alerter backend does via exec.CommandContext.
// The complete target is retained so a click can use the exact captured socket
// and window ID after the backend returns.
func (n *Notifier) runDispatch(target NotificationTarget, title, subtitle, action string, trace NotificationTrace) {
	windowID := target.WindowID
	account := target.Account
	n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "attempted", Reason: "backend_started"})
	n.mu.Lock()
	parent := n.ctx
	dispatch := n.dispatch
	n.mu.Unlock()
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
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: outcome, Reason: reason})
		return
	}
	if result.delivery != deliveryDelivered {
		slog.Warn("notifier: backend returned success without delivery", "delivery", result.delivery, "title", title, "subtitle", subtitle)
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "failed", Reason: "send_failed"})
		return
	}
	n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "dispatch", Outcome: "sent", Reason: "send_success"})
	if result.interaction != interactionClicked {
		reason := result.interaction
		switch reason {
		case interactionTimeout, interactionDismissed, interactionReplied, interactionUnavailable, interactionUnknown:
		default:
			reason = interactionUnknown
		}
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "suppressed", Reason: string(reason)})
		return
	}
	slog.Debug("notifier: clicked", "window_id", windowID, "account", account, "title", title, "subtitle", subtitle)
	if !target.Navigable || windowID <= 0 {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "suppressed", Reason: "focus_target_missing"})
		return
	}
	n.mu.Lock()
	focus := n.focus
	n.mu.Unlock()
	if focus == nil {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "suppressed", Reason: "focus_unavailable"})
		return
	}
	if !kitty.IsReusableListenAddress(target.KittyListenOn) {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "suppressed", Reason: "focus_unavailable"})
		return
	}
	if err := n.focusForClick(focus, target); err != nil {
		if errors.Is(err, errFocusTargetStale) {
			n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "suppressed", Reason: "focus_target_stale"})
			return
		}
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "failed", Reason: "focus_failed"})
		return
	}
	n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: "focus", Outcome: "focused", Reason: "focus_success"})
}

// focusForClick brings the source of a clicked notification back to the
// foreground. The two-step focus pattern (FocusTab → FocusWindow) mirrors
// cmd/root.go:47-62. It returns errors so runDispatch can record a precise
// terminal focus disposition after the notification subprocess exits.

var errFocusTargetStale = errors.New("notification focus target is no longer live")

func (n *Notifier) focusForClick(focus FocusClient, target NotificationTarget) error {
	windowID := target.WindowID
	windows, err := focus.ListWindows(target.KittyListenOn)
	if err != nil {
		return err
	}
	if _, _, _, ok := kitty.FindWindowLocationByID(windows, windowID); !ok {
		return errFocusTargetStale
	}
	var errs []error
	if err := focus.FocusTab(target.KittyListenOn, kitty.MatchTabByWindowID(windowID)); err != nil {
		slog.Debug("notifier: focus tab failed", "err", err, "window_id", windowID)
		errs = append(errs, err)
	}
	if err := focus.FocusWindow(target.KittyListenOn, kitty.MatchWindowByID(windowID)); err != nil {
		slog.Debug("notifier: focus window failed", "err", err, "window_id", windowID)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	n.mu.Unlock()
	if cancelled {
		n.emitDisposition(disposition)
	}
}
