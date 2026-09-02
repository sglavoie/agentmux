package daemon

import (
	"hash/fnv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// SetKittyClient injects the Kitty client used by the stale sweep to clear
// stranded tab-title icons. Passing nil disables the title-clear step
// (notifications still fire). Safe to call once before the sweep runs.
func (h *Handler) SetKittyClient(client kittyTitleClient) {
	h.useKittyClient(client)
	// Prime the snapshot cache immediately so the hook path has something to
	// route against before the first render tick. The render/restore loop keeps
	// it fresh afterward; a list error here just leaves the cache cold. This
	// list is synchronous, so production startup uses useKittyClient + the
	// bounded restore instead, and only tests and simple callers rely on the
	// prime here.
	h.primeSnapshot(time.Now())
}

// useKittyClient installs the Kitty client without priming the snapshot cache.
// Production startup uses this so an unbounded ListWindows can never block the
// daemon before its bounded initial restore runs.
func (h *Handler) useKittyClient(client kittyTitleClient) {
	h.kittyClient = client
}

// SetStaleThresholds configures the per-action sweep thresholds. The global
// map keys are action names ("active", "subagent", "compact"); the
// perAccount map is keyed first by account name (case-insensitive — names
// are lowercased on insert) and then by action name. A zero or missing
// inner entry falls back to the global default for that action. The sweep
// is disabled entirely when every global value is <= 0 AND no per-account
// override supplies a positive duration.
func (h *Handler) SetStaleThresholds(global map[string]time.Duration, perAccount map[string]map[string]time.Duration) {
	gs := make(map[string]time.Duration, len(global))
	for action, d := range global {
		gs[action] = d
	}
	h.staleThresholds = gs

	pa := make(map[string]map[string]time.Duration, len(perAccount))
	for account, m := range perAccount {
		if len(m) == 0 {
			continue
		}
		inner := make(map[string]time.Duration, len(m))
		for action, d := range m {
			inner[action] = d
		}
		pa[strings.ToLower(account)] = inner
	}
	h.perAccountThresholds = pa
}

// resolveThreshold returns the sweep duration to use for the given account
// and action. Per-account overrides win over the global default; a zero
// value at either level means "no threshold configured for this action".
func (h *Handler) resolveThreshold(account, action string) time.Duration {
	if action == "" {
		return 0
	}
	if account != "" {
		if inner, ok := h.perAccountThresholds[strings.ToLower(account)]; ok {
			if d, ok := inner[action]; ok && d > 0 {
				return d
			}
		}
	}
	return h.staleThresholds[action]
}

// SetClaudeContextLimitProbe toggles the periodic Claude context-limit probe.
// Mirrors the [defaults.watchdog].claude_context_limit_probe config knob.
// Safe to call once before the probe ticker fires.
func (h *Handler) SetClaudeContextLimitProbe(enabled bool) {
	h.claudeContextLimitProbeEnabled = enabled
}

// SetStopConfirm configures the confirmed-idle gate applied to native stops:
// delay is the quiet period a held stop must observe, timeout is the hard
// deadline past which it commits unconfirmed, and enabled false restores the
// immediate-stop behaviour. Mirrors the [defaults.stop_confirm] config block.
// Safe to call once before the hook path runs.
func (h *Handler) SetStopConfirm(delay, timeout time.Duration, enabled bool) {
	h.stopConfirmDelay = delay
	h.stopConfirmTimeout = timeout
	h.stopConfirmEnabled = enabled
}

// SetCopilotTranscriptHomes installs the Copilot home directories the transcript
// stop probe resolves a managed tab's interactive session transcript against.
// profileHomes is the ordered set of configured Copilot profile homes (the
// caller must ~-expand them; ResolveCopilotTranscript joins verbatim);
// defaultHome is harness.DefaultHomeDir(harness.Copilot). Safe to call once
// before the probe ticker fires.
func (h *Handler) SetCopilotTranscriptHomes(profileHomes []string, defaultHome string) {
	homes := make([]string, 0, len(profileHomes))
	for _, home := range profileHomes {
		if home != "" {
			homes = append(homes, home)
		}
	}
	h.copilotTranscriptHomes = homes
	h.copilotDefaultHome = defaultHome
}

// SetHookSilenceThresholds configures the global and per-account hook-silence
// thresholds consumed by SweepHookSilence. The global value applies whenever a
// per-account override is missing or zero. A global value of 0 disables the
// sweep unless a positive per-account override is supplied. Per-account keys
// are lowercased on insert; lookups also lowercase the account name.
func (h *Handler) SetHookSilenceThresholds(global time.Duration, perAccount map[string]time.Duration) {
	h.hookSilenceThreshold = global
	pa := make(map[string]time.Duration, len(perAccount))
	for account, d := range perAccount {
		if d <= 0 {
			continue
		}
		pa[strings.ToLower(account)] = d
	}
	h.perAccountHookSilence = pa
}

// resolveHookSilenceThreshold returns the hook-silence duration to use for the
// given account. A positive per-account override wins over the global default;
// a zero value at both levels means the sweep is disabled for that account.
func (h *Handler) resolveHookSilenceThreshold(account string) time.Duration {
	if account != "" {
		if d, ok := h.perAccountHookSilence[strings.ToLower(account)]; ok && d > 0 {
			return d
		}
	}
	return h.hookSilenceThreshold
}

// hookSilenceWindowID maps an account name to a stable synthetic window ID
// used as the notifier dedup key for hook-silence dispatches. Real Kitty
// window IDs are positive ints; negating the FNV-32 hash places the synthetic
// IDs in a non-overlapping negative range. The -1 offset prevents collision
// with the zero value (uint32 hashes are 32-bit, so a 64-bit int can hold the
// full negation without overflow).
func hookSilenceWindowID(account string) int {
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(strings.ToLower(account)))
	return -int(hh.Sum32()) - 1
}

// anyStaleThresholdSet reports whether at least one global or per-account
// threshold is positive. The sweep early-exits when this returns false.
func (h *Handler) anyStaleThresholdSet() bool {
	for _, d := range h.staleThresholds {
		if d > 0 {
			return true
		}
	}
	for _, inner := range h.perAccountThresholds {
		for _, d := range inner {
			if d > 0 {
				return true
			}
		}
	}
	return false
}

const (
	// windowTombstoneRetention bounds how long a closed window's durable
	// metadata survives for post-mortem export after its live state is GC'd.
	windowTombstoneRetention = 24 * time.Hour
	// maxWindowTombstones bounds the tombstone count; past it the oldest
	// tombstones (and their durable metadata) are evicted first.
	maxWindowTombstones = 1000
)

// Synthetic daemon-emitted event names. They never cross the IPC wire as
// requests; the watchdog, reconciliation, and closed-window GC append them so
// the per-window event log and the durable export metadata record why the
// daemon changed state on its own.
const (
	eventWatchdogStale     = "WatchdogStale"
	eventSourceExpired     = "SourceExpired"
	eventASLReconciled     = "ASLReconciled"
	eventWindowClosed      = "WindowClosed"
	eventAuthoritativeIdle = "AuthoritativeIdle"
)

// appendWatchdogEventLocked records one synthetic daemon event atomically in
// both diagnostic surfaces: the current per-window event ring (via
// appendEventLocked, which also maintains the durable Stats counters) and the
// durable watchdog section of the export metadata. Callers hold stateMu.
func (h *Handler) appendWatchdogEventLocked(windowID, originWindowID int, at time.Time, event, action, source, reason string) {
	if originWindowID <= 0 {
		originWindowID = windowID
	}
	h.appendEventLocked(windowID, protocol.SessionEvent{
		At:             at.Unix(),
		Event:          event,
		Action:         action,
		Source:         source,
		Reason:         reason,
		OriginWindowID: originWindowID,
	})
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = event
	meta.Watchdog.LastReason = reason
	meta.Watchdog.LastAt = at.Unix()
	switch event {
	case eventSourceExpired:
		meta.Watchdog.SourceExpiries++
	case eventWatchdogStale:
		meta.Watchdog.StaleSweeps++
	case eventASLReconciled:
		meta.Watchdog.Reconciles++
	}
	h.windowMetadata[windowID] = meta
}
