package daemon

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
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const cooldownDuration = 10 * time.Second

// notifyableActions lists the actions that may produce macOS notifications.
var notifyableActions = map[string]bool{
	"stop":         true,
	"permission":   true,
	"failure":      true,
	"stop_failure": true,
	"elicitation":  true,
	"stale":        true,
	"hook_silence": true,
}

// notificationLabels maps action names to human-readable notification titles.
var notificationLabels = map[string]string{
	"stop":         "Session stopped",
	"permission":   "Permission requested",
	"failure":      "Tool failure",
	"stop_failure": "Session stopped (failure)",
	"elicitation":  "User input requested",
	"stale":        "Session idle",
	"hook_silence": "Hook silence detected",
}

// notifyKey scopes the cooldown to a single (window, action) pair so a
// recent stop/failure notification cannot silently suppress a subsequent
// permission notification (and vice versa).
type notifyKey struct {
	windowID int
	action   string
}

// NotificationTrace carries sanitized correlation identity into asynchronous
// debounce, dispatch, and click-focus work.
type NotificationTrace struct {
	TraceID       string
	RequestID     string
	SourceEventID string
	Harness       string
	Profile       string
	SessionID     string
	RunID         string
}

// NotificationTarget identifies the only Kitty pane a click may focus.
// Navigable is false for profile-wide informational notifications such as
// hook-silence alerts, which intentionally have no arbitrary pane fallback.
type NotificationTarget struct {
	WindowID      int
	Account       string
	KittyListenOn string
	Navigable     bool
}

// NotificationDisposition is one explicit notification decision or terminal
// asynchronous outcome. It intentionally excludes notification body content.
type NotificationDisposition struct {
	Trace      NotificationTrace
	WindowID   int
	Action     string
	Stage      string
	Outcome    string
	Reason     string
	ObservedAt time.Time
}

// focusClient scopes every read/write to the notification's captured socket.
// The production adapter wraps kitty.SubprocessClient; tests record the socket
// directly without implementing unrelated Kitty operations.
type focusClient interface {
	ListWindows(socket string) ([]kitty.OSWindow, error)
	FocusTab(socket, matchExpr string) error
	FocusWindow(socket, matchExpr string) error
}

type kittyNotificationFocus struct {
	client *kitty.SubprocessClient
}

func newKittyNotificationFocus(client *kitty.SubprocessClient) *kittyNotificationFocus {
	return &kittyNotificationFocus{client: client}
}

func (f *kittyNotificationFocus) scoped(socket string) *kitty.SubprocessClient {
	return f.client.WithSocket(socket)
}

func (f *kittyNotificationFocus) ListWindows(socket string) ([]kitty.OSWindow, error) {
	return f.scoped(socket).ListWindows()
}

func (f *kittyNotificationFocus) FocusTab(socket, matchExpr string) error {
	return f.scoped(socket).FocusTab(matchExpr)
}

func (f *kittyNotificationFocus) FocusWindow(socket, matchExpr string) error {
	return f.scoped(socket).FocusWindow(matchExpr)
}

// Notifier tracks per-window cooldowns and fires macOS notifications via
// the selected Alerter backend when its startup capability probe succeeds.
type Notifier struct {
	mu              sync.Mutex
	observerMu      sync.RWMutex
	observer        func(NotificationDisposition)
	last            map[notifyKey]time.Time // last dispatched notification time per (window_id, action)
	pending         map[int]pendingNotification
	nextPendingSeq  uint64
	debounce        time.Duration
	dispatchTimeout time.Duration
	cfg             *config.NotificationConfig
	now             func() time.Time // injectable for tests
	dispatch        dispatchFn       // nil when compatible Alerter is unavailable; injectable for tests
	capability      NotificationCapability
	focus           focusClient     // optional; nil when the daemon has no Kitty client wired
	isMuted         func(int) bool  // optional; nil treats every window as unmuted (tests + pre-wire)
	ctx             context.Context // parent ctx for dispatch goroutines; defaults to Background until SetParentContext
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	d := selectDispatcher(cfg)
	return &Notifier{
		last:            make(map[notifyKey]time.Time),
		pending:         make(map[int]pendingNotification),
		debounce:        time.Duration(cfg.PermissionDebounceMs) * time.Millisecond,
		dispatchTimeout: d.timeout,
		cfg:             cfg,
		now:             time.Now,
		dispatch:        d.fn,
		capability:      d.capability,
		ctx:             context.Background(),
	}
}

// Capability returns the immutable backend capability selected at daemon
// startup. The value is copied so callers cannot mutate notifier state.
func (n *Notifier) Capability() NotificationCapability {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.capability
}

// SetFocus installs the Kitty client used to surface the originating
// tab/window when a notification is clicked. Wiring is optional — tests
// that omit it keep the focus path inert.
func (n *Notifier) SetFocus(f focusClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.focus = f
}

// SetIsMuted installs the closure MaybeNotify consults to suppress
// notifications for muted windows. The closure is typically Handler.IsMuted,
// which reads the daemon's muted-window cache populated from Kitty snapshots.
// A nil closure (or one that returns false) leaves the gate open.
func (n *Notifier) SetIsMuted(fn func(int) bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.isMuted = fn
}

// IsMuted reports whether the configured isMuted closure marks windowID as
// muted. Returns false when no closure is installed so tests and unwired
// pre-Run code paths behave identically to "not muted".
func (n *Notifier) IsMuted(windowID int) bool {
	n.mu.Lock()
	fn := n.isMuted
	n.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn(windowID)
}

// SetParentContext installs the daemon's lifecycle context as the parent of
// every dispatch's bounded context. Once wired, cancelling the daemon ctx
// terminates any in-flight alerter subprocess immediately rather than waiting
// for the per-dispatch timeout (up to alerterDispatchTimeout, i.e. 6m).
// Defaults to context.Background() so tests and pre-wiring call sites keep
// working without explicit setup.
func (n *Notifier) SetParentContext(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ctx = ctx
}

// SetObserver installs a fail-open disposition observer. Handler uses it to
// update schema-v9+ event links and persist durable diagnostic journal rows.
func (n *Notifier) SetObserver(observer func(NotificationDisposition)) {
	n.observerMu.Lock()
	n.observer = observer
	n.observerMu.Unlock()
}

func (n *Notifier) emitDisposition(disposition NotificationDisposition) {
	disposition.ObservedAt = time.Now().UTC()
	n.observerMu.RLock()
	observer := n.observer
	n.observerMu.RUnlock()
	if observer != nil {
		observer(disposition)
	}
}

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
	trace.Profile = firstNonEmpty(trace.Profile, account)
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
	if n.IsMuted(windowID) {
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
	if target.Navigable && focusAvailable && !isReusableKittySocket(target.KittyListenOn) {
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
		if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
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

	if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
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
	if !isReusableKittySocket(target.KittyListenOn) {
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

func (n *Notifier) focusForClick(focus focusClient, target NotificationTarget) error {
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
		return n.cfg.Permission
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
