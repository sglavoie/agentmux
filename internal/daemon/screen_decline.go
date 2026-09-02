package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Handler-side entry points for screen-probe declines. The counters, the
// fingerprint comparison, and the export projection all live in
// internal/daemon/activity as a caller-locked activity.Declines tracker; what
// stays here is the part that needs the daemon: stateMu, the stabilization
// streak the decline resets, and the export metadata it republishes.

// recordScreenDeclineLocked credits one declined settle for the ref against the
// fingerprint the frame carried, and returns the resulting decline state.
// Callers hold stateMu.
func (h *Handler) recordScreenDeclineLocked(ref activity.Ref, evidence activity.ScreenDeclineEvidence) activity.ScreenDeclineObservation {
	return h.declines.Record(ref, evidence)
}

// clearScreenDeclineLocked drops a probe source's decline streak, the
// fingerprint it was accumulating against, and the evidence of the frame that
// last extended it. Callers hold stateMu.
func (h *Handler) clearScreenDeclineLocked(ref activity.Ref) {
	h.declines.Clear(ref)
}

// retractScreenDeclinesLocked drops every screen-probe decline streak recorded
// for a window and republishes its export metadata, so a retained streak can
// never outlive the condition it describes. It is a no-op for the overwhelming
// majority of windows, which hold no streak at all. Callers hold stateMu.
func (h *Handler) retractScreenDeclinesLocked(windowID int) {
	if h.declines.RetractWindow(windowID) {
		h.syncScreenDeclineMetadataLocked(windowID)
	}
}

// retractScreenDeclinesForIconLocked keeps the decline streaks of a window
// consistent with its visible icon. It is written as an idempotent invariant of
// that icon rather than as a transition rule — the same shape, and for the same
// reason, as stampBusySinceLocked: every publication of the reduced state
// reasserts it, so no caller has to know what the previous icon was or which of
// the many paths into the reducer it came from. Callers hold stateMu.
func (h *Handler) retractScreenDeclinesForIconLocked(windowID int, icon string) {
	if activity.ScreenProbeEligibleIcon(icon) {
		return
	}
	h.retractScreenDeclinesLocked(windowID)
}

// RecordScreenProbeDecline resets a probe source's stabilization streak AND
// credits one decline against the frame's fingerprint, both under a single
// stateMu acquisition. The two belong together: the reset is the safety
// behaviour ClearIdleSample already performed on this path, the credit is the
// diagnostic record of why, and taking the lock twice would leave a window in
// which the streak is cleared but the decline that cleared it is not yet
// counted. The window's export metadata is republished in the same critical
// section, so the counter a diagnostic surface reads can never describe a
// different frame than the one just credited.
//
// Safe to call off the state lock.
func (h *Handler) RecordScreenProbeDecline(windowID int, source protocol.SourceID, evidence activity.ScreenDeclineEvidence) activity.ScreenDeclineObservation {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	ref := activity.Ref{WindowID: windowID, Key: source.Key}
	h.clearIdleSampleLocked(ref)
	decline := h.recordScreenDeclineLocked(ref, evidence)
	h.syncScreenDeclineMetadataLocked(windowID)
	return decline
}

// ClearScreenProbeDecline drops a probe source's decline streak. Callers use it
// after a settle actually lands: reaching a terminal screen is the classifier
// proving it can still grade this pane, which is precisely the condition the
// streak exists to detect the absence of. Safe to call off the state lock.
func (h *Handler) ClearScreenProbeDecline(windowID int, source protocol.SourceID) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.clearScreenDeclineLocked(activity.Ref{WindowID: windowID, Key: source.Key})
	h.syncScreenDeclineMetadataLocked(windowID)
}
