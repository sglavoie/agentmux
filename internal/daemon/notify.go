package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

const cooldownDuration = 10 * time.Second

// notifyDispatchTimeout caps how long a single notification dispatch can run.
// macOS notification backends (osascript, alerter) can block under user-mode
// pressure (e.g. Notification Center unresponsive, UI server restart), so the
// goroutine wrapping every dispatch must enforce its own deadline rather than
// trusting the external process to return promptly.
const notifyDispatchTimeout = 5 * time.Second

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
	"elicitation":  "Claude is asking",
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
	now             func() time.Time                                        // injectable for tests
	dispatch        func(ctx context.Context, title, subtitle string) error // injectable for tests
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	return &Notifier{
		last:            make(map[notifyKey]time.Time),
		pending:         make(map[int]pendingNotification),
		debounce:        time.Duration(cfg.PermissionDebounceMs) * time.Millisecond,
		dispatchTimeout: notifyDispatchTimeout,
		cfg:             cfg,
		now:             time.Now,
		dispatch:        selectDispatcher(),
	}
}

// selectDispatcher picks the macOS notification backend once at construction.
// alerter is preferred when available; otherwise osascript is used.
func selectDispatcher() func(ctx context.Context, title, subtitle string) error {
	if _, err := exec.LookPath("alerter"); err == nil {
		slog.Info("notifier: using alerter backend")
		return runAlerter
	}
	slog.Info("notifier: using osascript backend (alerter not found in PATH)")
	return runOsascript
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
func (n *Notifier) MaybeNotify(windowID int, sessionTitle, action string, pipeMode bool) {
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
		n.schedulePermissionLocked(windowID, title, subtitle)
		return
	}

	if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
		return
	}
	n.last[key] = now
	go n.runDispatch(title, subtitle)
}

// runDispatch invokes the configured dispatcher under a bounded context. The
// dispatcher's underlying process (or test stub) is responsible for honoring
// ctx.Done — the real osascript/alerter backends do via exec.CommandContext.
func (n *Notifier) runDispatch(title, subtitle string) {
	ctx, cancel := context.WithTimeout(context.Background(), n.dispatchTimeout)
	defer cancel()
	if err := n.dispatch(ctx, title, subtitle); err != nil {
		slog.Warn("notifier: dispatch failed", "err", err, "title", title, "subtitle", subtitle)
	}
}

func (n *Notifier) schedulePermissionLocked(windowID int, title, subtitle string) {
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

		n.runDispatch(title, subtitle)
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

func runOsascript(ctx context.Context, title, subtitle string) error {
	script := fmt.Sprintf(
		`display notification "" with title %s subtitle %s`,
		quoteAppleScript(title), quoteAppleScript(subtitle),
	)
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
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

func runAlerter(ctx context.Context, title, subtitle string) error {
	return exec.CommandContext(ctx, "alerter", alerterArgs(title, subtitle)...).Run()
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
