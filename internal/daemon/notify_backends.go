package daemon

// Strict interactive macOS notifications via Alerter 26.x or newer.

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// alerterProtocolTimeout is sent to alerter so a notification that receives
// no interaction still exits successfully with @TIMEOUT. The process context
// gets a bounded grace period beyond that protocol timeout. Notification
// Center can delay delivery enough that alerter's own countdown starts several
// seconds after process launch, so a one-minute grace avoids racing the
// backend's successful @TIMEOUT token while still bounding a genuinely stuck
// process.
const (
	alerterProtocolTimeout = 5 * time.Minute
	alerterTimeoutGrace    = time.Minute
	alerterDispatchTimeout = alerterProtocolTimeout + alerterTimeoutGrace
	alerterProbeTimeout    = 500 * time.Millisecond
	kittyBundleIdentifier  = "net.kovidgoyal.kitty"
)

const minimumAlerterMajor = 26

// NotificationCapability is the daemon-start snapshot of native notification
// support. It is exported through health so operators can distinguish a ready
// daemon from an unavailable optional notification dependency.
type NotificationCapability struct {
	Enabled     bool   `json:"enabled"`
	Available   bool   `json:"available"`
	Interactive bool   `json:"interactive"`
	Backend     string `json:"backend"`
	Executable  string `json:"executable,omitempty"`
	Version     string `json:"version,omitempty"`
	Reason      string `json:"reason,omitempty"`
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

// dispatcher bundles a backend's dispatch function with the timeout the
// runDispatch goroutine should use. Bundling avoids a mismatch where the
// Alerter owns the interaction timeout; the grace period bounds process cleanup.
type dispatcher struct {
	fn         dispatchFn
	timeout    time.Duration
	capability NotificationCapability
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

func resolveAlerter(cfg *config.NotificationConfig, resolver alerterResolver) NotificationCapability {
	capability := NotificationCapability{Enabled: notificationsEnabled(cfg), Backend: "alerter"}
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
	timeoutSeconds := strconv.FormatInt(int64(alerterProtocolTimeout/time.Second), 10)
	return []string{"--title", title, "--subtitle", subtitle, "--message", "", "--timeout", timeoutSeconds, "--sender", kittyBundleIdentifier}
}

func runAlerterAt(ctx context.Context, path string, _ int, _ string, title, subtitle string) (dispatchResult, error) {
	out, err := exec.CommandContext(ctx, path, alerterArgs(title, subtitle)...).Output()
	if err != nil {
		return dispatchResult{}, err
	}
	return parseAlerterOutput(out), nil
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
