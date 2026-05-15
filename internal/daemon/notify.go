package daemon

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

const cooldownDuration = 10 * time.Second

// notifyableActions lists the actions that may produce macOS notifications.
var notifyableActions = map[string]bool{
	"stop":         true,
	"permission":   true,
	"failure":      true,
	"stop_failure": true,
	"elicitation":  true,
}

// notificationLabels maps action names to human-readable notification titles.
var notificationLabels = map[string]string{
	"stop":         "Session stopped",
	"permission":   "Permission requested",
	"failure":      "Tool failure",
	"stop_failure": "Session stopped (failure)",
	"elicitation":  "Claude is asking",
}

// Notifier tracks per-window cooldowns and fires macOS notifications via
// the selected backend (alerter when installed, osascript otherwise).
type Notifier struct {
	mu       sync.Mutex
	last     map[int]time.Time   // last notification time per window_id
	pending  map[int]*time.Timer // pending permission timers per window_id
	debounce time.Duration
	cfg      *config.NotificationConfig
	now      func() time.Time               // injectable for tests
	dispatch func(title, subtitle string) error // injectable for tests
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	return &Notifier{
		last:     make(map[int]time.Time),
		pending:  make(map[int]*time.Timer),
		debounce: time.Duration(cfg.PermissionDebounceMs) * time.Millisecond,
		cfg:      cfg,
		now:      time.Now,
		dispatch: selectDispatcher(),
	}
}

// selectDispatcher picks the macOS notification backend once at construction.
// alerter is preferred when available; otherwise osascript is used.
func selectDispatcher() func(title, subtitle string) error {
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
// cancelled if CancelPending is called before the window expires. This avoids
// spurious notifications when a harness auto-approves the request.
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

	now := n.now()
	if last, ok := n.last[windowID]; ok && now.Sub(last) < cooldownDuration {
		return
	}

	title := sessionTitle
	if title == "" {
		title = "Agent Mux"
	}
	subtitle := notificationLabels[action]

	if action == "permission" && n.debounce > 0 {
		// Cancel any existing pending timer for this window.
		if t, ok := n.pending[windowID]; ok {
			t.Stop()
		}
		// Lock in the cooldown now to prevent duplicate scheduling during the window.
		n.last[windowID] = now
		n.pending[windowID] = time.AfterFunc(n.debounce, func() {
			n.mu.Lock()
			delete(n.pending, windowID)
			n.mu.Unlock()
			if err := n.dispatch(title, subtitle); err != nil {
				slog.Warn("notifier: dispatch failed", "err", err, "title", title, "subtitle", subtitle)
			}
		})
		return
	}

	n.last[windowID] = now
	go func() {
		if err := n.dispatch(title, subtitle); err != nil {
			slog.Warn("notifier: dispatch failed", "err", err, "title", title, "subtitle", subtitle)
		}
	}()
}

// CancelPending cancels any pending permission notification for windowID.
// The cooldown is reset so the next real permission notification is not suppressed.
func (n *Notifier) CancelPending(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.pending[windowID]; ok {
		t.Stop()
		delete(n.pending, windowID)
		delete(n.last, windowID)
	}
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
		return n.cfg.StopFailure
	}
	return false
}

func runOsascript(title, subtitle string) error {
	script := fmt.Sprintf(
		`display notification "" with title %q subtitle %q`,
		title, subtitle,
	)
	return exec.Command("osascript", "-e", script).Run()
}

func alerterArgs(title, subtitle string) []string {
	return []string{"--title", title, "--subtitle", subtitle, "--message", ""}
}

func runAlerter(title, subtitle string) error {
	return exec.Command("alerter", alerterArgs(title, subtitle)...).Run()
}

// ResetCooldown clears the cooldown for windowID (for testing).
func (n *Notifier) ResetCooldown(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.last, windowID)
}
