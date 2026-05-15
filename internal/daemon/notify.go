package daemon

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

const cooldownDuration = 10 * time.Second

// notifyableActions lists the actions that may produce macOS notifications.
var notifyableActions = map[string]bool{
	"stop":         true,
	"permission":   true,
	"failure":      true,
	"stop_failure": true,
}

// notificationLabels maps action names to human-readable notification titles.
var notificationLabels = map[string]string{
	"stop":         "Claude stopped",
	"permission":   "Permission requested",
	"failure":      "Tool failure",
	"stop_failure": "Claude stopped (failure)",
}

// Notifier tracks per-window cooldowns and fires macOS notifications via
// the selected backend (alerter when installed, osascript otherwise).
type Notifier struct {
	mu       sync.Mutex
	last     map[int]time.Time // last notification time per window_id
	cfg      *config.NotificationConfig
	now      func() time.Time              // injectable for tests
	dispatch func(title, subtitle string) error // injectable for tests
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	return &Notifier{
		last:     make(map[int]time.Time),
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
//   - pipeMode is false (claude -p suppresses notifications)
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
	n.last[windowID] = now

	title := sessionTitle
	if title == "" {
		title = "Claude Code"
	}
	subtitle := notificationLabels[action]
	go func() {
		if err := n.dispatch(title, subtitle); err != nil {
			slog.Warn("notifier: dispatch failed", "err", err, "title", title, "subtitle", subtitle)
		}
	}()
}

func (n *Notifier) actionEnabled(action string) bool {
	switch action {
	case "stop":
		return n.cfg.Stop
	case "permission":
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
