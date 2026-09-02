package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// supersedesStopHold reports whether an incoming state update should cancel a
// stop still held before the reducer. Any reducer-visible non-terminal action
// proves the harness is still working — a fresh prompt (a new turn), a tool
// call, a subagent, a compaction, a permission or elicitation prompt, a
// recoverable failure — as does an explicit wipe (user_clear/clear). The held
// stop is stale in every one of those cases, so it must not paint.
// Reset-flow cleanup (req.ResetFlow) is deliberately excluded: the held stop is
// kept so applyResetFlowLocked can fold it into the same user_clear when the
// hold commits.
func supersedesStopHold(req protocol.Request) bool {
	if req.ResetFlow {
		return false
	}
	if req.IsPrompt {
		return true
	}
	switch req.Action {
	case "user_clear", "clear", "active", "subagent", "compact", "permission", "elicitation", "failure":
		return true
	default:
		return false
	}
}

func (h *Handler) commitStopHold(windowID int, seq uint64) {
	h.stateMu.Lock()
	pending, ok := h.pendingStopHolds[windowID]
	if !ok || pending.seq != seq {
		h.stateMu.Unlock()
		return
	}
	// Keep the claimed generation addressable until its reducer application is
	// complete. A receipt-valid Codex clear that lands before the final commit
	// boundary can therefore cancel this callback even after its timer fired.
	pending.claimed = true
	if pending.class == stopHoldClassTerminal && !pending.confirmed {
		// The quiet period kept elapsing without the harness ever confirming, so
		// the hard deadline is what releases this stop.
		pending.confirmedBy = protocol.StopConfirmedByDeadline
	}
	h.pendingStopHolds[windowID] = pending
	h.stateMu.Unlock()

	if h.afterDeferredStopClaimForTest != nil {
		h.afterDeferredStopClaimForTest(windowID, seq)
	}

	// The commit mutex is the linearization boundary with Codex clear (and the
	// ordinary superseding-event cancellation prelude). Revalidate the claimed
	// map entry after acquiring it: if clear won while the callback was between
	// claim and application, the entry is gone and this callback is inert. If
	// the callback wins, superseding events wait until the Stop has advanced the
	// reducer receipt and then make their decision against that applied state.
	h.resetStopCommitMu.Lock()
	defer h.resetStopCommitMu.Unlock()
	h.stateMu.Lock()
	pending, ok = h.pendingStopHolds[windowID]
	if !ok || pending.seq != seq || !pending.claimed {
		h.stateMu.Unlock()
		return
	}
	req := pending.req
	if pending.class == stopHoldClassTerminal {
		req = h.refreshHeldStopRequestLocked(req)
		req.StopHoldWaitedMs = int(time.Since(pending.armedAt).Milliseconds())
		req.StopConfirmedBy = pending.confirmedBy
		req.StopHoldOutcome = protocol.StopHoldOutcomeDeadline
		if pending.confirmed {
			req.StopHoldOutcome = protocol.StopHoldOutcomeConfirmed
		}
	}
	h.stateMu.Unlock()

	h.handleStateUpdate(req)

	h.stateMu.Lock()
	if pending, ok := h.pendingStopHolds[windowID]; ok && pending.seq == seq {
		delete(h.pendingStopHolds, windowID)
	}
	h.stateMu.Unlock()
}

// refreshHeldStopRequestLocked re-reads the volatile window metadata the held
// stop captured at arm time. A terminal hold can wait the whole configured
// timeout, long enough for the tab title, profile, or visible icon to move on,
// so the committed stop must carry what the window looks like now. It mirrors
// probeClaudeIdleWindow's shape: read WindowState under stateMu, materialize
// plain values, then dispatch off-lock. Mutedness deliberately has no captured
// copy — the notifier consults Handler.IsMuted at dispatch time, so it is
// already read fresh. Callers hold stateMu.
func (h *Handler) refreshHeldStopRequestLocked(req protocol.Request) protocol.Request {
	st, ok := h.windowStateLocked(req.WindowID)
	if !ok {
		return req
	}
	if st.LastSessionTitle != "" {
		req.SessionTitle = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		req.Account = st.LastAccount
	}
	req.CurrentIconState = rendering.DesiredIcon(st)
	return req
}

// pendingTerminalStopSnapshotLocked is the export view of a stop this window is
// still withholding. Only the terminal class is exported: a reset hold is a
// 500ms debounce nothing can observe, while a terminal hold is a visible state
// the window is deliberately not showing yet, and the one whose failure to
// converge (stop_hold_stuck) is otherwise invisible. Callers hold stateMu.
func (h *Handler) pendingTerminalStopSnapshotLocked(windowID int) *protocol.PendingTerminalStop {
	pending, ok := h.pendingStopHolds[windowID]
	if !ok || pending.class != stopHoldClassTerminal {
		return nil
	}
	return &protocol.PendingTerminalStop{
		ArmedAt:    pending.armedAt.Unix(),
		QuietUntil: pending.quietUntil.Unix(),
		Deadline:   pending.deadline.Unix(),
		Confirmed:  pending.confirmed,
		Class:      protocol.StopHoldClassTerminal,
	}
}

// stopHoldDecisionReason maps a hold outcome to its stable decision reason.
// Only outcomes that end the hold without committing its stop get their own
// row; the release outcomes (confirmed, deadline) are stamped on the stop the
// hold commits.
func stopHoldDecisionReason(outcome protocol.StopHoldOutcome) (string, bool) {
	switch outcome {
	case protocol.StopHoldOutcomeHeld:
		return suppressionReasonTerminalStopHeld, true
	case protocol.StopHoldOutcomeSuperseded:
		return suppressionReasonTerminalStopSuperseded, true
	case protocol.StopHoldOutcomeBypassed:
		return suppressionReasonTerminalStopBypassed, true
	default:
		return "", false
	}
}

// appendStopHoldEvent records one synthetic EventTerminalStopHold row for a
// stop the handler is withholding from the reducer. Nothing is applied and no
// icon changes, so the row follows the suppressed-event append shape: the
// window's own effective state is both the before and after evidence.
func (h *Handler) appendStopHoldEvent(req protocol.Request, outcome protocol.StopHoldOutcome, waited time.Duration) {
	reason, ok := stopHoldDecisionReason(outcome)
	if !ok || req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	st := h.ensureWindowStateLocked(req.WindowID)
	effective := events.EffectiveStateEvidence(st.Effective)
	event := events.SuppressedSessionEvent(req, req.Action, reason, rendering.DesiredIcon(st))
	event.Event = protocol.EventTerminalStopHold
	event.StopHoldOutcome = outcome
	event.StopHoldWaitedMs = int(waited.Milliseconds())
	event.DecisionReason = reason
	event.ReducerDecision = protocol.DecisionSuppressed
	event.ReducerReason = reason
	event.ReducerInputAction = req.Action
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.EffectiveBefore = &effective
	event.EffectiveAfter = &effective
	event = h.appendEventLocked(req.WindowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}

// stopHoldClassFor reports which hold class an incoming stop belongs to and
// whether it should be held at all. The terminal class claims every stop the
// bypass matrix left armable (terminalArmable); a bypassed stop falls back to
// the reset class exactly as before the confirmed-idle gate existed.
func stopHoldClassFor(req protocol.Request, payload hookevent.HookPayload, terminalArmable bool) (stopHoldClass, bool) {
	if req.Action != "stop" || req.ContextLimit || req.ResetFlow {
		return stopHoldClassReset, false
	}
	if terminalArmable {
		return stopHoldClassTerminal, true
	}
	if resetSensitiveStop(req, payload) {
		return stopHoldClassReset, true
	}
	return stopHoldClassReset, false
}

// resetSensitiveStop reports whether a stop is one a late reset marker may
// still fold into the same user_clear transition.
func resetSensitiveStop(req protocol.Request, payload hookevent.HookPayload) bool {
	switch req.Event {
	case "Stop", "SessionEnd", "agentStop", "sessionEnd":
		return true
	case "Notification", "notification":
		return strings.EqualFold(strings.TrimSpace(payload.EffectiveNotificationType()), "agent_idle")
	default:
		return false
	}
}

// terminalStopHoldArmableLocked applies the bypass matrix. Every condition that
// makes a confirmed-idle hold impossible or pointless is decided once, at arm
// time, so the stop either enters the terminal class or keeps the behaviour it
// had before the gate existed. Repeated probe read failures are deliberately
// absent: they are knowable only after arming, and a pane that stays unreadable
// is exactly where the native hook must still be trusted at the deadline.
func (h *Handler) terminalStopHoldArmableLocked(req protocol.Request) bool {
	if !h.stopConfirmEnabled || h.stopConfirmDelay <= 0 {
		return false
	}
	if h.kittyClient == nil {
		// No screen read is possible, so nothing could ever confirm the stop.
		return false
	}
	if req.PipeMode {
		// Non-interactive children are excluded from probing, so a hold would
		// always ride to the deadline for nothing.
		return false
	}
	if !harnessHasStopProbe(stopHoldTargetHarness(req)) {
		return false
	}
	// Reduce already masks "stop" while an ASL lease is live, so the icon
	// outcome is identical and the hold would only add latency.
	if st, ok := h.windowStateLocked(req.WindowID); ok && st.Effective.Looping {
		return false
	}
	return true
}

// harnessHasStopProbe reports whether the harness owning the target pane has an
// idle stop probe able to confirm a held stop.
func harnessHasStopProbe(name string) bool {
	switch harness.NormalizeKnown(name) {
	case harness.Claude, harness.Codex, harness.Copilot:
		return true
	default:
		return false
	}
}

// stopHoldTargetHarness resolves the pane the stop is being held for. Direct
// legacy state_update requests predate the producer/target split and use
// Harness for both roles.
func stopHoldTargetHarness(req protocol.Request) string {
	if req.TargetHarnessSet {
		return req.TargetHarness
	}
	return req.Harness
}
