package daemon

// Notification backends and click-to-focus contract.
//
// macOS notifications dispatch via one of two backends, selected once at
// Notifier construction: `alerter` (preferred when installed) and `osascript`
// (fallback). Only the alerter backend is interactive — when the user clicks
// the notification body, alerter prints @CONTENTCLICKED / @ACTIONCLICKED on
// stdout and the Notifier focuses the originating Kitty tab/window via the
// focusClient installed by SetFocus. osascript's `display notification` is
// fire-and-forget and cannot relay interaction, so its notifications never
// trigger a focus action.

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const cooldownDuration = 10 * time.Second

// notifyDispatchTimeout caps how long a single osascript dispatch can run.
// macOS notification backends can block under user-mode pressure (e.g.
// Notification Center unresponsive, UI server restart), so the goroutine
// wrapping every dispatch must enforce its own deadline rather than trusting
// the external process to return promptly. osascript returns as soon as the
// notification is posted; alerter blocks waiting for user interaction and uses
// the longer alerterDispatchTimeout instead.
const notifyDispatchTimeout = 5 * time.Second

// alerterDispatchTimeout bounds how long a single alerter invocation may
// wait for user interaction. alerter exits when the user clicks, dismisses,
// or times out; this cap is an upper bound for the worst case where the user
// never interacts and Notification Center swallows the alert. The
// exec.CommandContext underlying runAlerter still terminates the subprocess
// promptly on daemon shutdown via context cancellation.
const alerterDispatchTimeout = 5 * time.Minute

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

type pendingNotification struct {
	timer *time.Timer
	seq   uint64
}

// notifyKey scopes the cooldown to a single (window, action) pair so a
// recent stop/failure notification cannot silently suppress a subsequent
// permission notification (and vice versa).
type notifyKey struct {
	windowID int
	action   string
}

// dispatchFn is the signature each backend implements. The bool reports
// whether the user clicked the notification body (alerter only); osascript
// always returns false because its notifications cannot relay interaction.
// windowID and account identify the focus target for click handling: a
// positive windowID maps to a real Kitty window; a non-empty account is
// used when windowID is synthetic (e.g. hook-silence sweeps). Backends
// today ignore both — Session 3 wires them to a focus closure.
type dispatchFn func(ctx context.Context, windowID int, account, title, subtitle string) (bool, error)

// dispatcher bundles a backend's dispatch function with the timeout the
// runDispatch goroutine should use. Bundling avoids a mismatch where the
// alerter backend is selected but runs under the short osascript timeout.
type dispatcher struct {
	fn      dispatchFn
	timeout time.Duration
}

// focusClient is the narrow subset of the Kitty client that the Notifier
// uses to surface the originating tab/window after a notification click.
// kitty.SubprocessClient satisfies this interface implicitly. Declaring it
// here rather than depending on the full KittyClient interface keeps the
// test fake minimal (no need to stub every Kitty operation).
type focusClient interface {
	FocusTab(matchExpr string) error
	FocusWindow(matchExpr string) error
}

// Notifier tracks per-window cooldowns and fires macOS notifications via
// the selected backend (alerter when installed, osascript otherwise).
type Notifier struct {
	mu              sync.Mutex
	last            map[notifyKey]time.Time // last dispatched notification time per (window_id, action)
	pending         map[int]pendingNotification
	nextPendingSeq  uint64
	debounce        time.Duration
	dispatchTimeout time.Duration
	cfg             *config.NotificationConfig
	now             func() time.Time // injectable for tests
	dispatch        dispatchFn       // injectable for tests
	focus           focusClient      // optional; nil when the daemon has no Kitty client wired
	ctx             context.Context  // parent ctx for dispatch goroutines; defaults to Background until SetParentContext
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	d := selectDispatcher()
	return &Notifier{
		last:            make(map[notifyKey]time.Time),
		pending:         make(map[int]pendingNotification),
		debounce:        time.Duration(cfg.PermissionDebounceMs) * time.Millisecond,
		dispatchTimeout: d.timeout,
		cfg:             cfg,
		now:             time.Now,
		dispatch:        d.fn,
		ctx:             context.Background(),
	}
}

// SetFocus installs the Kitty client used to surface the originating
// tab/window when a notification is clicked. Wiring is optional — tests
// that omit it keep the focus path inert.
func (n *Notifier) SetFocus(f focusClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.focus = f
}

// SetParentContext installs the daemon's lifecycle context as the parent of
// every dispatch's bounded context. Once wired, cancelling the daemon ctx
// terminates any in-flight alerter subprocess immediately rather than waiting
// for the per-dispatch timeout (up to alerterDispatchTimeout, i.e. 5 minutes).
// Defaults to context.Background() so tests and pre-wiring call sites keep
// working without explicit setup.
func (n *Notifier) SetParentContext(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ctx = ctx
}

// selectDispatcher picks the macOS notification backend once at construction.
// alerter is preferred when available; otherwise osascript is used.
func selectDispatcher() dispatcher {
	if _, err := exec.LookPath("alerter"); err == nil {
		slog.Info("notifier: using alerter backend")
		return dispatcher{fn: runAlerter, timeout: alerterDispatchTimeout}
	}
	slog.Info("notifier: using osascript backend (alerter not found in PATH)")
	return dispatcher{fn: runOsascript, timeout: notifyDispatchTimeout}
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
// account threads the originating profile through to the dispatcher so a
// click on the notification can be resolved to the right Kitty window even
// when windowID is synthetic (e.g. the hook-silence sweep uses
// hookSilenceWindowID(account) instead of a real window). It may be empty
// when the caller does not know the account; click-target resolution
// silently no-ops in that case.
func (n *Notifier) MaybeNotify(windowID int, sessionTitle, account, action string, pipeMode bool) {
	if pipeMode {
		return
	}
	if !notifyableActions[action] {
		return
	}
	if !n.actionEnabled(action) {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	title := sessionTitle
	if title == "" {
		title = "Agent Mux"
	}
	subtitle := notificationLabels[action]

	if action != "permission" {
		n.cancelPendingLocked(windowID)
	}

	key := notifyKey{windowID: windowID, action: action}
	now := n.now()
	if action == "permission" && n.debounce > 0 {
		if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
			return
		}
		n.schedulePermissionLocked(windowID, account, title, subtitle)
		return
	}

	if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
		return
	}
	n.last[key] = now
	go n.runDispatch(windowID, account, title, subtitle)
}

// runDispatch invokes the configured dispatcher under a bounded context. The
// dispatcher's underlying process (or test stub) is responsible for honoring
// ctx.Done — the real osascript/alerter backends do via exec.CommandContext.
// windowID and account are forwarded so Session 3's focus closure can map
// a clicked notification back to the right Kitty window.
func (n *Notifier) runDispatch(windowID int, account, title, subtitle string) {
	n.mu.Lock()
	parent := n.ctx
	n.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, n.dispatchTimeout)
	defer cancel()
	clicked, err := n.dispatch(ctx, windowID, account, title, subtitle)
	if err != nil {
		slog.Warn("notifier: dispatch failed", "err", err, "title", title, "subtitle", subtitle)
		return
	}
	if !clicked {
		return
	}
	slog.Debug("notifier: clicked", "window_id", windowID, "account", account, "title", title, "subtitle", subtitle)
	n.mu.Lock()
	focus := n.focus
	n.mu.Unlock()
	if focus == nil {
		return
	}
	n.focusForClick(focus, windowID, account)
}

// focusForClick brings the source of a clicked notification back to the
// foreground. The two-step focus pattern (FocusTab → FocusWindow) mirrors
// cmd/root.go:47-62. Errors are logged at debug and swallowed: the
// notification subprocess has already returned, the window may have been
// closed in the meantime, and propagating the error would only spam logs.
func (n *Notifier) focusForClick(focus focusClient, windowID int, account string) {
	switch {
	case windowID > 0:
		if err := focus.FocusTab(kitty.MatchTabByWindowID(windowID)); err != nil {
			slog.Debug("notifier: focus tab failed", "err", err, "window_id", windowID)
		}
		if err := focus.FocusWindow(kitty.MatchWindowByID(windowID)); err != nil {
			slog.Debug("notifier: focus window failed", "err", err, "window_id", windowID)
		}
	case account != "":
		if err := focus.FocusWindow(kitty.MatchAccountWindows(account)); err != nil {
			slog.Debug("notifier: focus account window failed", "err", err, "account", account)
		}
	default:
		slog.Debug("notifier: click had no focus target", "window_id", windowID)
	}
}

func (n *Notifier) schedulePermissionLocked(windowID int, account, title, subtitle string) {
	n.cancelPendingLocked(windowID)
	n.nextPendingSeq++
	seq := n.nextPendingSeq
	entry := pendingNotification{seq: seq}
	entry.timer = time.AfterFunc(n.debounce, func() {
		n.mu.Lock()
		pending, ok := n.pending[windowID]
		if !ok || pending.seq != seq {
			n.mu.Unlock()
			return
		}
		delete(n.pending, windowID)

		key := notifyKey{windowID: windowID, action: "permission"}
		now := n.now()
		if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
			n.mu.Unlock()
			return
		}
		n.last[key] = now
		n.mu.Unlock()

		n.runDispatch(windowID, account, title, subtitle)
	})
	n.pending[windowID] = entry
}

func (n *Notifier) cancelPendingLocked(windowID int) {
	if pending, ok := n.pending[windowID]; ok {
		pending.timer.Stop()
		delete(n.pending, windowID)
	}
}

// CancelPending cancels any pending permission notification for windowID.
func (n *Notifier) CancelPending(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cancelPendingLocked(windowID)
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

func runOsascript(ctx context.Context, _ int, _ string, title, subtitle string) (bool, error) {
	script := fmt.Sprintf(
		`display notification "" with title %s subtitle %s`,
		quoteAppleScript(title), quoteAppleScript(subtitle),
	)
	return false, exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// quoteAppleScript wraps s in a double-quoted AppleScript string literal.
// AppleScript only recognizes \", \\, \n, \r, and \t as string escapes; Go's
// %q can emit \uXXXX / \xNN forms (e.g. for U+2028) that AppleScript treats
// as a syntax error, dropping the notification silently. This routine
// escapes only the characters AppleScript understands and passes every other
// rune through verbatim — including any non-ASCII printable characters
// commonly found in session titles.
func quoteAppleScript(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func alerterArgs(title, subtitle string) []string {
	return []string{"--title", title, "--subtitle", subtitle, "--message", ""}
}

func runAlerter(ctx context.Context, _ int, _ string, title, subtitle string) (bool, error) {
	out, err := exec.CommandContext(ctx, "alerter", alerterArgs(title, subtitle)...).Output()
	if err != nil {
		return false, err
	}
	return parseAlerterOutput(out), nil
}

// parseAlerterOutput reports whether the user clicked the notification body.
// alerter prints exactly one of @CONTENTCLICKED, @ACTIONCLICKED, @CLOSED,
// @TIMEOUT, @REPLIED, or @CONTENTCLOSED on stdout when it exits. Only the
// two "click" outcomes count as an explicit request to focus; everything
// else (dismissal, timeout, reply, empty/garbled output) is treated as no
// click so the caller does not steal focus on a passive dismissal.
func parseAlerterOutput(stdout []byte) bool {
	line := strings.TrimSpace(string(stdout))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	switch line {
	case "@CONTENTCLICKED", "@ACTIONCLICKED":
		return true
	default:
		return false
	}
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
	defer n.mu.Unlock()
	for k := range n.last {
		if k.windowID == windowID {
			delete(n.last, k)
		}
	}
	n.cancelPendingLocked(windowID)
}
