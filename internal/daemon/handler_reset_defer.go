package daemon

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
)

// resetFlowStopDelay gives late reset markers a brief chance to reach the
// daemon before an ordinary stop hook paints a stop icon. It mirrors the
// hook-side transition delay while TypeStateUpdate remains in use.
var resetFlowStopDelay = 500 * time.Millisecond

type pendingResetStop struct {
	timer   *time.Timer
	seq     uint64
	claimed bool
}

func (h *Handler) deferResetSensitiveStop(req Request, payload hookevent.HookPayload) bool {
	if !shouldDeferResetSensitiveStop(req, payload) {
		return false
	}
	if resetFlowStopDelay <= 0 {
		return false
	}

	h.stateMu.Lock()
	if h.pendingResetStops == nil {
		h.pendingResetStops = make(map[int]pendingResetStop)
	}
	if pending, ok := h.pendingResetStops[req.WindowID]; ok && pending.timer != nil {
		pending.timer.Stop()
	}
	h.nextResetStopSeq++
	seq := h.nextResetStopSeq
	// Accepting the deferred stop is a window-scoped state transition even
	// though its reducer fact is not applied until the timer commits. Advance
	// the receipt now so an off-lock screen observation captured before this
	// stop cannot clear or cancel it as if no event had landed. A clear that
	// captures after this point may cancel this exact pending generation while
	// holding stateMu; replacing it with another deferred stop advances the
	// receipt again and makes that older clear observation stale.
	h.ensureWindowStateLocked(req.WindowID).ShadowSeq++
	timer := time.AfterFunc(resetFlowStopDelay, func() {
		h.commitDeferredResetStop(req, seq)
	})
	h.pendingResetStops[req.WindowID] = pendingResetStop{timer: timer, seq: seq}
	h.stateMu.Unlock()
	return true
}

// cancelPendingResetStop drops any deferred reset stop still waiting on the
// debounce timer for windowID. Called when a newer authoritative event
// supersedes the held stop so it can no longer fire and repaint a stale icon.
func (h *Handler) cancelPendingResetStop(windowID int) {
	h.resetStopCommitMu.Lock()
	defer h.resetStopCommitMu.Unlock()

	h.stateMu.Lock()
	h.cancelPendingResetStopLocked(windowID)
	h.stateMu.Unlock()
}

// cancelPendingResetStopLocked is the state-critical variant used when the
// pending or claimed stop must be removed atomically with another window
// mutation. Callers hold resetStopCommitMu and stateMu, in that order, so a
// callback cannot pass its final claim check until the superseding mutation
// has either canceled it or completed after it.
func (h *Handler) cancelPendingResetStopLocked(windowID int) {
	if pending, ok := h.pendingResetStops[windowID]; ok {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(h.pendingResetStops, windowID)
	}
}

// supersedesDeferredResetStop reports whether an incoming state update should
// cancel a stop still held in the reset-defer window. A fresh prompt (a new
// turn) or an explicit wipe (user_clear/clear) makes the held stop stale, so it
// must not paint. Reset-flow cleanup (req.ResetFlow) is deliberately excluded:
// the deferred stop is kept so applyResetFlowLocked can fold it into the same
// user_clear when its timer commits.
func supersedesDeferredResetStop(req Request) bool {
	if req.ResetFlow {
		return false
	}
	if req.IsPrompt {
		return true
	}
	switch req.Action {
	case "user_clear", "clear":
		return true
	default:
		return false
	}
}

func (h *Handler) commitDeferredResetStop(req Request, seq uint64) {
	h.stateMu.Lock()
	pending, ok := h.pendingResetStops[req.WindowID]
	if !ok || pending.seq != seq {
		h.stateMu.Unlock()
		return
	}
	// Keep the claimed generation addressable until its reducer application is
	// complete. A receipt-valid Codex clear that lands before the final commit
	// boundary can therefore cancel this callback even after its timer fired.
	pending.claimed = true
	h.pendingResetStops[req.WindowID] = pending
	h.stateMu.Unlock()

	if h.afterDeferredStopClaimForTest != nil {
		h.afterDeferredStopClaimForTest(req.WindowID, seq)
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
	pending, ok = h.pendingResetStops[req.WindowID]
	if !ok || pending.seq != seq || !pending.claimed {
		h.stateMu.Unlock()
		return
	}
	h.stateMu.Unlock()

	h.handleStateUpdate(req)

	h.stateMu.Lock()
	if pending, ok := h.pendingResetStops[req.WindowID]; ok && pending.seq == seq {
		delete(h.pendingResetStops, req.WindowID)
	}
	h.stateMu.Unlock()
}

func shouldDeferResetSensitiveStop(req Request, payload hookevent.HookPayload) bool {
	if req.Action != "stop" || req.ContextLimit || req.ResetFlow {
		return false
	}
	switch req.Event {
	case "Stop", "SessionEnd", "agentStop", "sessionEnd":
		return true
	case "Notification", "notification":
		return strings.EqualFold(strings.TrimSpace(payload.EffectiveNotificationType()), "agent_idle")
	default:
		return false
	}
}
