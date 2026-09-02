package daemon

// Authoritative idle observations. Native hooks are primary, but they can be
// lost: Claude documents that `Stop` does not fire after a user interruption,
// and a session left idle after such an interruption keeps a stale ⚡ (or a
// stranded older session's bolt) until a new prompt lands. An authoritative
// idle observation is daemon-side external evidence — a stabilized screen read
// from a harness adapter (sessions 3 and 4) — that the interactive session is
// actually done. It settles that stale interactive work by applying a synthetic
// probe-sourced stop, but only when it is safe to do so:
//
//   - the reducer receipt sequence captured before the screen read is still
//     current, so newer activity is never overwritten (a reducer event landing
//     between capture and application discards the probe);
//   - no /clear, /reset, or /new flow owns the window's cleared state;
//   - the configured number of consecutive stabilized samples have agreed, so a
//     single transient idle frame cannot settle live work.
//
// The stop is applied through the reducer's SettleAuthoritativeIdle seam, NOT
// the legacy scalar machine's separate legacy source: pipe-mode `claude -p`
// children, subagents, live ASL leases, attention icons, warnings, and terminal
// errors are all preserved, and the reduced visible state is what actually
// renders. A notification fires only when the reduced icon truly transitions to
// stop.

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
)

// AuthoritativeIdle is one piece of external idle evidence for a managed
// window. WindowID names the tab; Source is the daemon probe source the
// synthetic stop is attributed to (a SourceProbe fact, deliberately distinct
// from the harness session's own source and the legacy scalar bucket); Harness
// labels the adapter that produced the evidence; CapturedSeq is the window's
// reducer receipt sequence (WindowState.ShadowSeq) sampled before the screen
// read, used to discard the probe if any reducer event has since landed; At is
// the evidence timestamp; and RequiredSamples is how many consecutive
// stabilized idle observations must agree before the stop is applied (values
// below 1 are treated as 1). The remaining fields carry the notification
// context used only when the reduced state actually transitions to stop.
type AuthoritativeIdle struct {
	WindowID    int
	Source      protocol.SourceID
	Harness     string
	CapturedSeq uint64
	// ProbeBinding, when present, strengthens the ordinary target receipt check
	// with the exact producer session, source pane, source receipt, and snapshot
	// epochs captured by a route-aware adapter. Legacy/direct adapters may leave
	// it zero-valued and retain the original check.
	ProbeBinding    screenProbeBinding
	At              int64
	RequiredSamples int

	// EvidenceTier names the grade of evidence that bought RequiredSamples — a
	// screenstate.Tier rendered by its String method. It is DIAGNOSTIC ONLY: no
	// gate reads it, and a producer with no tier concept (the Copilot transcript
	// probe) leaves it empty, which reads as "none" in the event ring. It exists
	// because "why did this tab settle?" is otherwise answerable only from a
	// debug-level log that is off by default, while the applied reason below is
	// durable and visible through `amx daemon export-events`.
	EvidenceTier string

	SessionTitle  string
	Account       string
	PipeMode      bool
	Muted         bool
	SourceEventID string
	TraceID       string

	// SessionID names the interactive conversation this observation settles. It
	// is stamped onto the probe fact so a same-source recovery can re-consult the
	// backing transcript after the settle acks the interactive fact (see
	// SettleAuthoritativeIdle). Empty for probes that never recover (Codex/Claude
	// screen reads).
	SessionID string

	// Generation, when non-zero, is an EQUALITY fingerprint of the external
	// evidence this observation was read from. Consecutive stabilization samples
	// must observe the SAME generation to accumulate, and
	// recordIdleSampleWithGenerationLocked is the only consumer: it compares with
	// != and never with <. Order is therefore never consulted, and a producer is
	// free to emit an opaque hash (a Claude screen's normalized text) exactly as
	// readily as a value that happens to be monotonic (a Copilot transcript's
	// byte size). A probe source whose evidence changes on every new record can
	// consequently not reach its required-samples threshold across two DIFFERENT
	// states, so a run of distinct intermediate terminals never settles — and a
	// pane still repainting an elapsed timer never settles at all. A zero
	// generation disables the check.
	Generation int64

	// ConfirmStable, when non-nil, is called under stateMu immediately before the
	// synthetic stop is settled. It returns false when fresh evidence landed
	// since capture that the reducer receipt sequence cannot see — an append-only
	// artifact (the Copilot transcript) grown by a record written after the
	// terminal read but before this settle. A false result discards the probe and
	// restarts stabilization, so the tight read-then-append race never false-stops.
	ConfirmStable func() bool

	// ConfirmNotify, when non-nil, is called OFF the state lock immediately before
	// a synthetic stop notification is dispatched, and only when the reduced icon
	// actually transitioned to stop. It returns false to SUPPRESS the notification
	// because the settled stop is about to be retracted: the ConfirmStable gate
	// bounds but cannot make atomic the read-then-settle ordering against an
	// external append-only writer, so a record can land after ConfirmStable
	// returned but before the reducer mutation, leaving a synthetic stop over a
	// session that is actually still active. The post-stat recovery path restores
	// the interactive ⚡ on the next probe cycle, but it cannot revoke a stop
	// notification already handed to the async dispatcher — so this gate refuses to
	// dispatch one in the first place when a fresh read shows the same active
	// evidence recovery will act on. A nil ConfirmNotify (screen probes,
	// whole-session terminals that cannot be intermediate) always notifies.
	ConfirmNotify func() bool

	// DiagnosticEvent names the synthetic export/event-log row this observation
	// records (e.g. a screen adapter's "ScreenStopProbe"). It defaults to
	// "AuthoritativeIdle" when empty. The row always carries the ACTUAL reduced
	// result, never the requested stop.
	DiagnosticEvent string
}

// AuthoritativeIdleSource is the probe source an authoritative idle observation
// applies its synthetic stop under. It is a daemon-originated SourceProbe fact
// keyed by harness so a Codex idle and a Claude idle never collide, and is
// deliberately never a SourceLegacy fact: an idle probe must settle stale work
// through the reducer's source-scoped facts, not the legacy scalar grace.
func AuthoritativeIdleSource(harnessName string) protocol.SourceID {
	key := "idle"
	if h := harness.Normalize(harnessName); h != "" {
		key = "idle:" + h
	}
	return protocol.SourceID{Kind: protocol.SourceProbe, Key: key}
}

// captureWindowReceipt samples the window-wide reducer receipt generation
// (WindowState.ShadowSeq) under stateMu so a slow off-lock external read (a
// screen text capture, a transcript stat) can later prove no receipt-relevant
// state transition landed while it was looking. A window with no in-process
// state yet reports generation zero; every path that creates receipt-visible
// state (including deferred-stop acceptance) advances the generation past
// zero, so the matching recheck treats a window created in the meantime as a
// receipt change. Safe to call off the state lock.
func (h *Handler) captureWindowReceipt(windowID int) uint64 {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.windowReceiptLocked(windowID)
}

// windowReceiptLocked returns the window-wide reducer receipt generation, or
// zero for a window with no in-process state. Callers hold stateMu.
func (h *Handler) windowReceiptLocked(windowID int) uint64 {
	if st, ok := h.windowStateLocked(windowID); ok {
		return st.ShadowSeq
	}
	return 0
}

// windowReceiptCurrentLocked reports whether the window-wide reducer receipt
// still matches the value captured before an off-lock external read. A false
// result means a receipt-relevant transition (a fresh prompt, a native or
// deferred terminal hook, an error, a clear) landed between evidence capture
// and application, so the complete observation must be discarded without any
// reducer, guard, audit, renderer, or notification mutation. Callers hold
// stateMu and must keep it held through the observation's application so the
// check stays atomic.
func (h *Handler) windowReceiptCurrentLocked(windowID int, capturedSeq uint64) bool {
	return h.windowReceiptLocked(windowID) == capturedSeq
}

// idleSampleRef keys the consecutive idle-sample stabilization streak by window
// and probe source key, mirroring aslLeaseRef's shape.
type idleSampleRef struct {
	windowID int
	key      string
}

// recordIdleSampleLocked credits one stabilized idle observation for the ref
// and returns the new consecutive-sample count. Callers hold stateMu.
func (h *Handler) recordIdleSampleLocked(ref idleSampleRef) int {
	if h.idleSampleStreak == nil {
		h.idleSampleStreak = make(map[idleSampleRef]int)
	}
	h.idleSampleStreak[ref]++
	return h.idleSampleStreak[ref]
}

// recordIdleSampleWithGenerationLocked credits one stabilized idle observation
// for the ref, keyed to an evidence generation. When the observed generation
// differs from the last one recorded for this ref, the stabilization streak
// restarts before this sample is counted: agreeing samples must observe the SAME
// generation. A zero generation disables the check and behaves exactly like
// recordIdleSampleLocked. Callers hold stateMu.
func (h *Handler) recordIdleSampleWithGenerationLocked(ref idleSampleRef, generation int64) int {
	if generation != 0 {
		if h.idleSampleGen == nil {
			h.idleSampleGen = make(map[idleSampleRef]int64)
		}
		if h.idleSampleGen[ref] != generation {
			delete(h.idleSampleStreak, ref)
			h.idleSampleGen[ref] = generation
		}
	}
	return h.recordIdleSampleLocked(ref)
}

// clearIdleSampleLocked resets a single probe source's stabilization streak and
// its recorded evidence generation. Callers hold stateMu.
func (h *Handler) clearIdleSampleLocked(ref idleSampleRef) {
	delete(h.idleSampleStreak, ref)
	delete(h.idleSampleGen, ref)
}

// clearIdleSamplesForWindowLocked drops every stabilization streak for a
// window, used when fresh activity or a window closure invalidates all of its
// in-flight idle evidence. Callers hold stateMu.
func (h *Handler) clearIdleSamplesForWindowLocked(windowID int) {
	for ref := range h.idleSampleStreak {
		if ref.windowID == windowID {
			delete(h.idleSampleStreak, ref)
		}
	}
	for ref := range h.idleSampleGen {
		if ref.windowID == windowID {
			delete(h.idleSampleGen, ref)
		}
	}
}

// RecordIdleSample credits one stabilized idle observation for the window's
// probe source and returns the new consecutive-sample count. Screen adapters
// that require stabilized evidence call it each poll and compare the count
// against their required sample threshold before emitting an AuthoritativeIdle.
// Safe to call off the state lock.
func (h *Handler) RecordIdleSample(windowID int, source protocol.SourceID) int {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.recordIdleSampleLocked(idleSampleRef{windowID: windowID, key: source.Key})
}

// ClearIdleSample resets a probe source's consecutive-sample streak. Adapters
// call it after a classifier failure or a protected-state change so
// stabilization restarts from zero. Safe to call off the state lock.
func (h *Handler) ClearIdleSample(windowID int, source protocol.SourceID) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.clearIdleSampleLocked(idleSampleRef{windowID: windowID, key: source.Key})
}

// applyAuthoritativeIdle atomically folds one idle observation into a window's
// reducer under stateMu and publishes the reduced result. It reports whether a
// synthetic stop was actually applied (false when the observation was discarded
// as stale, blocked by a reset flow, still stabilizing, or superseded by an
// existing probe fact). Adapters call it each time they observe a stabilized
// idle screen; the required-samples gate and the captured-sequence check make
// repeated safe calls idempotent.
func (h *Handler) applyAuthoritativeIdle(obs AuthoritativeIdle) bool {
	if obs.WindowID <= 0 {
		return false
	}
	ref := idleSampleRef{windowID: obs.WindowID, key: obs.Source.Key}

	h.stateMu.Lock()
	st, ok := h.windowStateLocked(obs.WindowID)
	if !ok || st.Reducer == nil {
		// No reducer facts to settle. Drop any partial streak so a window that
		// produced no facts cannot strand a stabilization count.
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return false
	}
	// Discard the probe if either reducer state or the cached Kitty snapshot
	// moved since the evidence was captured. Snapshot publication refreshes mute
	// state while holding stateMu, so this generation check is atomic with the
	// mute assignment and reducer mutation below: a mute-only snapshot change
	// cannot be overwritten by an older observation whose reducer receipt still
	// happens to match.
	fresh := st.ShadowSeq == obs.CapturedSeq
	if obs.ProbeBinding.valid() {
		fresh = obs.ProbeBinding.TargetWindowID == obs.WindowID &&
			obs.ProbeBinding.TargetReceipt == obs.CapturedSeq &&
			h.screenProbeBindingCurrentLocked(obs.ProbeBinding)
	}
	if !fresh {
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return false
	}
	// A /clear, /reset, or /new flow owns the window's cleared-state semantics;
	// a synthetic stop must never surface during one. Leave the streak intact —
	// the reset cleanup clears it through the normal activity path once it lands.
	if st.PendingReset != state.ResetFlowNone {
		h.stateMu.Unlock()
		return false
	}
	// Require the configured number of consecutive stabilized samples before
	// acting so a single transient idle frame cannot settle live work. When the
	// observation carries a generation, samples only accumulate while it holds
	// steady, so a run of distinct intermediate terminals never converges.
	required := obs.RequiredSamples
	if required < 1 {
		required = 1
	}
	if count := h.recordIdleSampleWithGenerationLocked(ref, obs.Generation); count < required {
		h.stateMu.Unlock()
		return false
	}
	// Final freshness check for append-only evidence: a record written after the
	// terminal read but before this settle (with no reducer event to advance the
	// captured sequence) must still discard the probe. Restart stabilization so
	// the newly appended state must itself stabilize before any future settle.
	if obs.ConfirmStable != nil && !obs.ConfirmStable() {
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return false
	}
	// Every freshness and stabilization gate has passed, so this is the strongest
	// idle evidence the daemon can produce — exactly the moment a native terminal
	// stop being withheld for this window should be released. Hand the evidence to
	// the hold instead of settling a probe fact: the committed stop keeps its
	// harness provenance, and `probe:<harness>` keeps meaning the native hook was
	// lost. The stabilization streak is deliberately left intact — the tab stays ⚡
	// until the hold commits, so the next observation must be able to confirm
	// again if this one is superseded by trailing activity.
	if h.confirmStopHoldLocked(obs.WindowID, obs.Source) {
		h.stateMu.Unlock()
		return false
	}

	// Snapshot-derived mute state is part of the observation. Apply it only
	// after every freshness gate and in the same critical section as the
	// reducer mutation, so a stale probe cannot alter notification behavior.
	st.Muted = obs.Muted
	before := st.Effective
	at := obs.At
	if at == 0 {
		at = time.Now().Unix()
	}
	originWindowID := obs.WindowID
	if obs.ProbeBinding.SourceWindowID > 0 {
		originWindowID = obs.ProbeBinding.SourceWindowID
	}
	st.ShadowSeq++
	decision := st.Reducer.SettleAuthoritativeIdle(obs.Source, st.ShadowSeq, at, obs.SessionID, originWindowID)
	if decision != protocol.DecisionApplied {
		// The probe fact did not advance (a newer same-source fact already
		// exists). Nothing to publish; reset the streak so the next stretch of
		// evidence restarts cleanly.
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return false
	}
	// The observation settled: this stabilization stretch is spent.
	h.clearIdleSampleLocked(ref)

	st.Reducer.Compact(at)
	effective := st.Reducer.Reduce(at)
	h.publishEffectiveLocked(obs.WindowID, st, effective)

	// Record the ACTUAL reduced result, not the requested stop: a probe masked
	// by a live ASL lease, a newer interactive bolt, or a higher-precedence
	// attention/error icon records the icon that actually won.
	resultAction := effective.Icon
	if resultAction == "" {
		resultAction = "idle"
	}
	diagnosticEvent := obs.DiagnosticEvent
	if diagnosticEvent == "" {
		diagnosticEvent = eventAuthoritativeIdle
	}
	// The reason pins WHY this settle was allowed into the durable per-window
	// event ring, at zero cost and with no schema change: tier is the grade of
	// evidence the screen produced, samples is the run length that grade bought,
	// and generation is the evidence fingerprint every sample in that run had to
	// agree on. Together they turn "the tab went 🛑" into a reading of the
	// decision — a settle at samples=2 on a stable generation is the designed
	// path, while an unexpected tier or a suspiciously low count names the fault
	// directly. No consumer parses this string; it is read by humans through
	// `amx daemon export-events`.
	tier := obs.EvidenceTier
	if tier == "" {
		tier = "none"
	}
	h.appendWatchdogEventLocked(obs.WindowID, originWindowID, time.Unix(at, 0), diagnosticEvent, resultAction,
		string(obs.Source.Kind)+":"+obs.Source.Key,
		fmt.Sprintf("authoritative idle (%s) settled visible state %q→%q (tier=%s samples=%d generation=%d)",
			idleHarnessLabel(obs.Harness), before.Icon, effective.Icon, tier, required, obs.Generation))
	if len(st.EventLog) > 0 && obs.TraceID != "" {
		event := &st.EventLog[len(st.EventLog)-1]
		event.TraceID = obs.TraceID
		event.SourceEventID = obs.SourceEventID
		event.ProducerHarness = obs.Harness
		event.Profile = obs.Account
		event.SessionID = obs.SessionID
		h.linkSessionEventRenderGenerationLocked(obs.WindowID, st, event, obs.TraceID, "", obs.SourceEventID, st.Seq+1)
	}
	h.syncRendererMetadataLocked(obs.WindowID)

	if effective != before {
		h.markWindowDirtyLocked(obs.WindowID)
	} else {
		h.markWindowActivityDirtyLocked(obs.WindowID)
	}

	// Transition-only, source-event-deduped notification: fire only when the
	// visible icon actually becomes stop. A masked or unchanged reduction never
	// notifies, and a retried upstream event notifies at most once.
	notify := effective.Icon == "stop" && before.Icon != "stop" &&
		st.MarkSourceEventNotified(obs.SourceEventID)
	title, account, pipe := obs.SessionTitle, obs.Account, obs.PipeMode
	h.stateMu.Unlock()

	// Final off-lock notification gate for retractable synthetic stops: a record
	// can land after the ConfirmStable freshness check but before the reducer
	// mutation, stranding this stop over a still-active session. Recovery restores
	// ⚡ next cycle but cannot revoke a notification already dispatched, so a fresh
	// read that shows the append active suppresses the false stop notification
	// here. The visible stop still stands and self-heals through recovery; only the
	// unrevocable notification is withheld.
	if notify && obs.ConfirmNotify != nil && !obs.ConfirmNotify() {
		notify = false
	}
	if notify {
		h.maybeNotifyTraced(obs.WindowID, title, account, "stop", pipe, NotificationTrace{
			TraceID: obs.TraceID, SourceEventID: obs.SourceEventID,
			Harness: obs.Harness, Profile: account, SessionID: obs.SessionID,
		})
	}
	return true
}

// idleHarnessLabel gives the authoritative-idle diagnostic a stable harness
// label, falling back to a generic tag when the observation carried none.
func idleHarnessLabel(harnessName string) string {
	if h := harness.Normalize(harnessName); h != "" {
		return h
	}
	return "session"
}
