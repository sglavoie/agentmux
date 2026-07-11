package daemon

// Authoritative reduction: every state-changing request that reaches
// handleStateUpdate is folded into the window's source-fact reducer, and the
// reducer's effective state is what the renderer paints (DesiredIcon +
// Looping). The legacy scalar machine still runs first — it owns harness
// quirk handling, guard bookkeeping, and the event log — but its render
// decision survives only as divergence evidence in the window's export
// metadata (WindowDebugShadow). Notification decisions are generated here
// from reducer transitions and deduplicated by upstream source event ID.
// handleStateUpdate runs either on the serialized ingress consumer or on a
// probe/overflow goroutine; all reducer access below happens under stateMu,
// so the non-concurrency-safe Reducer is never touched in parallel.

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
)

// maxShadowMismatches bounds the per-window mismatch ring kept for export.
// MismatchCount keeps counting past the bound.
const maxShadowMismatches = 50

// reduceNormalizedForRequest resolves the reducer's input for one request.
// Paths that hold the raw hook payload stamp a fully normalized event on the
// request; everything else (legacy state_update clients, screen probes,
// sweeps, direct test calls) degrades to a synthesized legacy-source event so
// terminal self-healing uses the guard-duration grace. A quarantined stamp
// (unreadable payload or malformed identity fields) also degrades to the
// synthesized legacy event: the fail-closed contract is that untrusted
// identity never enters the fact map, not that a Stop with a corrupt payload
// loses its icon. A loop_marker without a stamp synthesizes a window-keyed
// ASL lease event from the lifecycle event name so legacy loop clients keep
// their 🔁.
func reduceNormalizedForRequest(req Request, outcome applyOutcome) (NormalizedEvent, bool) {
	if req.normalized != nil && !req.normalized.Invalid {
		n := *req.normalized
		// The legacy machine may have rewritten the action after the stamp was
		// taken (reset-flow stops become user_clear; copilot compact idles
		// become synthetic stops). Fold what actually happened. ASL phase
		// events are identity-carrying and never rewritten, and neither are
		// subagent lifecycle stamps: normalization deliberately rewrote a
		// clean stop to end the agent's own contribution, and the legacy
		// machine's "active" repaint must not undo that.
		if outcome.reduceAction != "" && n.Phase == "" && n.Action != "" &&
			n.Action != outcome.reduceAction &&
			n.Source.Kind != SourceSubagent && n.SubagentDelta == 0 {
			n.Action = outcome.reduceAction
		}
		return n, true
	}
	if req.Action == "loop_marker" {
		phase := aslPhaseForEvent(req.Event)
		if phase == "" {
			return NormalizedEvent{}, false
		}
		n := NormalizedEvent{
			Source: SourceID{Kind: SourceASL, Key: windowKey(req.WindowID)},
			Event:  req.Event,
			Phase:  phase,
		}
		if phase == ASLPhasePaused {
			n.Action = aslPausedAttentionAction(req.Reason)
		}
		return n, true
	}
	action := outcome.reduceAction
	if action == "" {
		action = req.Action
	}
	if action == "" {
		return NormalizedEvent{}, false
	}
	// Un-stamped subagent lifecycle requests (legacy state_update clients,
	// direct calls) carry no agent or session identity; aggregate them as a
	// balanced multiset on the window's legacy bucket so parent activity on
	// the plain window source can never overwrite the 🤖 they contribute.
	if action == "subagent" || isSubagentStopEvent(req.Event) {
		n := NormalizedEvent{
			Source: SourceID{Kind: SourceLegacy, Key: legacySubagentBucketKey(windowKey(req.WindowID))},
			Event:  req.Event,
		}
		if action == "subagent" {
			n.Action, n.SubagentDelta = "subagent", 1
		} else {
			n.SubagentDelta = -1
			// A failure-carrying stop surfaces its sticky error; a clean stop
			// only rebalances the bucket.
			if action == "stop_failure" {
				n.Action = "stop_failure"
			} else {
				n.Action = "idle"
			}
		}
		return n, true
	}
	return NormalizedEvent{
		Source: SourceID{Kind: SourceLegacy, Key: windowKey(req.WindowID)},
		Action: action,
		Event:  req.Event,
		Prompt: req.IsPrompt,
		// Identity-less clients have no session boundary: mirror the legacy
		// machine's terminal self-heal by letting any terminal action end the
		// window's sessions, so lost subagent stops cannot strand 🤖.
		EndsSession: isTerminalAction(action),
	}, true
}

// reduceStateUpdate folds one settled request into the window's reducer and
// publishes the reduced effective state as the authoritative visible state.
// It also generates the notification decision for the transition and records
// any divergence from the legacy outcome for export.
func (h *Handler) reduceStateUpdate(req Request, outcome applyOutcome, resp Response, now time.Time) {
	if req.WindowID <= 0 {
		return
	}
	n, ok := reduceNormalizedForRequest(req, outcome)
	if !ok {
		return
	}

	notify := false
	var journalItem *activity.Item
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(req.WindowID)
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	// Today's harness payloads carry no source_sequence; assign the window's
	// receipt sequence so per-source monotonicity (duplicate/reorder handling)
	// still holds. Sequenced events advance the counter instead, so a later
	// sequence-less event can never be shadowed by an earlier sequenced one.
	if n.Seq == 0 {
		st.ShadowSeq++
		n.Seq = st.ShadowSeq
	} else if n.Seq > st.ShadowSeq {
		st.ShadowSeq = n.Seq
	}
	if n.At == 0 {
		n.At = now.Unix()
	}

	decision := st.Reducer.ApplyNormalized(n)
	compacted := st.Reducer.Compact(now.Unix())
	effective := st.Reducer.Reduce(now.Unix())

	// Capture the legacy render decision before overwriting it: divergence is
	// exported as evidence for retiring the legacy scalar machine.
	legacyIcon := desiredIconForState(st)
	legacyLooping := st.Looping

	changed := effective != st.Effective
	// Cutover: the reduced effective state is the visible state.
	h.publishEffectiveLocked(req.WindowID, st, effective)

	// publishEffectiveLocked already stamped Shadow.Effective*; meta is
	// re-read here only to record divergence evidence.
	meta := h.windowMetadata[req.WindowID]

	if effective.Icon != legacyIcon || st.Looping != legacyLooping {
		meta.Shadow.MismatchCount++
		meta.Shadow.Mismatches = append(meta.Shadow.Mismatches, ShadowMismatch{
			At:            now.Unix(),
			Event:         n.Event,
			Action:        n.Action,
			LegacyIcon:    legacyIcon,
			ShadowIcon:    effective.Icon,
			LegacyLooping: legacyLooping,
			ShadowLooping: st.Looping,
			SourceKind:    string(effective.Source.Kind),
			SourceKey:     effective.Source.Key,
		})
		if len(meta.Shadow.Mismatches) > maxShadowMismatches {
			meta.Shadow.Mismatches = meta.Shadow.Mismatches[len(meta.Shadow.Mismatches)-maxShadowMismatches:]
		}
		slog.Debug("reducer diverged from legacy state machine",
			"window_id", req.WindowID,
			"event", n.Event,
			"action", n.Action,
			"legacy_icon", legacyIcon,
			"effective_icon", effective.Icon,
			"legacy_looping", legacyLooping,
			"effective_looping", st.Looping,
		)
	}
	h.windowMetadata[req.WindowID] = meta

	// The reducer owns the suppression reason the event log records. A
	// reducer-suppressed trailing completion logs active_after_terminal
	// whether the legacy guard let it through or not; a guard-refused event
	// the reducer did not suppress falls back to the legacy guard_blocked
	// reason so the event still appears exactly once. Idle/clear preservation
	// is not logged: the legacy path already appended its own allowed event
	// with the same preserved outcome.
	if decision == DecisionSuppressed && n.Action != "idle" && n.Action != "clear" {
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, n.Action, suppressionReasonActiveAfterTerminal, legacyIcon))
	} else if outcome.guardBlocked {
		h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, n.Action, suppressionReasonGuardBlocked, legacyIcon))
	}

	if decision == DecisionApplied && notifyableActions[outcome.notifyAction] &&
		h.shouldNotifySourceEventLocked(st, n.SourceEventID) {
		notify = true
	}
	// Classify only after the reducer has accepted the normalized event. A
	// duplicate/reordered/suppressed delivery must never create journal noise,
	// and the current conversation is now the reducer's authoritative mapping.
	if identity, ok := st.CurrentConversation(now.Unix()); ok {
		if item, meaningful := meaningfulActivity(n, req, decision, identity.Harness, identity.SessionID, h.activityBootID, h.eventSeq, now); meaningful {
			journalItem = &item
		}
	} else if item, meaningful := meaningfulActivity(n, req, decision, "", "", h.activityBootID, h.eventSeq, now); meaningful {
		journalItem = &item
	}

	// Persist reducer changes with the next render pass. Duplicates and
	// suppressions leave the durable payload untouched, so only real fact
	// mutations (or a visible-state overwrite of the legacy decision) mark the
	// window dirty. Only a change to what the user actually sees (icon or loop
	// marker vs the pre-reduce visible state) queues an immediate render;
	// fact-only mutations are batched persistence.
	visual := effective.Icon != legacyIcon || st.Looping != legacyLooping
	if decision == DecisionApplied || decision == DecisionAcknowledged || compacted > 0 || changed || visual {
		h.syncRendererMetadataLocked(req.WindowID)
		if visual {
			h.markWindowDirtyLocked(req.WindowID)
		} else {
			h.markWindowActivityDirtyLocked(req.WindowID)
		}
	}
	h.stateMu.Unlock()
	if journalItem != nil {
		h.enqueueActivity(*journalItem)
	}

	switch {
	case decision == DecisionAcknowledged:
		h.cancelPendingNotification(req.WindowID)
	case notify:
		h.maybeNotify(req.WindowID, req.SessionTitle, req.Account, outcome.notifyAction, req.PipeMode)
	}
}

// publishEffectiveLocked writes the reduced effective state as the window's
// authoritative visible state. Looping is only asserted when the reducer
// holds ASL knowledge; discovery-driven loop markers (restored titles,
// sibling routing bookkeeping) keep their writers otherwise. It also stamps
// the shadow export metadata: the shadow_current_mismatch invariant compares
// the renderer's desired icon against Shadow.EffectiveIcon, so every publish
// path — events, restore, watchdog sweeps, ASL reconciliation, screen probes
// — must move both together or the non-event paths pin a false-positive
// doctor/audit failure until the window's next real event (observed live
// twice: restored windows after a restart, and watchdog-expired windows
// during the 30-minute monitored audit). Callers hold stateMu.
func (h *Handler) publishEffectiveLocked(windowID int, st *WindowState, effective EffectiveState) {
	if effective != st.Effective {
		st.Effective = effective
		st.EffectiveGen++
	}
	st.DesiredIcon = effective.Icon
	st.StateSet = true
	if st.Reducer != nil && st.Reducer.LoopStateKnown() {
		st.Looping = effective.Looping
		st.LoopingSet = true
	}
	meta := h.windowMetadata[windowID]
	meta.Shadow.EffectiveIcon = effective.Icon
	meta.Shadow.EffectiveLooping = effective.Looping
	meta.Shadow.EffectiveGen = st.EffectiveGen
	h.windowMetadata[windowID] = meta
}

// maxNotifiedSourceEvents bounds the per-window ring of upstream source event
// IDs that already produced a notification decision.
const maxNotifiedSourceEvents = 64

// shouldNotifySourceEventLocked reports whether a notification decision for
// this upstream source event ID has not been made yet, and records it. Events
// without an upstream ID are never deduplicated here (the notifier's own
// per-action cooldown still applies). Callers hold stateMu.
func (h *Handler) shouldNotifySourceEventLocked(st *WindowState, sourceEventID string) bool {
	if sourceEventID == "" {
		return true
	}
	if _, seen := st.notifiedSourceEvents[sourceEventID]; seen {
		return false
	}
	if st.notifiedSourceEvents == nil {
		st.notifiedSourceEvents = make(map[string]struct{}, maxNotifiedSourceEvents)
	}
	st.notifiedSourceEvents[sourceEventID] = struct{}{}
	st.notifiedSourceOrder = append(st.notifiedSourceOrder, sourceEventID)
	if len(st.notifiedSourceOrder) > maxNotifiedSourceEvents {
		oldest := st.notifiedSourceOrder[0]
		st.notifiedSourceOrder = st.notifiedSourceOrder[1:]
		delete(st.notifiedSourceEvents, oldest)
	}
	return true
}
