package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Screen-probe declines: the negative half of the idle evidence recorded in
// authoritative_idle.go. A decline is a screen the probe classified and Layer A
// refused to call a settleable terminal, credited per source so an operator can
// see why a window is not settling.

// ScreenDeclineObservation is one credited declined settle: the screen probe
// classified a frame and Layer A refused to call it a settleable terminal.
type ScreenDeclineObservation struct {
	// Streak is how many consecutive declines this probe source has now been
	// credited with, including the one just recorded. It restarts at one
	// whenever the observed fingerprint differs from the streak's.
	Streak int

	// Fingerprint is the normalized-screen fingerprint the streak is
	// accumulating against — the same value the stabilization gate would have
	// compared had the frame been settleable.
	Fingerprint int64

	// StableFingerprint reports whether the classifier has now declined the SAME
	// unchanging screen at least twice. This is the discriminator the decline
	// site's comment describes in prose: false means the pane is repainting,
	// which is what ordinary live work looks like, while true means the screen
	// went quiet and Layer A vetoed it anyway — the shape a classifier that has
	// stopped matching a redesigned harness UI takes. A zero fingerprint is
	// never stable, because the fail-closed zero Observation carries no hash at
	// all; that mirrors how recordIdleSampleWithGenerationLocked treats a zero
	// generation.
	StableFingerprint bool
}

// ScreenDeclineEvidence is one declined frame's diagnostic context, recorded
// alongside the streak it extends. Fingerprint is a decision input — it decides
// whether the streak continues or restarts — while Vetoes, StatusLine and At
// exist purely to be published: they are what tells an operator WHICH harness
// copy the classifier stopped matching, which a bare counter cannot.
type ScreenDeclineEvidence struct {
	// Fingerprint is the normalized-screen fingerprint of the declined frame,
	// the same value the stabilization gate would have compared.
	Fingerprint int64
	// Vetoes is screenstate.Veto.String() for the frame: the named reasons Layer
	// A refused, already rendered by the caller so this package never has to
	// know the bitset's spelling.
	Vetoes string
	// StatusLine is the harness status line the frame carried, empty when the
	// classifier found none.
	StatusLine string
	// At is when the decline was observed (unix seconds).
	At int64
}

// recordScreenDeclineLocked credits one declined settle for the ref against the
// fingerprint the frame carried, and returns the resulting decline state. A
// fingerprint that differs from the one the streak is accumulating against
// restarts the streak before this decline is counted, so the streak only ever
// measures repeated refusals of the same screen. The frame's remaining evidence
// is retained for export. Callers hold stateMu.
func (h *Handler) recordScreenDeclineLocked(ref idleSampleRef, evidence ScreenDeclineEvidence) ScreenDeclineObservation {
	if h.screenProbeDeclineStreak == nil {
		h.screenProbeDeclineStreak = make(map[idleSampleRef]int)
	}
	if h.screenProbeDeclineFingerprint == nil {
		h.screenProbeDeclineFingerprint = make(map[idleSampleRef]int64)
	}
	if h.screenProbeDeclineEvidence == nil {
		h.screenProbeDeclineEvidence = make(map[idleSampleRef]ScreenDeclineEvidence)
	}
	if prev, ok := h.screenProbeDeclineFingerprint[ref]; !ok || prev != evidence.Fingerprint {
		delete(h.screenProbeDeclineStreak, ref)
		h.screenProbeDeclineFingerprint[ref] = evidence.Fingerprint
	}
	h.screenProbeDeclineStreak[ref]++
	h.screenProbeDeclineEvidence[ref] = evidence
	streak := h.screenProbeDeclineStreak[ref]
	return ScreenDeclineObservation{
		Streak:            streak,
		Fingerprint:       evidence.Fingerprint,
		StableFingerprint: evidence.Fingerprint != 0 && streak > 1,
	}
}

// clearScreenDeclineLocked drops a probe source's decline streak, the
// fingerprint it was accumulating against, and the evidence of the frame that
// last extended it. Callers hold stateMu.
func (h *Handler) clearScreenDeclineLocked(ref idleSampleRef) {
	delete(h.screenProbeDeclineStreak, ref)
	delete(h.screenProbeDeclineFingerprint, ref)
	delete(h.screenProbeDeclineEvidence, ref)
}

// retractScreenDeclinesLocked drops every screen-probe decline streak recorded
// for a window and republishes its export metadata, so a retained streak can
// never outlive the condition it describes. It is a no-op for the overwhelming
// majority of windows, which hold no streak at all. Callers hold stateMu.
//
// A decline streak is a LIVE counter — "the classifier is refusing this screen
// right now" — and never a history: the durable per-window event ring is where
// history lives. That distinction is the whole reason this exists. The screen
// probe only runs while the window's reduced icon is ⚡, so the moment a native
// stop, a blocking prompt, or a clear/reset moves the window off it the probe
// stops being called and can no longer retract anything itself; without an
// explicit retraction the last streak would be frozen into the export forever
// and `amx doctor` would keep failing screenstate_persistent_decline for a
// window that already recovered.
func (h *Handler) retractScreenDeclinesLocked(windowID int) {
	if len(h.screenProbeDeclineStreak) == 0 {
		return
	}
	retracted := false
	for ref := range h.screenProbeDeclineStreak {
		if ref.windowID != windowID {
			continue
		}
		h.clearScreenDeclineLocked(ref)
		retracted = true
	}
	if retracted {
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
	if ScreenProbeEligibleIcon(icon) {
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
func (h *Handler) RecordScreenProbeDecline(windowID int, source protocol.SourceID, evidence ScreenDeclineEvidence) ScreenDeclineObservation {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	ref := idleSampleRef{windowID: windowID, key: source.Key}
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
	h.clearScreenDeclineLocked(idleSampleRef{windowID: windowID, key: source.Key})
	h.syncScreenDeclineMetadataLocked(windowID)
}
