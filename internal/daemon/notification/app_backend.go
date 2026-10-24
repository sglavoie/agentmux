package notification

// The companion app as a notification backend.
//
// A connected, authorized companion — the native menu bar app — can show a
// notification through the operating system's own notification center, with
// click-to-focus and a few safe actions. It does not replace Alerter: the app
// may be quit, unauthorized, or wedged at any moment, so every dispatch decides
// afresh. When the app is ready it gets the notification first and has
// appDeliveredTimeout to confirm the operating system accepted it; anything
// short of that confirmation records app_fallback and runs the Alerter path
// exactly as if no app existed. Every gate in MaybeNotifyTracedTarget runs
// before either backend is chosen, so the app path suppresses exactly what the
// Alerter path suppresses.
//
// This package owns the decision and the dispositions. The transport — which
// subscriber holds the role, the notification and notification_retract frames,
// and matching the app's notification_disposition requests back to a pending
// delivery — is the daemon root's companion hub, seen here only as AppBackend.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// appDeliveredTimeout bounds how long a dispatch waits for the app to report
// delivered before it falls back to Alerter. A variable so tests can shorten it.
var appDeliveredTimeout = 3 * time.Second

// ErrAppBackendUnavailable is what Deliver returns when no subscriber holds the
// notification backend role.
var ErrAppBackendUnavailable = errors.New("no companion app holds the notification backend role")

// AppNotice is one notification as the companion receives it, and the payload
// of a notification frame. It carries what a banner shows and what the app
// needs to report back; it never carries the event that caused it.
type AppNotice struct {
	NotificationID string `json:"notification_id"`
	WindowID       int    `json:"window_id"`
	// KittySocket is the Kitty instance WindowID lives in, as the daemon
	// captured it for the target. Window IDs repeat across instances, so the
	// app needs both to know which Kitty to raise and which pane to mute. It
	// is empty when the notice is not navigable or no socket was recorded.
	KittySocket string `json:"kitty_socket,omitempty"`
	// Navigable says a click can show a session: a Kitty pane named by
	// KittySocket and WindowID, or the runtime session named below, which the
	// app asks for with focus_session. Profile-wide notices, such as hook
	// silence, are informational and have nothing to show.
	Navigable bool     `json:"navigable"`
	Title     string   `json:"title"`
	Subtitle  string   `json:"subtitle,omitempty"`
	Action    string   `json:"action"`
	RunID     string   `json:"run_id,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	Actions   []string `json:"actions,omitempty"`
	// RuntimeSessionID and Backend name the AgentMux runtime session the
	// notice is about, when one holds its slot. For a session whose backend is
	// not Kitty they are the notice's only target: WindowID is zero, because
	// the daemon's slot for such a session is not a window any Kitty has.
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	Backend          string `json:"backend,omitempty"`
}

// BackendKindKitty is the NotificationTarget.Backend of a Kitty session, the
// only backend whose slot is a window ID an app can hand back to Kitty.
const BackendKindKitty = "kitty"

// AppInteraction is one report about a delivered notice. Disposition is a
// protocol.Disposition* value, or one of the two the backend synthesizes
// itself: AppRetracted when the daemon withdrew the notice, and AppGone when
// the subscriber holding it disconnected.
type AppInteraction struct {
	Disposition string
	Error       string
}

// Dispositions the backend synthesizes rather than receives from the app.
const (
	AppRetracted = "retracted"
	AppGone      = string(interactionUnavailable)
)

// AppBackend is the companion seen from the notifier.
//
// Deliver hands a notice to the subscriber holding the role and returns the
// channel its interactions arrive on: at most one protocol.DispositionDelivered
// followed by at most one terminal value. Nothing more is sent once a terminal
// value has been, and the channel is never closed, so a reader also selects on
// ctx. Cancelling ctx forgets the delivery; a late report for it is ignored.
//
// Retract withdraws a notice that has not reached a terminal value: the app is
// told to remove it, and the reader receives AppRetracted. Retracting an
// unknown or settled notice does nothing. Neither Ready nor Retract blocks,
// and Retract is safe to call with any daemon lock held.
type AppBackend interface {
	Ready() bool
	Deliver(ctx context.Context, notice AppNotice) (<-chan AppInteraction, error)
	Retract(notificationID, reason string)
}

// SetAppBackend installs the companion backend. A nil backend leaves Alerter
// as the only path, which is also the state before the daemon wires one.
func (n *Notifier) SetAppBackend(app AppBackend) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.app = app
}

// appReady reports whether a companion can take the next notification.
func (n *Notifier) appReady() bool {
	n.mu.Lock()
	app := n.app
	n.mu.Unlock()
	return app != nil && app.Ready()
}

// newNotificationID names one notice. It is random rather than a counter so a
// disposition that outlives a daemon restart cannot settle a different notice
// that happens to reuse its number.
func newNotificationID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "n-" + hex.EncodeToString(b[:])
}

// BackendKindTmux is the NotificationTarget.Backend of a session on the
// private tmux server, the only backend `amx send` writes to.
const BackendKindTmux = "tmux"

// appActions is what a notice offers besides its click. A permission prompt
// offers no answer: the operator must look at the pane before answering it.
// What it can offer is a way to look and answer without going there, for a
// session `amx send` can write to. Muting needs a session to mute: a Kitty
// window, or a runtime session the app mutes with `mute-toggle
// --runtime-session`. Answering needs an ASL run with a question.
func appActions(target NotificationTarget, action string, trace NotificationTrace) []string {
	if action == "permission" {
		if target.RuntimeSessionID != "" && target.Backend == BackendKindTmux {
			return []string{protocol.NotificationActionRespond}
		}
		return nil
	}
	var actions []string
	if target.Navigable && (target.WindowID > 0 || target.sessionFocus()) {
		actions = append(actions, protocol.NotificationActionMute)
	}
	actions = append(actions, protocol.NotificationActionSnooze)
	if action == "elicitation" && trace.RunID != "" {
		actions = append(actions, protocol.NotificationActionAnswer)
	}
	return actions
}

// trackAppNotice remembers which window an outstanding notice belongs to, so
// a later event for that window can retract it.
func (n *Notifier) trackAppNotice(id string, windowID int) {
	n.mu.Lock()
	n.appNotices[id] = windowID
	n.mu.Unlock()
}

func (n *Notifier) untrackAppNotice(id string) {
	n.mu.Lock()
	delete(n.appNotices, id)
	n.mu.Unlock()
}

// takeAppNoticesLocked removes and returns every outstanding notice for
// windowID. The caller retracts them once n.mu is released.
func (n *Notifier) takeAppNoticesLocked(windowID int) (AppBackend, []string) {
	var ids []string
	for id, owner := range n.appNotices {
		if owner == windowID {
			ids = append(ids, id)
			delete(n.appNotices, id)
		}
	}
	return n.app, ids
}

func retractAll(app AppBackend, ids []string, reason string) {
	if app == nil {
		return
	}
	for _, id := range ids {
		app.Retract(id, reason)
	}
}

// dispatchToApp offers one notification to the companion. It returns false
// when the caller should fall back to Alerter: the app refused the notice,
// reported it failed, went away, or did not confirm delivery in time. Once it
// returns true every disposition for the notification has been recorded.
func (n *Notifier) dispatchToApp(parent context.Context, app AppBackend, target NotificationTarget, title, subtitle, action string, trace NotificationTrace) bool {
	windowID := target.WindowID
	emit := func(stage, outcome, reason string) {
		n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: action, Stage: stage, Outcome: outcome, Reason: reason, Backend: BackendApp})
	}
	ctx, cancel := context.WithTimeout(parent, n.dispatchTimeout)
	defer cancel()
	navigable := target.Navigable && windowID > 0
	notice := AppNotice{
		NotificationID:   newNotificationID(),
		WindowID:         target.focusWindowID(),
		Navigable:        navigable,
		Title:            title,
		Subtitle:         subtitle,
		Action:           action,
		RunID:            trace.RunID,
		SessionID:        trace.SessionID,
		Actions:          appActions(target, action, trace),
		RuntimeSessionID: target.RuntimeSessionID,
		Backend:          target.Backend,
	}
	if target.Backend != "" && target.Backend != BackendKindKitty {
		notice.WindowID = 0
	}
	if navigable {
		notice.KittySocket = target.KittyListenOn
	}
	interactions, err := app.Deliver(ctx, notice)
	if err != nil {
		slog.Debug("notifier: app backend refused notice", "err", err, "window_id", windowID)
		return false
	}
	id := notice.NotificationID
	n.trackAppNotice(id, windowID)
	defer n.untrackAppNotice(id)

	ack := time.NewTimer(appDeliveredTimeout)
	defer ack.Stop()
	select {
	case in := <-interactions:
		switch in.Disposition {
		case protocol.DispositionDelivered:
		case AppRetracted:
			// Something newer withdrew the notice before the app confirmed it.
			// Falling back would show Alerter a notice the daemon no longer wants.
			emit("dispatch", "cancelled", "retracted")
			return true
		default:
			slog.Debug("notifier: app backend did not deliver", "disposition", in.Disposition, "error", in.Error, "window_id", windowID)
			return false
		}
	case <-ack.C:
		// A late banner would duplicate the Alerter one about to be shown.
		app.Retract(id, "unconfirmed")
		return false
	case <-ctx.Done():
		if parent.Err() != nil {
			emit("dispatch", "cancelled", "daemon_shutdown")
			return true
		}
		return false
	}
	emit("dispatch", "sent", "send_success")

	select {
	case in := <-interactions:
		n.settleInteraction(target, action, trace, interactionOutcome(in.Disposition), BackendApp)
	case <-ctx.Done():
		if parent.Err() != nil {
			emit("focus", "cancelled", "daemon_shutdown")
			return true
		}
		app.Retract(id, string(interactionTimeout))
		n.settleInteraction(target, action, trace, interactionTimeout, BackendApp)
	}
	return true
}

// TestAppDelivery sends one test notice to the companion and reports whether
// it confirmed delivery within appDeliveredTimeout. It bypasses every gate,
// because asking for a test is explicit consent to one notification, and it
// never falls back: the caller decides whether to test Alerter instead.
func (n *Notifier) TestAppDelivery(ctx context.Context) protocol.NotificationTestResult {
	n.mu.Lock()
	app := n.app
	n.mu.Unlock()
	if app == nil || !app.Ready() {
		return protocol.NotificationTestResult{Reason: "app_unavailable"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	notice := AppNotice{
		NotificationID: newNotificationID(),
		Title:          "Agent Mux notification test",
		Subtitle:       "Notification test",
		Action:         "test",
	}
	interactions, err := app.Deliver(ctx, notice)
	if err != nil {
		return protocol.NotificationTestResult{Reason: "app_unavailable"}
	}
	ack := time.NewTimer(appDeliveredTimeout)
	defer ack.Stop()
	select {
	case in := <-interactions:
		if in.Disposition == protocol.DispositionDelivered {
			return protocol.NotificationTestResult{Backend: BackendApp, Delivered: true}
		}
		return protocol.NotificationTestResult{Reason: "app_" + in.Disposition}
	case <-ack.C:
		app.Retract(notice.NotificationID, "unconfirmed")
		return protocol.NotificationTestResult{Reason: "app_unconfirmed"}
	case <-ctx.Done():
		return protocol.NotificationTestResult{Reason: "cancelled"}
	}
}
