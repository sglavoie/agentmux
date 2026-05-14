package daemon

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

const cooldownDuration = 10 * time.Second

// notifyableActions lists the actions that may produce macOS notifications.
var notifyableActions = map[string]bool{
	"stop":       true,
	"permission": true,
	"failure":    true,
}

// notificationLabels maps action names to human-readable notification titles.
var notificationLabels = map[string]string{
	"stop":       "Claude stopped",
	"permission": "Permission requested",
	"failure":    "Tool failure",
}

// Notifier tracks per-window cooldowns and fires osascript notifications.
type Notifier struct {
	mu      sync.Mutex
	last    map[int]time.Time // last notification time per window_id
	cfg     *config.NotificationConfig
	now     func() time.Time // injectable for tests
	osascript func(title, msg string) error // injectable for tests
}

// NewNotifier creates a Notifier with the given notification config.
func NewNotifier(cfg *config.NotificationConfig) *Notifier {
	n := &Notifier{
		last: make(map[int]time.Time),
		cfg:  cfg,
		now:  time.Now,
	}
	n.osascript = n.runOsascript
	return n
}

// MaybeNotify fires a notification for the given window if:
//   - the action is notifyable (stop/permission/failure)
//   - the corresponding config flag is enabled
//   - the 10s cooldown has elapsed since the last notification for this window
//   - pipeMode is false (claude -p suppresses notifications)
func (n *Notifier) MaybeNotify(windowID int, account, action string, pipeMode bool) {
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

	title := notificationLabels[action]
	body := account
	if body == "" {
		body = "Claude Code"
	}
	// Fire asynchronously so the IPC response isn't delayed.
	go func() { _ = n.osascript(title, body) }()
}

func (n *Notifier) actionEnabled(action string) bool {
	switch action {
	case "stop":
		return n.cfg.Stop
	case "permission":
		return n.cfg.Permission
	case "failure":
		return n.cfg.Error
	}
	return false
}

func (n *Notifier) runOsascript(title, body string) error {
	script := fmt.Sprintf(
		`display notification %q with title %q`,
		body, title,
	)
	return exec.Command("osascript", "-e", script).Run()
}

// ResetCooldown clears the cooldown for windowID (for testing).
func (n *Notifier) ResetCooldown(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.last, windowID)
}
