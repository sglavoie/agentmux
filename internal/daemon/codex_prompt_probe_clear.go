package daemon

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// applyCodexClearObservation folds a classified cleared Codex screen into the
// window atomically under stateMu. The receipt recheck happens before ANY
// mutation — mute cache, pending notification, guard, reset bookkeeping,
// reducer, audit, persistence generation, or renderer queue — so a prompt or
// protected state accepted during either screen read survives unchanged.
// Holding stateMu through the guard update and pending-notification removal is
// safe: stateMu → guard.mu is already established by
// markWindowStateDirtyLocked. Notification disposition observers can perform
// Handler work, so cancellation is collected here but emitted only after both
// stateMu and resetStopCommitMu are released.
//
// This is intentionally a probe-specific application path rather than a call
// to Handle: the ordinary state-update pipeline releases stateMu between its
// legacy bookkeeping and reducer publication, which cannot make a receipt
// recheck atomic with the complete synthetic clear.
func (h *Handler) applyCodexClearObservation(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	windowID := binding.TargetWindowID
	targetHarness := harness.NormalizeKnown(userVars.EffectiveHarness())
	if targetHarness == "" && binding.legacyDirectManaged {
		targetHarness = harness.Codex
	}
	req := Request{
		Type:             TypeStateUpdate,
		WindowID:         windowID,
		Action:           "user_clear",
		Event:            screenClearProbeEvent,
		Source:           screenPromptProbeSource,
		Account:          userVars.EffectiveProfile(),
		Harness:          harness.Codex,
		SessionTitle:     titleParts.Base,
		currentIconState: titleParts.State,
		ResetFlow:        true,
		targetHarness:    targetHarness,
		targetHarnessSet: true,
	}

	// Serialize the complete clear with the final application of any held Stop
	// that has already fired and claimed its timer generation. A callback
	// paused before this boundary remains cancelable below; a callback that
	// already crossed it applies first and advances the receipt before this
	// observation can recheck freshness.
	var pendingDisposition NotificationDisposition
	var pendingCancelled bool
	var supersededHold pendingStopHold
	var supersededHoldDropped bool
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	defer func() {
		h.stateMu.Unlock()
		h.resetStopCommitMu.Unlock()
		if pendingCancelled {
			h.notifier.emitDisposition(pendingDisposition)
		}
		if supersededHoldDropped {
			h.recordSupersededStopHold(supersededHold)
		}
	}()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return icon == "" || icon == "stop"
	}) {
		slog.Debug("codex clear probe: discarded stale observation", "window_id", windowID)
		return
	}
	// The receipt covers held stops as well as applied reducer events. Cancel
	// the pending generation that existed when this clear was captured before
	// publishing the cleared reducer state. Because a newer held stop advances
	// ShadowSeq under this same lock, the freshness check above rejects a stale
	// clear before it can delete that newer generation.
	supersededHold, supersededHoldDropped = h.cancelStopHoldLocked(windowID)

	// Preserve the ordinary reset-flow provenance and final marker state: an
	// explicit reset-scoped user_clear enters and immediately completes the
	// reset flow, leaving PendingReset clear while the audit row retains
	// ResetFlow=true.
	req = h.applyResetFlowLocked(req)
	h.touchWindowMetadataLocked(req)
	st := h.ensureWindowStateLocked(windowID)
	st.Muted = titleParts.Muted || userVars.Mute != ""
	st.CopilotToolPermissionState = copilotToolPermissionNone
	st.SubagentCount = 0
	st.NotifiedStale = false
	st.ContextLimitNotified = false
	st.Activity = now
	st.LastSessionTitle = titleParts.Base
	st.LastPipeMode = false
	if profile := userVars.EffectiveProfile(); profile != "" {
		st.LastAccount = profile
		h.lastSeenEventAt[strings.ToLower(profile)] = now
		delete(h.notifiedHookSilence, strings.ToLower(profile))
	}
	if st.LastHarness == "" && targetHarness != "" {
		st.LastHarness = targetHarness
	}
	st.LastAction = ""
	st.LastActiveEvent = ""

	// user_clear always removes the legacy guard and any pending permission
	// notification. These calls stay inside the receipt critical section so a
	// later event cannot be cleared after it has advanced the reducer receipt.
	h.guard.TryUpdate(windowID, "user_clear", false)
	if h.notifier != nil {
		pendingDisposition, pendingCancelled = h.notifier.cancelPending(windowID)
	}

	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(ReducerEvent{
		Source: userActionSource(),
		Action: "user_clear",
		Seq:    st.ShadowSeq,
		At:     now.Unix(),
	})
	st.Reducer.Compact(now.Unix())
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
	h.clearIdleSamplesForWindowLocked(windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.ActivityAt = now.Unix()
	h.windowMetadata[windowID] = meta
	eventRow := sessionEventFromRequest(req, req.Action, effective.Icon, titleParts.State)
	eventRow.OriginWindowID = binding.SourceWindowID
	h.appendEventLocked(windowID, eventRow)
	h.syncRendererMetadataLocked(windowID)
	// A real clear must always publish both durable reducer acknowledgement and
	// title cleanup, including when the stopped icon existed only in the Kitty
	// snapshot and the in-process reducer was previously empty.
	h.markWindowDirtyLocked(windowID)
}

// probeCodexIdleWindow recovers a Codex tab whose bolt icon is stuck on a
// finished assistant turn — a normal completion or a user interruption — that
// left no Stop hook behind. Codex is authoritative for its own screen, so the
// daemon reads the completed/interrupted UI and settles the stale interactive
// bolt through the race-safe authoritative-idle path.
//
// The reducer receipt sequence is captured before the cached Kitty snapshot and
// revalidated under stateMu BEFORE the slow screen read: applyAuthoritativeIdle
// rechecks it after the read, so any reducer event (a fresh prompt, activity, or
// a native terminal hook) landing after capture discards the observation. The
// synthetic stop is applied under the SourceProbe `codex_screen`
// fact, never the legacy scalar machine: a live ASL lease, a higher-precedence
// attention/error icon, a newer interactive bolt, or a pipe-mode/subagent fact
// all still win at reduce time. The caller only invokes this for a `bolt` tab,
// so protected on-screen icons never reach here in the first place.
func (h *Handler) probeCodexIdleWindow(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	targetWindowID := binding.TargetWindowID

	// Revalidate the snapshot receipt before the off-lock GetText. A window with no
	// reducer facts (an unmanaged/foreground-only bolt) has no stale interactive
	// work for the probe to settle, so bail without a screen read and drop any
	// in-flight stabilization streak.
	h.stateMu.Lock()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return icon == "bolt"
	}) {
		h.stateMu.Unlock()
		slog.Debug("codex stop probe: discarded stale snapshot", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID)
		return
	}
	st, ok := h.windowStateLocked(targetWindowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(idleSampleRef{windowID: targetWindowID, key: source.Key})
		h.stateMu.Unlock()
		return
	}
	h.stateMu.Unlock()

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "screen")
	if err != nil {
		slog.Debug("codex stop probe: get screen failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	if !ClassifyCodexCompletedTurnScreen(screen) && !ClassifyCodexInterruptedTurnScreen(screen) {
		// Still working, a bare composer with no terminal marker, or unrelated
		// output: reset the stabilization streak so a later transient idle frame
		// cannot settle live work.
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:        targetWindowID,
		Source:          source,
		Harness:         harness.Codex,
		CapturedSeq:     binding.TargetReceipt,
		ProbeBinding:    binding,
		At:              now.Unix(),
		RequiredSamples: 1,
		SessionTitle:    titleParts.Base,
		Account:         userVars.EffectiveProfile(),
		PipeMode:        false,
		Muted:           titleParts.Muted || userVars.Mute != "",
		SourceEventID:   fmt.Sprintf("codex-screen-stop:%d:%d:%d", binding.SourceWindowID, targetWindowID, binding.TargetReceipt),
		DiagnosticEvent: screenStopProbeEvent,
	})
}
