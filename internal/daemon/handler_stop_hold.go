package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// resetFlowStopDelay gives late reset markers a brief chance to reach the
// daemon before an ordinary stop hook paints a stop icon. It mirrors the
// hook-side transition delay while TypeStateUpdate remains in use. It is the
// release delay for the reset hold class only.
var resetFlowStopDelay = 500 * time.Millisecond

// defaultStopConfirmDelay and defaultStopConfirmTimeout mirror the
// [defaults.stop_confirm] config defaults so a Handler built without
// SetStopConfirm gates stops the way a configured daemon does. daemon.Run
// overrides both from config at startup.
const (
	defaultStopConfirmDelay   = 6 * time.Second
	defaultStopConfirmTimeout = 15 * time.Second
)

// stopHoldClass names why a stop is being held before it reaches the reducer.
// Each class owns its own release rule.
type stopHoldClass int

const (
	// stopHoldClassReset holds a reset-sensitive stop so a late reset marker
	// still has a chance to fold into the same transition.
	stopHoldClassReset stopHoldClass = iota
	// stopHoldClassTerminal holds a stop that would otherwise paint 🛑 while the
	// harness is still working on its own, until the confirmed-idle gate
	// releases it.
	stopHoldClassTerminal
)

// stopHoldRetryInterval is how often a terminal hold re-checks the confirmation
// gate once its quiet period has elapsed. It tracks the screen-probe cadence so
// a confirmation is acted on within one probe cycle instead of riding to the
// deadline.
const stopHoldRetryInterval = renderInterval

// pendingStopHold is one stop the handler is withholding from the reducer.
// quietUntil and deadline are the two halves of the terminal class's release
// rule: the hold commits only once the quiet period has elapsed *and* the
// harness's own idle probe has confirmed, with deadline as the hard convergence
// bound for a window nothing ever confirms.
type pendingStopHold struct {
	timer       *time.Timer
	seq         uint64
	claimed     bool
	class       stopHoldClass
	req         Request
	armedAt     time.Time
	quietUntil  time.Time
	deadline    time.Time
	confirmed   bool
	confirmedBy StopConfirmedBy
}

func (h *Handler) holdStopForConfirmation(req Request, payload hookevent.HookPayload) bool {
	if req.Action != "stop" {
		return false
	}

	h.stateMu.Lock()
	class, ok := stopHoldClassFor(req, payload, h.terminalStopHoldArmableLocked(req))
	if !ok {
		h.stateMu.Unlock()
		return false
	}
	delay := resetFlowStopDelay
	if class == stopHoldClassTerminal {
		delay = h.stopConfirmDelay
	}
	if delay <= 0 {
		h.stateMu.Unlock()
		return false
	}

	if h.pendingStopHolds == nil {
		h.pendingStopHolds = make(map[int]pendingStopHold)
	}
	previous, rearm := h.pendingStopHolds[req.WindowID]
	now := time.Now()
	armedAt := now
	if rearm {
		if previous.timer != nil {
			previous.timer.Stop()
		}
		// A re-arm replaces the stop being withheld, not the hold itself: the
		// window has been withholding continuously since the first arm, so the
		// original arm time keeps measuring the real wait and any idle evidence
		// already gathered for this pane stays valid.
		armedAt = previous.armedAt
	}
	// The quiet period restarts with each withheld stop, but the hard deadline
	// runs from the first arm: a harness that keeps re-firing Stop must not be
	// able to push convergence out indefinitely. The reset class has no second
	// gate, so its deadline is simply its release time.
	quietUntil := now.Add(delay)
	deadline := quietUntil
	if class == stopHoldClassTerminal {
		deadline = armedAt.Add(h.stopConfirmTimeout)
		if deadline.Before(quietUntil) {
			quietUntil = deadline
		}
	}
	h.nextStopHoldSeq++
	seq := h.nextStopHoldSeq
	if !rearm {
		// Accepting the held stop is a window-scoped state transition even though
		// its reducer fact is not applied until the timer commits. Advance the
		// receipt now so an off-lock screen observation captured before this stop
		// cannot clear or cancel it as if no event had landed. A clear that
		// captures after this point may cancel this exact pending generation while
		// holding stateMu; replacing it with another held stop advances the receipt
		// again and makes that older clear observation stale.
		//
		// Exactly one bump per hold, never per arm: over a multi-second terminal
		// hold every extra bump would discard the in-flight probe evidence the
		// gate depends on, and a re-arm caused by genuine new activity has already
		// advanced the receipt through the normal reducer path.
		h.ensureWindowStateLocked(req.WindowID).ShadowSeq++
	}
	timer := time.AfterFunc(quietUntil.Sub(now), func() {
		h.stopHoldTimerFired(req.WindowID, seq)
	})
	h.pendingStopHolds[req.WindowID] = pendingStopHold{
		timer:       timer,
		seq:         seq,
		class:       class,
		req:         req,
		armedAt:     armedAt,
		quietUntil:  quietUntil,
		deadline:    deadline,
		confirmed:   previous.confirmed,
		confirmedBy: previous.confirmedBy,
	}
	h.stateMu.Unlock()

	if class == stopHoldClassTerminal && !rearm {
		h.appendStopHoldEvent(req, StopHoldOutcomeHeld, 0)
	}
	return true
}

// stopHoldTimerFired is the single per-window timer callback. It runs the
// terminal class's AND gate first: a quiet period that elapsed without
// confirmation buys another probe interval rather than a commit.
func (h *Handler) stopHoldTimerFired(windowID int, seq uint64) {
	if h.rearmStopHoldForConfirmation(windowID, seq) {
		return
	}
	h.commitStopHold(windowID, seq)
}

// rearmStopHoldForConfirmation reports whether the fired hold went back to
// waiting instead of committing. Only stateMu is taken — the re-arm is pure
// timer bookkeeping and must never acquire resetStopCommitMu behind it, which
// would invert the commit path's lock order.
func (h *Handler) rearmStopHoldForConfirmation(windowID int, seq uint64) bool {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	pending, ok := h.pendingStopHolds[windowID]
	if !ok || pending.seq != seq || pending.claimed {
		return false
	}
	if pending.class != stopHoldClassTerminal || pending.confirmed {
		return false
	}
	now := time.Now()
	if !now.Before(pending.deadline) {
		return false
	}
	next := now.Add(stopHoldRetryInterval)
	if next.After(pending.deadline) {
		next = pending.deadline
	}
	if pending.timer != nil {
		pending.timer.Reset(next.Sub(now))
	}
	return true
}

// confirmStopHoldLocked records that the harness's own idle probe settled the
// window while a terminal stop was being withheld, and reports whether a hold
// took ownership of that evidence. A true result means the native stop is the
// one about to paint, so the probe must not settle a synthetic stop of its own.
//
// Confirmation is only half the gate: before the quiet period elapses it is
// recorded and nothing else, so the already-armed timer still decides at
// quietUntil. Afterwards there is nothing left to wait for and the commit is
// pulled in immediately. Re-confirming an already-confirmed hold is still a true
// result: the tab stays ⚡ until the commit lands, so the probe keeps observing
// the same idle evidence and must keep being told the held stop owns it.
// Callers hold stateMu.
func (h *Handler) confirmStopHoldLocked(windowID int, source SourceID) bool {
	pending, ok := h.pendingStopHolds[windowID]
	if !ok || pending.class != stopHoldClassTerminal {
		return false
	}
	pending.confirmed = true
	pending.confirmedBy = stopConfirmedByProbe(source)
	h.pendingStopHolds[windowID] = pending
	if !time.Now().Before(pending.quietUntil) && pending.timer != nil {
		pending.timer.Reset(0)
	}
	return true
}

// stopConfirmedByProbe spells one probe source as the release evidence of a held
// terminal stop. The three idle probe source keys are exactly the three probe
// members of the StopConfirmedBy vocabulary, so a released stop keeps the
// documented `probe:<harness>` provenance.
func stopConfirmedByProbe(source SourceID) StopConfirmedBy {
	return StopConfirmedBy(string(source.Kind) + ":" + source.Key)
}

// cancelStopHold drops any stop still waiting on the hold timer for windowID.
// Called when a newer authoritative event supersedes the held stop so it can no
// longer fire and repaint a stale icon.
func (h *Handler) cancelStopHold(windowID int) {
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	dropped, ok := h.cancelStopHoldLocked(windowID)
	h.stateMu.Unlock()
	h.resetStopCommitMu.Unlock()

	if ok {
		h.recordSupersededStopHold(dropped)
	}
}

// cancelStopHoldLocked is the state-critical variant used when the pending or
// claimed stop must be removed atomically with another window mutation. Callers
// hold resetStopCommitMu and stateMu, in that order, so a callback cannot pass
// its final claim check until the superseding mutation has either canceled it
// or completed after it. It returns the dropped hold so the caller can record
// the diagnostic row once both locks are released.
func (h *Handler) cancelStopHoldLocked(windowID int) (pendingStopHold, bool) {
	pending, ok := h.pendingStopHolds[windowID]
	if !ok {
		return pendingStopHold{}, false
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	delete(h.pendingStopHolds, windowID)
	return pending, true
}

// cancelStopHoldForProcessExitLocked retires a held stop only when the exiting
// process is the producer that emitted it. A window routinely carries several
// ordinary process groups at once — an interactive parent plus its `claude -p`
// children, or a sibling session — and settling one of them proves nothing about
// the others, so an uncorrelated exit must leave the hold armed with its original
// quiet period and deadline. Callers hold resetStopCommitMu and stateMu, in that
// order, and pass the window's pre-settlement facts.
func (h *Handler) cancelStopHoldForProcessExitLocked(
	windowID int,
	identity HarnessProcessIdentity,
	facts []SourceFactSnapshot,
) (pendingStopHold, bool) {
	pending, ok := h.pendingStopHolds[windowID]
	if !ok {
		return pendingStopHold{}, false
	}
	owner, known := heldStopProcessOwner(pending, facts)
	if !known || owner != identity {
		return pendingStopHold{}, false
	}
	return h.cancelStopHoldLocked(windowID)
}

// heldStopProcessOwner resolves which harness process produced a held stop. The
// delivery's own validated fingerprint is authoritative. An unstamped legacy
// delivery falls back to the reducer's ownership of the exact fact that stop
// would have updated, which is still a statement about that one producer rather
// than about the window. A stop whose producer cannot be identified either way
// keeps waiting on its own gate: converging at the deadline is correct, while
// attributing it to whichever process happened to exit is not.
func heldStopProcessOwner(pending pendingStopHold, facts []SourceFactSnapshot) (HarnessProcessIdentity, bool) {
	if pending.req.normalized == nil {
		return HarnessProcessIdentity{}, false
	}
	if identity, ok := canonicalHarnessProcessIdentity(pending.req.normalized.HarnessProcess); ok {
		return identity, true
	}
	for _, fact := range facts {
		if fact.Source() != pending.req.normalized.Source {
			continue
		}
		return canonicalHarnessProcessIdentity(HarnessProcessIdentity{
			Harness:    fact.HarnessProcessHarness,
			PID:        fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
	}
	return HarnessProcessIdentity{}, false
}

// recordSupersededStopHold writes the diagnostic row for a terminal hold a
// newer authoritative event retired before it could paint. Reset-class holds
// keep their pre-existing silent cancellation: they are a sub-second debounce,
// not a withheld terminal state a consumer needs to reason about.
func (h *Handler) recordSupersededStopHold(pending pendingStopHold) {
	if pending.class != stopHoldClassTerminal {
		return
	}
	h.appendStopHoldEvent(pending.req, StopHoldOutcomeSuperseded, time.Since(pending.armedAt))
}

// recordBypassedStopHold writes the diagnostic row for a terminal hold that a
// verified harness-process exit retired. The exit settles the window's terminal
// state itself, so unlike the confirmed and deadline releases there is no
// committed stop to stamp the outcome on.
func (h *Handler) recordBypassedStopHold(pending pendingStopHold) {
	if pending.class != stopHoldClassTerminal {
		return
	}
	h.appendStopHoldEvent(pending.req, StopHoldOutcomeBypassed, time.Since(pending.armedAt))
}

// supersedesStopHold reports whether an incoming state update should cancel a
// stop still held before the reducer. Any reducer-visible non-terminal action
// proves the harness is still working — a fresh prompt (a new turn), a tool
// call, a subagent, a compaction, a permission or elicitation prompt, a
// recoverable failure — as does an explicit wipe (user_clear/clear). The held
// stop is stale in every one of those cases, so it must not paint.
// Reset-flow cleanup (req.ResetFlow) is deliberately excluded: the held stop is
// kept so applyResetFlowLocked can fold it into the same user_clear when the
// hold commits.
func supersedesStopHold(req Request) bool {
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
		pending.confirmedBy = StopConfirmedByDeadline
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
		req.stopHoldWaitedMs = int(time.Since(pending.armedAt).Milliseconds())
		req.stopConfirmedBy = pending.confirmedBy
		req.stopHoldOutcome = StopHoldOutcomeDeadline
		if pending.confirmed {
			req.stopHoldOutcome = StopHoldOutcomeConfirmed
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
func (h *Handler) refreshHeldStopRequestLocked(req Request) Request {
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
	req.currentIconState = desiredIconForState(st)
	return req
}

// pendingTerminalStopSnapshotLocked is the export view of a stop this window is
// still withholding. Only the terminal class is exported: a reset hold is a
// 500ms debounce nothing can observe, while a terminal hold is a visible state
// the window is deliberately not showing yet, and the one whose failure to
// converge (stop_hold_stuck) is otherwise invisible. Callers hold stateMu.
func (h *Handler) pendingTerminalStopSnapshotLocked(windowID int) *PendingTerminalStop {
	pending, ok := h.pendingStopHolds[windowID]
	if !ok || pending.class != stopHoldClassTerminal {
		return nil
	}
	return &PendingTerminalStop{
		ArmedAt:    pending.armedAt.Unix(),
		QuietUntil: pending.quietUntil.Unix(),
		Deadline:   pending.deadline.Unix(),
		Confirmed:  pending.confirmed,
		Class:      StopHoldClassTerminal,
	}
}

// stopHoldDecisionReason maps a hold outcome to its stable decision reason.
// Only outcomes that end the hold without committing its stop get their own
// row; the release outcomes (confirmed, deadline) are stamped on the stop the
// hold commits.
func stopHoldDecisionReason(outcome StopHoldOutcome) (string, bool) {
	switch outcome {
	case StopHoldOutcomeHeld:
		return suppressionReasonTerminalStopHeld, true
	case StopHoldOutcomeSuperseded:
		return suppressionReasonTerminalStopSuperseded, true
	case StopHoldOutcomeBypassed:
		return suppressionReasonTerminalStopBypassed, true
	default:
		return "", false
	}
}

// appendStopHoldEvent records one synthetic EventTerminalStopHold row for a
// stop the handler is withholding from the reducer. Nothing is applied and no
// icon changes, so the row follows the suppressed-event append shape: the
// window's own effective state is both the before and after evidence.
func (h *Handler) appendStopHoldEvent(req Request, outcome StopHoldOutcome, waited time.Duration) {
	reason, ok := stopHoldDecisionReason(outcome)
	if !ok || req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	st := h.ensureWindowStateLocked(req.WindowID)
	effective := effectiveStateEvidence(st.Effective)
	event := suppressedSessionEvent(req, req.Action, reason, desiredIconForState(st))
	event.Event = EventTerminalStopHold
	event.StopHoldOutcome = outcome
	event.StopHoldWaitedMs = int(waited.Milliseconds())
	event.DecisionReason = reason
	event.ReducerDecision = DecisionSuppressed
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
func stopHoldClassFor(req Request, payload hookevent.HookPayload, terminalArmable bool) (stopHoldClass, bool) {
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
func resetSensitiveStop(req Request, payload hookevent.HookPayload) bool {
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
func (h *Handler) terminalStopHoldArmableLocked(req Request) bool {
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
func stopHoldTargetHarness(req Request) string {
	if req.targetHarnessSet {
		return req.targetHarness
	}
	return req.Harness
}
