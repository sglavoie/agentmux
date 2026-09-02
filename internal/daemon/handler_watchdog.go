package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
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
	h.staleThresholds = activity.NewStaleThresholds(global, perAccount)
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
	h.hookSilence = activity.NewHookSilenceThresholds(global, perAccount)
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
