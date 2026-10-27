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

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
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
func reduceNormalizedForRequest(req protocol.Request, outcome applyOutcome) (protocol.NormalizedEvent, bool, string, bool) {
	if req.Normalized != nil && !req.Normalized.Invalid {
		n := *req.Normalized
		// The legacy machine may have rewritten the action after the stamp was
		// taken (reset-flow stops become user_clear; copilot compact idles
		// become synthetic stops). Fold what actually happened. ASL phase
		// events are identity-carrying and never rewritten, and neither are
		// subagent lifecycle stamps: normalization deliberately rewrote a
		// clean stop to end the agent's own contribution, and the legacy
		// machine's "active" repaint must not undo that.
		// An operator's user_active is never rewritten: it must reach the
		// reducer as the forced ⚡ it asked for.
		if outcome.reduceAction != "" && n.Phase == "" && n.Action != "" &&
			n.Action != "user_active" && n.Action != outcome.reduceAction &&
			n.Source.Kind != protocol.SourceSubagent && n.SubagentDelta == 0 {
			n.Action = outcome.reduceAction
		}
		return n, true, events.IdentityConfidenceFor(n), false
	}
	untrustedFallback := req.Normalized != nil && req.Normalized.Invalid
	if req.Action == "loop_marker" {
		phase := events.ASLPhaseForEvent(req.Event)
		if phase == "" {
			return protocol.NormalizedEvent{}, false, "", untrustedFallback
		}
		n := protocol.NormalizedEvent{
			Source: protocol.SourceID{Kind: protocol.SourceASL, Key: protocol.WindowKey(req.WindowID)},
			Event:  req.Event,
			Phase:  phase,
		}
		if phase == state.ASLPhasePaused {
			n.Action = events.ASLPausedAttentionAction(req.Reason)
		}
		return n, true, protocol.IdentityConfidenceDaemon, untrustedFallback
	}
	action := outcome.reduceAction
	if action == "" {
		action = req.Action
	}
	if action == "" {
		return protocol.NormalizedEvent{}, false, "", untrustedFallback
	}
	// Un-stamped subagent lifecycle requests (legacy state_update clients,
	// direct calls) carry no agent or session identity; aggregate them as a
	// balanced multiset on the window's legacy bucket so parent activity on
	// the plain window source can never overwrite the 🤖 they contribute.
	if action == "subagent" || events.IsSubagentStopEvent(req.Event) {
		n := protocol.NormalizedEvent{
			Source: protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey(protocol.WindowKey(req.WindowID))},
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
		confidence := protocol.IdentityConfidenceLegacySynthesized
		if untrustedFallback {
			confidence = protocol.IdentityConfidenceUntrustedFallback
		}
		return n, true, confidence, untrustedFallback
	}
	source := protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.WindowKey(req.WindowID)}
	if action == "user_active" {
		// The TUI's mark arrives un-stamped; it still needs the synthetic
		// source a later real event retires.
		source = protocol.UserActiveSource()
	}
	n := protocol.NormalizedEvent{
		Source: source,
		Action: action,
		Event:  req.Event,
		Prompt: req.IsPrompt,
		// Identity-less clients have no session boundary: mirror the legacy
		// machine's terminal self-heal by letting any terminal action end the
		// window's sessions, so lost subagent stops cannot strand 🤖.
		EndsSession: protocol.IsTerminalAction(action),
	}
	confidence := protocol.IdentityConfidenceLegacySynthesized
	if untrustedFallback {
		confidence = protocol.IdentityConfidenceUntrustedFallback
	}
	return n, true, confidence, untrustedFallback
}

// reduceStateUpdate folds one settled request into the window's reducer and
// publishes the reduced effective state as the authoritative visible state.
// It also generates the notification decision for the transition and records
// any divergence from the legacy outcome for export.
func (h *Handler) reduceStateUpdate(req protocol.Request, outcome applyOutcome, resp protocol.Response, now time.Time) {
	if req.WindowID <= 0 {
		return
	}
	n, ok, identityConfidence, usedFallbackIdentity := reduceNormalizedForRequest(req, outcome)
	if !ok {
		return
	}
	if n.SourceEventID == "" {
		n.SourceEventID = req.SourceEventID
	}

	notify := false
	h.stateMu.Lock()
	// applyStateUpdate checked the slot under a lock it has since released. A
	// session that ended in between closed the slot, and folding the event now
	// would bring its state back where nothing would ever drop it again.
	if h.runtimeSlotClosedLocked(req.WindowID) {
		h.stateMu.Unlock()
		return
	}
	st := h.ensureWindowStateLocked(req.WindowID)
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	// A real hook/ASL event is the only evidence that re-arms one-shot busy-body
	// promotion. The probe applies its own fact directly through st.Reducer.Apply
	// and never reaches here, so it cannot re-arm itself. An operator clear is
	// not activity and never re-arms. Clearing a settled icon (🛑, attention)
	// acknowledges whatever the pane shows, stale title included, so it holds
	// promotion outright — otherwise the unchanged title would re-promote ⚡ on
	// the next tick and the idle probe would settle that back into the 🛑 just
	// cleared. Clearing ⚡ leaves the one-shot as it was: a hook-backed turn
	// still in progress may recover ⚡ from its title, while a title that has
	// already promoted once stays spent.
	//
	// An operator's user_active (F11) is not activity either: it leaves any
	// hold in place so the stale title cannot re-promote once the forced ⚡
	// ages out. Every real event retires that forced ⚡ before it applies, so
	// the event itself (Stop → 🛑, a prompt → the harness's ⚡) decides the
	// icon. Probe facts apply directly and never reach here; the screen probe
	// retires the mark itself, and only once it has seen the pane working since
	// the press (see probeScreenIdle).
	switch {
	case n.Action == "user_active":
		st.ResetUserActiveArm()
	case n.Action != "user_clear":
		st.ClearPromotedBusyBody()
		if n.Deliverable() {
			st.Reducer.RetireUserActive(now.Unix())
		}
	case st.Effective.Icon != "bolt":
		st.HoldBusyPromotion()
	}
	effectiveBefore := st.Effective
	// Today's harness payloads carry no source_sequence; assign the window's
	// receipt sequence so per-source monotonicity (duplicate/reorder handling)
	// still holds. Sequenced events advance the counter instead, so a later
	// sequence-less event can never be shadowed by an earlier sequenced one.
	seqBeforeApply := st.ShadowSeq
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
	reducerTrace, _ := st.Reducer.LatestTrace()
	reducerMutated := decision == protocol.DecisionApplied || decision == protocol.DecisionAcknowledged || decision == protocol.DecisionSuppressed
	// ShadowSeq is the window-wide receipt generation applyAuthoritativeIdle
	// captures before a slow off-lock screen/transcript read and re-checks under
	// the lock: any reducer mutation since capture must bump it so a stale idle
	// probe is discarded rather than allowed to retire newer work. The block
	// above only advances it for a sequence-less or higher explicit sequence, so
	// an accepted (or acknowledged) event carrying a per-source sequence at or
	// below the current window value would otherwise leave the generation
	// unchanged — letting a different source mutate the reducer between an idle
	// probe's evidence capture and application without invalidating it. Advance
	// the generation here on every real fact mutation, independently of the
	// event's per-source sequence and without rewriting the fact's own wire seq
	// (n.Seq is already folded in), so per-source monotonicity is preserved. A
	// suppressed decision (idle preserving an attention/terminal icon, or a
	// terminated source dropping trailing activity) still advances the mutated
	// source's per-source sequence, so it is a real mutation and participates in
	// the invalidation rule too; duplicate, reordered, and quarantined decisions
	// change no fact and are deliberately excluded so they never spuriously
	// advance the generation.
	if reducerMutated && st.ShadowSeq == seqBeforeApply {
		st.ShadowSeq++
	}
	compacted := st.Reducer.Compact(now.Unix())
	effective := st.Reducer.Reduce(now.Unix())

	// Capture the legacy render decision before overwriting it: divergence is
	// exported as evidence for retiring the legacy scalar machine.
	legacyIcon := rendering.DesiredIcon(st)
	legacyLooping := st.Looping

	changed := effective != st.Effective
	// Cutover: the reduced effective state is the visible state.
	h.publishEffectiveLocked(req.WindowID, st, effective)
	if n.Action == "user_clear" {
		// A manual wipe also dismisses a restored or externally stranded loop
		// marker. Only a live reducer lease keeps the marker authoritative.
		st.Looping = effective.Looping
		st.LoopingSet = true
	}

	// publishEffectiveLocked already stamped Shadow.Effective*; meta is
	// re-read here only to record divergence evidence.
	meta := h.windowMetadata[req.WindowID]

	if effective.Icon != legacyIcon || st.Looping != legacyLooping {
		meta.Shadow.MismatchCount++
		meta.Shadow.Mismatches = append(meta.Shadow.Mismatches, protocol.ShadowMismatch{
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
	if decision == protocol.DecisionApplied && notification.IsNotifyableAction(outcome.notifyAction) &&
		st.MarkSourceEventNotified(n.SourceEventID) {
		notify = true
	}
	// Persist reducer changes with the next render pass. A suppressed decision
	// still advances the affected source's durable sequence cutoff, so it is a
	// real mutation even though the contributed icon is preserved. Duplicates,
	// reordered deliveries, and quarantined/inert events remain clean. Only a
	// change to what the user actually sees (icon or loop marker vs the
	// pre-reduce visible state) queues an immediate render; fact-only mutations
	// are batched persistence.
	visual := effective.Icon != legacyIcon || st.Looping != legacyLooping
	if reducerMutated || compacted > 0 || changed || visual {
		h.syncRendererMetadataLocked(req.WindowID)
		if visual {
			h.markWindowDirtyLocked(req.WindowID)
		} else {
			h.markWindowActivityDirtyLocked(req.WindowID)
		}
	}
	// A real reducer event (new activity, a protected-state change, or a user
	// clear) resets any in-flight idle-sample stabilization for the window: the
	// screen has changed, so an adapter's consecutive-idle streak must restart
	// from zero rather than settle stale work over the fresh state.
	if decision == protocol.DecisionApplied || decision == protocol.DecisionAcknowledged {
		h.clearIdleSamplesForWindowLocked(req.WindowID)
	}
	completedEvent := h.finalizeReducerDecisionLocked(
		req, n, reducerTrace, effectiveBefore, effective,
		identityConfidence, usedFallbackIdentity, outcome,
	)
	h.stateMu.Unlock()
	h.recordDecisionEvent(completedEvent)

	switch {
	case decision == protocol.DecisionAcknowledged:
		h.cancelPendingNotification(req.WindowID)
	case notify:
		h.maybeNotifyTraced(req.WindowID, req.SessionTitle, req.Account, outcome.notifyAction, req.PipeMode, notificationTraceFromEvent(completedEvent))
	}
	if decision == protocol.DecisionApplied &&
		(n.Source.Kind == protocol.SourceHarness || n.Source.Kind == protocol.SourceLegacy) &&
		n.Action == "stop" && !n.PipeMode {
		h.correlateASLSupervisorQuestion(req.WindowID, now)
	}
}

// publishEffectiveLocked writes the reduced effective state as the window's
// authoritative visible state. Looping is only asserted when the reducer
// holds ASL knowledge; discovery-driven loop markers (restored titles,
// sibling routing bookkeeping) keep their writers otherwise. Icon or Unseen
// changes stamp one shared PresentationChangedAt; source and looping churn at
// the same icon do not. It also stamps the shadow export metadata: the
// shadow_current_mismatch invariant compares
// the renderer's desired icon against Shadow.EffectiveIcon, so every publish
// path — events, restore, watchdog sweeps, ASL reconciliation, screen probes
// — must move both together or the non-event paths pin a false-positive
// doctor/audit failure until the window's next real event (observed live
// twice: restored windows after a restart, and watchdog-expired windows
// during the 30-minute monitored audit). Callers hold stateMu.
func (h *Handler) publishEffectiveLocked(windowID int, st *state.WindowState, effective state.EffectiveState) {
	previousIcon := st.Effective.Icon
	previousUnseen := st.Unseen
	now := time.Now()
	// The unseen bit is a transition rule, not an invariant of the new icon
	// (see stampUnseenLocked), so it must read st.Effective.Icon while that is
	// still the PREVIOUS icon — before the write below overwrites it.
	stampUnseenLocked(st, effective.Icon, h.windowFocusedFromSnapshotLocked(windowID))
	if effective.Icon != previousIcon || st.Unseen != previousUnseen {
		// One publication owns one transition instant even when the icon and unseen
		// bit change together.
		st.PresentationChangedAt = now
	}
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
	stampBusySinceLocked(st, effective.Icon, now)
	stampPromptKindLocked(st, effective.Icon)
	// The screen idle probe only classifies a ⚡ window, so a decline streak
	// stops describing anything the instant the reduced icon leaves it. This runs
	// AFTER the metadata write above because it republishes the same entry.
	h.retractScreenDeclinesForIconLocked(windowID, effective.Icon)
	h.companionDirty()
}

// stampBusySinceLocked maintains the busy-stretch anchor as an idempotent
// invariant of the visible icon rather than as a transition rule: an
// active-class icon stamps only when the anchor is zero, and every other icon
// zeroes it. That gets each case right without any "previous state" reasoning
// — entering the class stamps because leaving it already zeroed, bolt →
// subagent → bolt inside one turn leaves the original anchor alone, and a
// restored anchor survives its first republish because it is non-zero.
// Reading the clock under stateMu matches markWindowStateDirtyLocked.
// Callers hold stateMu.
func stampBusySinceLocked(st *state.WindowState, icon string, now time.Time) {
	switch icon {
	case "bolt", "subagent", "compact":
		if st.BusySince.IsZero() {
			st.BusySince = now
		}
	default:
		st.BusySince = time.Time{}
	}
}

// promptKindBlockingIcon reports whether an icon is one a prompt reason may
// describe. The blocking class is the two icons a visible harness prompt can
// two icons a visible harness prompt can raise. Any other icon means no prompt
// is on screen, so no reason is available to explain the row.
func promptKindBlockingIcon(icon string) bool {
	return icon == "key" || icon == "question"
}

// stampPromptKindLocked maintains the blocked-reason string as an invariant of
// the visible icon, in stampBusySinceLocked's shape rather than
// stampUnseenLocked's: it needs no "previous state" reasoning because the
// question it answers — is a prompt on screen right now — is fully settled by
// the icon this publish installs.
//
// It is HALF a writer on purpose. It only ever clears; the kind itself is
// supplied by the screen probe that read it, under the same stateMu critical
// section that folded the prompt into the reducer (see
// recordCodexPromptProbeStateLocked). That split is what keeps a stale reason
// from outliving its prompt. A prompt kind is written on exactly one path — a
// probe that saw the prompt — but the icon can leave the blocking class on
// paths the probe never runs: a hook stop, a watchdog expiry, a process exit, an
// ASL reconciliation, a restore. Putting the clear at the choke point every one
// of those already moves through is what makes "the row shows no reason unless
// one is visible" a property rather than a list of remembered call sites.
//
// The kind is left alone while the icon stays blocking, which is what lets a
// re-probe of a still-visible prompt overwrite it with a fresher reading —
// including a different kind at the same icon, since command_approval and
// permission_grant both raise 🔑. Callers hold stateMu.
func stampPromptKindLocked(st *state.WindowState, icon string) {
	if !promptKindBlockingIcon(icon) {
		st.PromptKind = ""
	}
}

// stampUnseenLocked maintains the unseen bit from the icon this publish is about
// to install. Unlike stampBusySinceLocked it cannot be an invariant of the new
// icon alone, because "resting and unread" and "resting and already read" are the
// same icon: only the transition into rest carries the news. So it reads
// st.Effective.Icon as the PREVIOUS icon and must be called before
// publishEffectiveLocked overwrites it. Three rules, in the order a reader wants
// them:
//
//   - any icon that is not resting clears the bit — a window that went back to
//     work has nothing unread about it;
//   - entering rest from anywhere else records whether the user was looking:
//     unfocused means the finished turn went unwitnessed, focused means they
//     watched it land;
//   - resting and staying resting leaves the bit exactly as it is. This is the
//     load-bearing case. A republish is not new news, and the focus observation
//     clears the bit out of band, so re-deriving it here would let any watchdog
//     sweep, restore, or probe re-raise a row the user just cleared by looking
//     at it. It is also what makes a daemon restart quiet: a restored stop
//     republishes as stop and keeps the seen state it persisted with.
//
// It deliberately fires INDEPENDENTLY OF MUTE. Mute suppresses a notification;
// it says nothing about whether the user has seen the row, and a muted window
// that finished unwatched is exactly as unread as a loud one. herdr entangles
// the two (its completion transition assigns seen from the notification
// suppression flag) and inherits the bug where muting a pane silently marks its
// output read. Callers hold stateMu.
func stampUnseenLocked(st *state.WindowState, icon string, focused bool) {
	const resting = "stop"
	switch {
	case icon != resting:
		st.Unseen = false
	case st.Effective.Icon == resting:
		// Already at rest: not news. Leave the bit alone.
	default:
		st.Unseen = !focused
	}
}
