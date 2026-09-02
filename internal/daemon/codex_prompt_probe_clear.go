package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
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
	req := protocol.Request{
		Type:             protocol.TypeStateUpdate,
		WindowID:         windowID,
		Action:           "user_clear",
		Event:            screenClearProbeEvent,
		Source:           screenPromptProbeSource,
		Account:          userVars.EffectiveProfile(),
		Harness:          harness.Codex,
		SessionTitle:     titleParts.Base,
		CurrentIconState: titleParts.State,
		ResetFlow:        true,
		TargetHarness:    targetHarness,
		TargetHarnessSet: true,
	}

	// Serialize the complete clear with the final application of any held Stop
	// that has already fired and claimed its timer generation. A callback
	// paused before this boundary remains cancelable below; a callback that
	// already crossed it applies first and advances the receipt before this
	// observation can recheck freshness.
	var pendingDisposition notification.NotificationDisposition
	var pendingCancelled bool
	var supersededHold pendingStopHold
	var supersededHoldDropped bool
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	defer func() {
		h.stateMu.Unlock()
		h.resetStopCommitMu.Unlock()
		if pendingCancelled {
			h.notifier.EmitDisposition(pendingDisposition)
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
	st.CopilotToolPermissionState = state.CopilotToolPermissionNone
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
		pendingDisposition, pendingCancelled = h.notifier.CancelPendingDisposition(windowID)
	}

	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.UserActionSource(),
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
	eventRow := events.SessionEventFromRequest(req, req.Action, effective.Icon, titleParts.State)
	eventRow.OriginWindowID = binding.SourceWindowID
	h.appendEventLocked(windowID, eventRow)
	h.syncRendererMetadataLocked(windowID)
	// A real clear must always publish both durable reducer acknowledgement and
	// title cleanup, including when the stopped icon existed only in the Kitty
	// snapshot and the in-process reducer was previously empty.
	h.markWindowDirtyLocked(windowID)
}
