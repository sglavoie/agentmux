package notification

// Strict interactive macOS notifications via Alerter 26.x or newer.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// alerterProtocolTimeout is sent to alerter so a notification that receives
// no interaction still exits successfully with @TIMEOUT. The process context
// gets a bounded grace period beyond that protocol timeout. Notification
// Center can delay delivery enough that alerter's own countdown starts several
// seconds after process launch, so a one-minute grace avoids racing the
// backend's successful @TIMEOUT token while still bounding a genuinely stuck
// process.
const (
	alerterProtocolTimeout  = 5 * time.Minute
	alerterTimeoutGrace     = time.Minute
	alerterDispatchTimeout  = alerterProtocolTimeout + alerterTimeoutGrace
	alerterProbeTimeout     = 500 * time.Millisecond
	notificationTestTimeout = 5 * time.Second
	notificationTestGrace   = 5 * time.Second
	alerterBundleIdentifier = "fr.vjeantet.alerter"
)

const minimumAlerterMajor = 26

// ErrNotificationDeliveryUnconfirmed means Alerter did not acknowledge the
// test notification before the bounded probe expired. The most common cause is
// disabled Alerter notifications, though a stuck Notification Center can produce
// the same result.
var ErrNotificationDeliveryUnconfirmed = errors.New("notification delivery could not be confirmed")

// NotificationDeliveryTestResult describes a successful visible delivery
// probe. Interaction is Alerter's terminal result (for example, "clicked" or
// "interaction_timeout").
type NotificationDeliveryTestResult struct {
	Capability  protocol.NotificationCapability
	Interaction string
}

type deliveryOutcome string

const deliveryDelivered deliveryOutcome = "delivered"

type interactionOutcome string

const (
	interactionClicked     interactionOutcome = "clicked"
	interactionTimeout     interactionOutcome = "interaction_timeout"
	interactionDismissed   interactionOutcome = "dismissed"
	interactionReplied     interactionOutcome = "replied"
	interactionUnavailable interactionOutcome = "interaction_unavailable"
	interactionUnknown     interactionOutcome = "interaction_unknown"

	// The companion app's safe actions and the daemon's own retraction. They
	// end a notification without a click, the way replied does.
	interactionActionMute    interactionOutcome = protocol.DispositionActionMute
	interactionActionSnooze  interactionOutcome = protocol.DispositionActionSnooze
	interactionActionAnswer  interactionOutcome = protocol.DispositionActionAnswer
	interactionActionRespond interactionOutcome = protocol.DispositionActionRespond
	interactionRetracted     interactionOutcome = AppRetracted
)

// dispatchResult keeps successful delivery separate from the user's terminal
// interaction. Backends return an error when delivery itself fails; a
// successful result always records deliveryDelivered and an explicit
// interaction value.
type dispatchResult struct {
	delivery    deliveryOutcome
	interaction interactionOutcome
}

func deliveredResult(interaction interactionOutcome) dispatchResult {
	return dispatchResult{delivery: deliveryDelivered, interaction: interaction}
}

// dispatchFn is the signature each backend implements. The result reports
// both successful delivery and the terminal interaction (when available).
// windowID and account preserve notification metadata for injectable test
// dispatchers. The Alerter backend ignores them; click focus uses the
// socket-aware NotificationTarget retained by Notifier.runDispatch.
type dispatchFn func(ctx context.Context, windowID int, account, title, subtitle string) (dispatchResult, error)

// Backend shows one notification and reports whether the user clicked it. A
// non-nil error means delivery itself failed; a nil error means the
// notification was shown and `clicked` is its terminal interaction.
type Backend func(ctx context.Context, windowID int, account, title, subtitle string) (clicked bool, err error)

// SetBackend replaces the Alerter backend selected at construction. It is the
// seam the daemon's handler, probe, and lifecycle tests use to observe
// notification decisions without spawning a real macOS alert; a nil backend
// makes the notifier behave as if no compatible Alerter were installed.
func (n *Notifier) SetBackend(backend Backend) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if backend == nil {
		n.dispatch = nil
		return
	}
	n.dispatch = func(ctx context.Context, windowID int, account, title, subtitle string) (dispatchResult, error) {
		clicked, err := backend(ctx, windowID, account, title, subtitle)
		if err != nil {
			return dispatchResult{}, err
		}
		if clicked {
			return deliveredResult(interactionClicked), nil
		}
		return deliveredResult(interactionUnavailable), nil
	}
}

// dispatcher bundles a backend's dispatch function with the timeout the
// runDispatch goroutine should use. Bundling avoids a mismatch where the
// Alerter owns the interaction timeout; the grace period bounds process cleanup.
type dispatcher struct {
	fn         dispatchFn
	timeout    time.Duration
	capability protocol.NotificationCapability
}

type alerterResolver struct {
	goos      string
	lookPath  func(string) (string, error)
	stat      func(string) (os.FileInfo, error)
	versionOf func(context.Context, string) ([]byte, error)
}

func defaultAlerterResolver() alerterResolver {
	return alerterResolver{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		stat:     os.Stat,
		versionOf: func(ctx context.Context, path string) ([]byte, error) {
			return exec.CommandContext(ctx, path, "--version").Output()
		},
	}
}

func selectDispatcher(cfg *config.NotificationConfig) dispatcher {
	capability := resolveAlerter(cfg, defaultAlerterResolver())
	if !capability.Available {
		slog.Debug("notifier: interactive notifications unavailable", "reason", capability.Reason, "executable", capability.Executable)
		return dispatcher{timeout: alerterDispatchTimeout, capability: capability}
	}
	path := capability.Executable
	slog.Debug("notifier: using alerter backend", "path", path, "version", capability.Version)
	return dispatcher{
		fn: func(ctx context.Context, windowID int, account, title, subtitle string) (dispatchResult, error) {
			return runAlerterAt(ctx, path, windowID, account, title, subtitle)
		},
		timeout:    alerterDispatchTimeout,
		capability: capability,
	}
}

func resolveAlerter(cfg *config.NotificationConfig, resolver alerterResolver) protocol.NotificationCapability {
	capability := protocol.NotificationCapability{Enabled: notificationsEnabled(cfg), Backend: "alerter"}
	if !capability.Enabled {
		capability.Backend = "none"
		capability.Reason = "disabled"
		return capability
	}
	if resolver.goos != "darwin" {
		capability.Reason = "unsupported_platform"
		return capability
	}

	override := strings.TrimSpace(cfg.AlerterPath)
	var candidates []string
	if override != "" {
		expanded := config.ExpandTilde(override)
		if !filepath.IsAbs(expanded) {
			capability.Executable = expanded
			capability.Reason = "invalid_override"
			return capability
		}
		candidates = []string{expanded}
	} else {
		if path, err := resolver.lookPath("alerter"); err == nil && path != "" {
			candidates = append(candidates, path)
		}
		candidates = append(candidates,
			"/opt/homebrew/bin/alerter",
			"/usr/local/bin/alerter",
			"/opt/local/bin/alerter",
		)
	}

	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		info, err := resolver.stat(path)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			if override != "" {
				capability.Executable = path
				capability.Reason = "invalid_override"
				return capability
			}
			continue
		}

		capability.Executable = path
		ctx, cancel := context.WithTimeout(context.Background(), alerterProbeTimeout)
		out, probeErr := resolver.versionOf(ctx, path)
		cancel()
		if probeErr != nil {
			capability.Reason = "probe_failed"
			if override != "" {
				return capability
			}
			continue
		}
		version := strings.TrimSpace(string(out))
		capability.Version = version
		majorText, _, _ := strings.Cut(version, ".")
		major, parseErr := strconv.Atoi(majorText)
		if parseErr != nil || major < minimumAlerterMajor {
			capability.Reason = "version_unsupported"
			if override != "" {
				return capability
			}
			continue
		}
		capability.Available = true
		capability.Interactive = true
		capability.Reason = ""
		return capability
	}

	if capability.Reason == "" {
		capability.Executable = ""
		capability.Version = ""
		capability.Reason = "not_found"
	}
	return capability
}

func notificationsEnabled(cfg *config.NotificationConfig) bool {
	if cfg == nil {
		return false
	}
	return cfg.AnyEnabled()
}

func alerterArgs(title, subtitle string) []string {
	return alerterArgsWithTimeout(title, subtitle, alerterProtocolTimeout)
}

func alerterArgsWithTimeout(title, subtitle string, timeout time.Duration) []string {
	return alerterArgsWithMessage(title, subtitle, "", timeout)
}

func alerterArgsWithMessage(title, subtitle, message string, timeout time.Duration) []string {
	timeoutSeconds := strconv.FormatInt(int64(timeout/time.Second), 10)
	return []string{"--title", title, "--subtitle", subtitle, "--message", message, "--timeout", timeoutSeconds, "--sender", alerterBundleIdentifier}
}

func runAlerterAt(ctx context.Context, path string, _ int, _ string, title, subtitle string) (dispatchResult, error) {
	return runAlerterAtWithTimeout(ctx, path, title, subtitle, alerterProtocolTimeout)
}

func runAlerterAtWithTimeout(ctx context.Context, path, title, subtitle string, timeout time.Duration) (dispatchResult, error) {
	out, err := exec.CommandContext(ctx, path, alerterArgsWithTimeout(title, subtitle, timeout)...).Output()
	if err != nil {
		return dispatchResult{}, err
	}
	return parseAlerterOutput(out), nil
}

// TestNotificationDelivery sends a visible, short-lived notification under
// Alerter's own bundle identity. It intentionally bypasses Agent Mux's per-event
// notification switches because invoking the command is explicit consent to a
// one-off test. A clean Alerter exit confirms delivery; expiration means the
// backend never acknowledged delivery.
func TestNotificationDelivery(ctx context.Context, cfg *config.NotificationConfig) (NotificationDeliveryTestResult, error) {
	return testNotificationDeliveryWith(ctx, cfg, defaultAlerterResolver(), notificationTestTimeout+notificationTestGrace, runNotificationTestAt)
}

type notificationTestRunner func(context.Context, string, string, string, time.Duration) (dispatchResult, error)

func runNotificationTestAt(ctx context.Context, path, title, message string, timeout time.Duration) (dispatchResult, error) {
	out, err := exec.CommandContext(ctx, path, alerterArgsWithMessage(title, "Notification test", message, timeout)...).Output()
	if err != nil {
		return dispatchResult{}, err
	}
	return parseAlerterOutput(out), nil
}

func testNotificationDeliveryWith(ctx context.Context, cfg *config.NotificationConfig, resolver alerterResolver, dispatchTimeout time.Duration, run notificationTestRunner) (NotificationDeliveryTestResult, error) {
	// Preserve the configured executable override while forcing discovery on for
	// this explicit test, even when every automatic notification category is off.
	testConfig := config.NotificationConfig{Stop: true}
	if cfg != nil {
		testConfig.AlerterPath = cfg.AlerterPath
	}
	capability := resolveAlerter(&testConfig, resolver)
	result := NotificationDeliveryTestResult{Capability: capability}
	if !capability.Available || !capability.Interactive {
		return result, fmt.Errorf("notification backend unavailable (%s)", capability.Reason)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	dispatched, err := run(
		probeCtx,
		capability.Executable,
		"Agent Mux notification test",
		"If you can see this, Agent Mux notifications are working.",
		notificationTestTimeout,
	)
	if err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return result, ErrNotificationDeliveryUnconfirmed
		}
		if probeCtx.Err() != nil {
			return result, probeCtx.Err()
		}
		return result, fmt.Errorf("running notification backend: %w", err)
	}
	result.Interaction = string(dispatched.interaction)
	return result, nil
}

// parseAlerterOutput reports the terminal delivery and interaction outcome.
// alerter prints exactly one of @CONTENTCLICKED, @ACTIONCLICKED, @CLOSED,
// @TIMEOUT, @REPLIED, or @CONTENTCLOSED on stdout when it exits. Unknown or
// empty successful output remains a delivered notification but never requests
// focus.
func parseAlerterOutput(stdout []byte) dispatchResult {
	line := strings.TrimSpace(string(stdout))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	switch line {
	case "@CONTENTCLICKED", "@ACTIONCLICKED":
		return deliveredResult(interactionClicked)
	case "@TIMEOUT":
		return deliveredResult(interactionTimeout)
	case "@CLOSED", "@CONTENTCLOSED":
		return deliveredResult(interactionDismissed)
	case "@REPLIED":
		return deliveredResult(interactionReplied)
	default:
		return deliveredResult(interactionUnknown)
	}
}
