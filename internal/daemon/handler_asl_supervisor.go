package daemon

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// The Handler half of ASL supervision: polling registered lease records,
// deciding what a fresh projection does to a window, and consuming ASL's
// mailbox handoffs. Parsing those payloads and retaining the per-run
// bookkeeping live with the domain in internal/daemon/asl; every reducer
// mutation below stays here, under stateMu.

const eventASLSupervisorQuestion = "ASLSupervisorQuestion"

// aslSupervisorNotification is one settled question probe waiting to be
// announced. It is built under stateMu and dispatched after it is released, so
// a notification backend can never be called with the daemon's state lock held.
type aslSupervisorNotification struct {
	windowID       int
	sessionTitle   string
	account        string
	harness        string
	sessionID      string
	runID          string
	sourceEventID  string
	bypassCooldown bool
}

// registerASLSupervisorLifecycleTarget captures the state directory carried by
// an authenticated ASL lifecycle hook, so a run can be polled from the event
// that announced it. Reconciliation remains the fallback when an older producer
// omits state_path or the path cannot be canonicalized.
func (h *Handler) registerASLSupervisorLifecycleTarget(windowID, originWindowID int, runID, statePath string) {
	h.companionDirty()
	runID = strings.TrimSpace(runID)
	if windowID <= 0 || runID == "" {
		return
	}
	stateDir, ok := asl.LifecycleStateDir(statePath)
	if !ok {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.runtimeSlotClosedLocked(windowID) {
		return
	}
	h.aslSupervisors.Register(asl.SupervisorTarget{
		WindowID:       windowID,
		OriginWindowID: originWindowID,
		Candidate: agentloop.Candidate{
			RunID:    runID,
			StateDir: stateDir,
		},
	})
}

// refreshASLSupervisorQuestions polls only registered 512-byte lease records.
// Reads happen off stateMu; a target identity is rechecked before any result is
// accepted, so a run finishing or a window closing during I/O wins.
func (h *Handler) refreshASLSupervisorQuestions(now time.Time) {
	h.stateMu.Lock()
	targets := h.aslSupervisors.Targets()
	h.stateMu.Unlock()

	for _, target := range targets {
		projection, ok, err := agentloop.ReadSupervisorQuestionProjection(target.Candidate, now)
		if err != nil {
			slog.Debug("asl supervisor question refresh failed; preserving prior evidence",
				"window_id", target.WindowID,
				"run_id", target.Candidate.RunID,
				"err", err,
			)
			continue
		}
		if !ok {
			// Missing, v1, stale, malformed, or foreign evidence cannot raise a
			// new question and cannot safely clear one already observed.
			continue
		}
		h.acceptASLSupervisorProjection(now, target, projection)
	}
}

func (h *Handler) acceptASLSupervisorProjection(
	now time.Time,
	target asl.SupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
) {
	h.companionDirty()
	ref := asl.LeaseRef{WindowID: target.WindowID, Key: target.Candidate.RunID}
	h.stateMu.Lock()
	current, exists := h.aslSupervisors.Target(ref)
	if !exists || current.Candidate.StateDir != target.Candidate.StateDir ||
		current.Candidate.RunID != projection.RunID {
		h.stateMu.Unlock()
		return
	}
	if h.aslSupervisors.ClosureBlocks(ref, projection.ProjectionSeq) {
		h.stateMu.Unlock()
		return
	}

	if projection.UnansweredQuestions == 0 {
		h.aslSupervisors.ClearProjection(ref)
		h.clearASLSupervisorQuestionLocked(
			target.WindowID,
			target.Candidate.RunID,
			projection.ProjectionSeq,
			now,
			"fresh supervisor projection reports no unanswered questions",
		)
		h.stateMu.Unlock()
		return
	}

	previous, _ := h.aslSupervisors.Projection(ref)
	if st, ok := h.windowStateLocked(target.WindowID); ok && st.Reducer != nil {
		fact, known := st.Reducer.Fact(asl.QuestionSource(target.Candidate.RunID))
		if known && fact.Acked &&
			previous.LatestQuestionID == projection.LatestQuestionID {
			// F8 dismisses the current open set. Lease heartbeats and unrelated
			// mailbox records may advance ProjectionSeq, but only a different latest
			// question identity is allowed to re-arm the attention probe.
			h.aslSupervisors.SetProjection(ref, projection)
			h.stateMu.Unlock()
			return
		}
	}
	h.aslSupervisors.SetProjection(ref, projection)
	notification := h.applyASLSupervisorQuestionLocked(now, target, projection)
	h.stateMu.Unlock()
	h.dispatchASLSupervisorNotification(notification)
}

// handleASLSupervisorMailboxQuestion consumes ASL's body-free handoff as soon
// as outbox wait surfaces an actionable open set. Unlike the lease compatibility
// path, a direct handoff does not wait for a correlated harness Stop.
func (h *Handler) handleASLSupervisorMailboxQuestion(
	payload hookevent.HookPayload,
	baseReq protocol.Request,
	windows []kitty.OSWindow,
	now time.Time,
) {
	h.companionDirty()
	question, ok := asl.ParseMailboxQuestion(payload)
	if !ok {
		return
	}
	targets, _ := h.aslHookTargets(baseReq, windows, payload)
	for _, routeTarget := range targets {
		targetCtx := h.loopTargetContextFor(routeTarget)
		profile := targetCtx.profile
		harnessName := targetCtx.harness
		originWindowID := aslHookOriginWindow(baseReq, payload)
		target := question.Target(routeTarget.windowID, originWindowID)
		projection := question.Projection(now)

		h.stateMu.Lock()
		ref := asl.LeaseRef{WindowID: target.WindowID, Key: question.RunID}
		if h.aslSupervisors.ClosureBlocks(ref, question.ProjectionSeq) {
			h.stateMu.Unlock()
			continue
		}
		if h.runtimeSlotClosedLocked(target.WindowID) {
			// The session ended after its hook was translated; a question
			// must not reopen its closed slot or notify about it.
			h.stateMu.Unlock()
			continue
		}
		st := h.ensureWindowStateLocked(target.WindowID)
		if st.Reducer == nil {
			st.Reducer = state.NewReducer(h.reducerCfg)
		}
		if st.LastSessionTitle == "" {
			st.LastSessionTitle = targetCtx.sessionTitle
		}
		if st.LastAccount == "" {
			st.LastAccount = profile
		}
		if st.LastHarness == "" {
			st.LastHarness = firstNonEmpty(harnessName, baseReq.Harness, payload.Harness)
		}
		h.aslSupervisors.Register(target)
		previous, _ := h.aslSupervisors.Projection(ref)
		if fact, known := st.Reducer.Fact(asl.QuestionSource(question.RunID)); known && fact.Acked &&
			previous.LatestQuestionID == projection.LatestQuestionID {
			h.aslSupervisors.SetProjection(ref, projection)
			h.stateMu.Unlock()
			continue
		}
		h.aslSupervisors.SetProjection(ref, projection)
		notification := h.applyASLSupervisorQuestionEvidenceLocked(
			now,
			target,
			projection,
			question.SourceEventID,
			firstNonEmpty(harnessName, baseReq.Harness, payload.Harness, st.LastHarness),
			"",
			true,
			fmt.Sprintf(
				"ASL outbox wait surfaced %d unanswered mailbox question(s), latest id %s",
				projection.UnansweredQuestions,
				projection.LatestQuestionID,
			),
		)
		h.stateMu.Unlock()
		h.dispatchASLSupervisorNotification(notification)
	}
}

// handleASLSupervisorMailboxClosure consumes ASL's authoritative, text-free
// reply/resolution fast-clear marker. The producer emits it only after its
// post-append snapshot proves the run has no remaining open questions.
func (h *Handler) handleASLSupervisorMailboxClosure(
	event string,
	payload hookevent.HookPayload,
	now time.Time,
) {
	h.companionDirty()
	closure, ok := asl.ParseMailboxClosure(event, payload)
	if !ok {
		return
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, ref := range h.aslSupervisors.RunTargets(closure.RunID, closure.StateDir) {
		h.aslSupervisors.RaiseClosureCutoff(ref, closure.ClosureSeq)
		projection, known := h.aslSupervisors.Projection(ref)
		if !known || projection.ProjectionSeq > closure.ClosureSeq {
			continue
		}
		h.clearASLSupervisorQuestionLocked(
			ref.WindowID,
			closure.RunID,
			closure.ClosureSeq,
			now,
			fmt.Sprintf(
				"ASL %s closed the surfaced mailbox question set at question %s",
				closure.Label,
				closure.QuestionID,
			),
		)
	}
}

// correlateASLSupervisorQuestion re-evaluates cached projections after a clean
// interactive Stop. It performs no filesystem I/O and is therefore safe on the
// hook path; the periodic refresh handles the inverse ordering where Stop
// arrives before the projection.

func (h *Handler) correlateASLSupervisorQuestion(windowID int, now time.Time) {
	h.stateMu.Lock()
	var notifications []aslSupervisorNotification
	for _, pendingQuestion := range h.aslSupervisors.PendingForWindow(windowID) {
		if pending := h.applyASLSupervisorQuestionLocked(now, pendingQuestion.Target, pendingQuestion.Projection); pending != nil {
			notifications = append(notifications, *pending)
		}
	}
	h.stateMu.Unlock()
	for i := range notifications {
		h.dispatchASLSupervisorNotification(&notifications[i])
	}
}

// applyASLSupervisorQuestionLocked applies the text-free question probe only
// when the newest trusted interactive fact is a clean Stop no older than the
// pending mailbox question. The worker's ASL waiting phase contributes only
// the live loop lease; this correlated probe is the sole owner of the
// human-actionable question icon and its notification deduplication.
func (h *Handler) applyASLSupervisorQuestionLocked(
	now time.Time,
	target asl.SupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
) *aslSupervisorNotification {
	st, ok := h.windowStateLocked(target.WindowID)
	if !ok || st.Reducer == nil || projection.UnansweredQuestions <= 0 {
		return nil
	}
	stop, ok := latestInteractiveSupervisorFact(st.Reducer, now.Unix())
	if !ok || !stop.Terminated || stop.Action != "stop" {
		return nil
	}
	terminalAt := stop.TerminalAt
	if terminalAt == 0 {
		terminalAt = stop.At
	}
	if terminalAt < projection.LatestQuestionCreatedAt.Unix() {
		return nil
	}
	return h.applyASLSupervisorQuestionEvidenceLocked(
		now,
		target,
		projection,
		fmt.Sprintf(
			"asl-supervisor:%s:%s",
			target.Candidate.RunID,
			projection.LatestQuestionID,
		),
		firstNonEmpty(
			state.HarnessFromSourceKey(stop.Key, stop.SessionID),
			st.LastHarness,
		),
		stop.SessionID,
		false,
		fmt.Sprintf(
			"clean supervisor stop correlated with %d unanswered ASL mailbox question(s), latest id %s",
			projection.UnansweredQuestions,
			projection.LatestQuestionID,
		),
	)
}

// applyASLSupervisorQuestionEvidenceLocked owns the common reducer projection
// used by both the immediate outbox handoff and the clean-Stop lease fallback.
func (h *Handler) applyASLSupervisorQuestionEvidenceLocked(
	now time.Time,
	target asl.SupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
	sourceEventID string,
	harnessName string,
	sessionID string,
	bypassCooldown bool,
	reason string,
) *aslSupervisorNotification {
	st, ok := h.windowStateLocked(target.WindowID)
	if !ok || st.Reducer == nil || projection.UnansweredQuestions <= 0 {
		return nil
	}

	before := st.Reducer.Reduce(now.Unix())
	source := asl.QuestionSource(target.Candidate.RunID)
	decision := st.Reducer.Apply(protocol.ReducerEvent{
		Source:         source,
		Action:         "elicitation",
		Seq:            projection.ProjectionSeq,
		At:             now.Unix(),
		OriginWindowID: target.OriginWindowID,
		RunID:          target.Candidate.RunID,
	})
	if decision != protocol.DecisionApplied {
		return nil
	}
	// A supervisor question is real evidence: it retires the operator's forced
	// ⚡ so answering it does not fall back to the mark.
	st.Reducer.RetireUserActive(now.Unix())
	st.ShadowSeq++
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(target.WindowID, st, effective)
	h.syncRendererMetadataLocked(target.WindowID)
	if effective != before {
		h.markWindowDirtyLocked(target.WindowID)
	} else {
		h.markWindowActivityDirtyLocked(target.WindowID)
	}
	h.appendWatchdogEventLocked(
		target.WindowID,
		target.OriginWindowID,
		now,
		eventASLSupervisorQuestion,
		"elicitation",
		string(source.Kind)+":"+source.Key,
		reason,
	)

	if effective.Icon != "question" ||
		!st.MarkSourceEventNotified(sourceEventID) {
		return nil
	}
	return &aslSupervisorNotification{
		windowID:       target.WindowID,
		sessionTitle:   st.LastSessionTitle,
		account:        st.LastAccount,
		harness:        firstNonEmpty(harnessName, st.LastHarness),
		sessionID:      sessionID,
		runID:          target.Candidate.RunID,
		sourceEventID:  sourceEventID,
		bypassCooldown: bypassCooldown,
	}
}

func latestInteractiveSupervisorFact(r *state.Reducer, now int64) (protocol.SourceFactSnapshot, bool) {
	return r.LatestLiveFact(now, func(fact protocol.SourceFactSnapshot) bool {
		identifiedHarness := fact.Kind == protocol.SourceHarness &&
			fact.SessionID != "" &&
			state.HarnessFromSourceKey(fact.Key, fact.SessionID) != ""
		authoritativeStopProbe := fact.Kind == protocol.SourceProbe &&
			fact.Terminated &&
			fact.Action == "stop" &&
			isAuthoritativeSupervisorStopProbe(fact.Key)
		return (identifiedHarness || authoritativeStopProbe) && !fact.PipeMode
	})
}

func isAuthoritativeSupervisorStopProbe(key string) bool {
	switch key {
	case screenPromptProbeSource, claudeScreenStopProbeSource, copilotTranscriptStopProbeSource:
		return true
	default:
		return false
	}
}

func (h *Handler) dispatchASLSupervisorNotification(pending *aslSupervisorNotification) {
	if pending == nil {
		return
	}
	if pending.bypassCooldown && h.notifier != nil {
		h.notifier.ResetActionCooldown(pending.windowID, "elicitation")
	}
	h.maybeNotifyTraced(
		pending.windowID,
		pending.sessionTitle,
		pending.account,
		"elicitation",
		false,
		notification.NotificationTrace{
			SourceEventID: pending.sourceEventID,
			Harness:       pending.harness,
			Profile:       pending.account,
			SessionID:     pending.sessionID,
			RunID:         pending.runID,
		},
	)
}

// clearASLSupervisorQuestionLocked retracts this run's probe and removes its
// cached projection. It preserves the source's sequence cutoff, so polling an
// unchanged lease or restoring acknowledged state cannot resurrect the icon.
func (h *Handler) clearASLSupervisorQuestionLocked(
	windowID int,
	runID string,
	seq uint64,
	now time.Time,
	reason string,
) {
	h.companionDirty()
	h.aslSupervisors.ClearProjection(asl.LeaseRef{WindowID: windowID, Key: runID})
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		return
	}
	source := asl.QuestionSource(runID)
	if !st.Reducer.CanRetract(source) {
		return
	}
	before := st.Reducer.Reduce(now.Unix())
	if !st.Reducer.Retract(source, seq, now.Unix()) {
		return
	}
	st.ShadowSeq++
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
	h.syncRendererMetadataLocked(windowID)
	if effective != before {
		h.markWindowDirtyLocked(windowID)
	} else {
		h.markWindowActivityDirtyLocked(windowID)
	}
	h.appendWatchdogEventLocked(
		windowID,
		windowID,
		now,
		eventASLSupervisorQuestion,
		"idle",
		string(source.Kind)+":"+source.Key,
		reason,
	)
}

func (h *Handler) finishASLSupervisorQuestion(windowID int, runID string, now time.Time) {
	h.companionDirty()
	if windowID <= 0 || runID == "" {
		return
	}
	h.stateMu.Lock()
	h.aslSupervisors.Retire(asl.LeaseRef{WindowID: windowID, Key: runID})
	seq := uint64(0)
	if st, ok := h.windowStateLocked(windowID); ok && st.Reducer != nil {
		if fact, known := st.Reducer.Fact(asl.QuestionSource(runID)); known {
			seq = fact.Seq + 1
		}
	}
	h.clearASLSupervisorQuestionLocked(
		windowID,
		runID,
		seq,
		now,
		"ASL run finished; supervisor question projection retired",
	)
	h.stateMu.Unlock()
}
