package notification

// Notification backends and click-to-focus contract.
//
// macOS notifications require Alerter 26.x or newer. A pane-specific
// notification also carries the reusable Kitty socket that can focus its exact
// source after Alerter reports a click. Notifications are suppressed when
// either half of that interactive contract is unavailable.

import (
	"context"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// defaultCooldown is the per-(window, action) gap when the config does not set
// one — a config built in code rather than loaded, as tests do.
const defaultCooldown = 10 * time.Second

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

// IsNotifyableAction reports whether an action may produce a macOS
// notification at all. The daemon's shadow path consults it before it bothers
// building a notification target for an applied decision.
func IsNotifyableAction(action string) bool {
	return notifyableActions[action]
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

// FocusClient scopes every read/write to the notification's captured socket.
// The production adapter wraps kitty.SubprocessClient; tests record the socket
// directly without implementing unrelated Kitty operations.
type FocusClient interface {
	ListWindows(socket string) ([]kitty.OSWindow, error)
	FocusTab(socket, matchExpr string) error
	FocusWindow(socket, matchExpr string) error
}

type kittyNotificationFocus struct {
	client *kitty.SubprocessClient
}

// NewKittyFocus adapts the daemon's Kitty client to the focus contract a
// clicked notification needs. The adapter itself stays unexported: the daemon
// only ever hands the result straight to SetFocus.
func NewKittyFocus(client *kitty.SubprocessClient) FocusClient {
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
	cooldown        time.Duration
	quiet           config.QuietWindow
	quietSet        bool
	dispatchTimeout time.Duration
	cfg             *config.NotificationConfig
	now             func() time.Time // injectable for tests
	dispatch        dispatchFn       // nil when compatible Alerter is unavailable; injectable for tests
	capability      protocol.NotificationCapability
	focus           FocusClient     // optional; nil when the daemon has no Kitty client wired
	isMuted         func(int) bool  // optional; nil treats every window as unmuted (tests + pre-wire)
	ctx             context.Context // parent ctx for dispatch goroutines; defaults to Background until SetParentContext
}

// cooldownFor is the configured cooldown, or the default when it is unset.
func cooldownFor(cfg *config.NotificationConfig) time.Duration {
	if cfg.CooldownSeconds > 0 {
		return time.Duration(cfg.CooldownSeconds) * time.Second
	}
	return defaultCooldown
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	d := selectDispatcher(cfg)
	// Validation has already refused a malformed window, so a parse error here
	// can only come from a config built in code; it leaves quiet hours off.
	quiet, quietSet, _ := config.ParseQuietHours(cfg.QuietHours)
	return &Notifier{
		quiet:           quiet,
		quietSet:        quietSet,
		last:            make(map[notifyKey]time.Time),
		pending:         make(map[int]pendingNotification),
		debounce:        time.Duration(cfg.PermissionDebounceMs) * time.Millisecond,
		cooldown:        cooldownFor(cfg),
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
func (n *Notifier) Capability() protocol.NotificationCapability {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.capability
}

// QuietHoursSpec returns the configured quiet-hours window verbatim, for a
// companion to show read-only. The configuration is fixed at construction.
func (n *Notifier) QuietHoursSpec() string {
	if n.cfg == nil {
		return ""
	}
	return n.cfg.QuietHours
}

// SetCapability overrides the backend capability resolved at construction. It
// is the seam the daemon's health tests use to assert that an unavailable
// Alerter is reported without degrading readiness, which cannot be staged
// through NewNotifier because the real probe inspects the host.
func (n *Notifier) SetCapability(capability protocol.NotificationCapability) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.capability = capability
}

// SetFocus installs the Kitty client used to surface the originating
// tab/window when a notification is clicked. Wiring is optional — tests
// that omit it keep the focus path inert.
func (n *Notifier) SetFocus(f FocusClient) {
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

// mutedWindow reports whether the configured isMuted closure marks windowID as
// muted. Returns false when no closure is installed so tests and unwired
// pre-Run code paths behave identically to "not muted".
func (n *Notifier) mutedWindow(windowID int) bool {
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

// EmitDisposition reports one disposition to the installed observer. Callers
// that cancelled a pending notification under a lock of their own use it to
// publish that outcome once their lock is free.
func (n *Notifier) EmitDisposition(disposition NotificationDisposition) {
	n.emitDisposition(disposition)
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
