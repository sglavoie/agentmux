package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
	req         protocol.Request
	armedAt     time.Time
	quietUntil  time.Time
	deadline    time.Time
	confirmed   bool
	confirmedBy protocol.StopConfirmedBy
}

func (h *Handler) holdStopForConfirmation(req protocol.Request, payload hookevent.HookPayload) bool {
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
		h.appendStopHoldEvent(req, protocol.StopHoldOutcomeHeld, 0)
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
func (h *Handler) confirmStopHoldLocked(windowID int, source protocol.SourceID) bool {
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
func stopConfirmedByProbe(source protocol.SourceID) protocol.StopConfirmedBy {
	return protocol.StopConfirmedBy(string(source.Kind) + ":" + source.Key)
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
	identity protocol.HarnessProcessIdentity,
	facts []protocol.SourceFactSnapshot,
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
func heldStopProcessOwner(pending pendingStopHold, facts []protocol.SourceFactSnapshot) (protocol.HarnessProcessIdentity, bool) {
	if pending.req.Normalized == nil {
		return protocol.HarnessProcessIdentity{}, false
	}
	if identity, ok := protocol.CanonicalHarnessProcessIdentity(pending.req.Normalized.HarnessProcess); ok {
		return identity, true
	}
	for _, fact := range facts {
		if fact.Source() != pending.req.Normalized.Source {
			continue
		}
		return protocol.CanonicalHarnessProcessIdentity(protocol.HarnessProcessIdentity{
			Harness:    fact.HarnessProcessHarness,
			PID:        fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
	}
	return protocol.HarnessProcessIdentity{}, false
}

// recordSupersededStopHold writes the diagnostic row for a terminal hold a
// newer authoritative event retired before it could paint. Reset-class holds
// keep their pre-existing silent cancellation: they are a sub-second debounce,
// not a withheld terminal state a consumer needs to reason about.
func (h *Handler) recordSupersededStopHold(pending pendingStopHold) {
	if pending.class != stopHoldClassTerminal {
		return
	}
	h.appendStopHoldEvent(pending.req, protocol.StopHoldOutcomeSuperseded, time.Since(pending.armedAt))
}

// recordBypassedStopHold writes the diagnostic row for a terminal hold that a
// verified harness-process exit retired. The exit settles the window's terminal
// state itself, so unlike the confirmed and deadline releases there is no
// committed stop to stamp the outcome on.
func (h *Handler) recordBypassedStopHold(pending pendingStopHold) {
	if pending.class != stopHoldClassTerminal {
		return
	}
	h.appendStopHoldEvent(pending.req, protocol.StopHoldOutcomeBypassed, time.Since(pending.armedAt))
}
