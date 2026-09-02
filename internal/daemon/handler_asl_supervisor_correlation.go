package daemon

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func (h *Handler) correlateASLSupervisorQuestion(windowID int, now time.Time) {
	h.stateMu.Lock()
	var notifications []aslSupervisorNotification
	for ref, projection := range h.aslSupervisorProjections {
		if ref.windowID != windowID || projection.UnansweredQuestions <= 0 {
			continue
		}
		target, ok := h.aslSupervisorTargets[ref]
		if !ok {
			continue
		}
		if notification := h.applyASLSupervisorQuestionLocked(now, target, projection); notification != nil {
			notifications = append(notifications, *notification)
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
	target aslSupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
) *aslSupervisorNotification {
	st, ok := h.windowStateLocked(target.windowID)
	if !ok || st.Reducer == nil || projection.UnansweredQuestions <= 0 {
		return nil
	}
	stop, ok := latestInteractiveSupervisorFact(st.Reducer, now.Unix())
	if !ok || !stop.terminated || stop.action != "stop" {
		return nil
	}
	terminalAt := stop.terminalAt
	if terminalAt == 0 {
		terminalAt = stop.at
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
			target.candidate.RunID,
			projection.LatestQuestionID,
		),
		firstNonEmpty(
			harnessFromSourceKey(stop.source.Key, stop.sessionID),
			st.LastHarness,
		),
		stop.sessionID,
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
	target aslSupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
	sourceEventID string,
	harnessName string,
	sessionID string,
	bypassCooldown bool,
	reason string,
) *aslSupervisorNotification {
	st, ok := h.windowStateLocked(target.windowID)
	if !ok || st.Reducer == nil || projection.UnansweredQuestions <= 0 {
		return nil
	}

	before := st.Reducer.Reduce(now.Unix())
	source := aslSupervisorQuestionSource(target.candidate.RunID)
	decision := st.Reducer.Apply(protocol.ReducerEvent{
		Source:         source,
		Action:         "elicitation",
		Seq:            projection.ProjectionSeq,
		At:             now.Unix(),
		OriginWindowID: target.originWindowID,
		RunID:          target.candidate.RunID,
	})
	if decision != protocol.DecisionApplied {
		return nil
	}
	st.ShadowSeq++
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(target.windowID, st, effective)
	h.syncRendererMetadataLocked(target.windowID)
	if effective != before {
		h.markWindowDirtyLocked(target.windowID)
	} else {
		h.markWindowActivityDirtyLocked(target.windowID)
	}
	h.appendWatchdogEventLocked(
		target.windowID,
		target.originWindowID,
		now,
		eventASLSupervisorQuestion,
		"elicitation",
		string(source.Kind)+":"+source.Key,
		reason,
	)

	if effective.Icon != "question" ||
		!h.shouldNotifySourceEventLocked(st, sourceEventID) {
		return nil
	}
	return &aslSupervisorNotification{
		windowID:       target.windowID,
		sessionTitle:   st.LastSessionTitle,
		account:        st.LastAccount,
		harness:        firstNonEmpty(harnessName, st.LastHarness),
		sessionID:      sessionID,
		runID:          target.candidate.RunID,
		sourceEventID:  sourceEventID,
		bypassCooldown: bypassCooldown,
	}
}

func latestInteractiveSupervisorFact(r *Reducer, now int64) (*sourceFact, bool) {
	var current *sourceFact
	for _, fact := range r.facts {
		identifiedHarness := fact.source.Kind == protocol.SourceHarness &&
			fact.sessionID != "" &&
			harnessFromSourceKey(fact.source.Key, fact.sessionID) != ""
		authoritativeStopProbe := fact.source.Kind == protocol.SourceProbe &&
			fact.terminated &&
			fact.action == "stop" &&
			isAuthoritativeSupervisorStopProbe(fact.source.Key)
		eligibleSource := identifiedHarness || authoritativeStopProbe
		if !eligibleSource || fact.pipeMode || !conversationFactLive(fact, now) {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
		}
	}
	return current, current != nil
}

func isAuthoritativeSupervisorStopProbe(key string) bool {
	switch key {
	case screenPromptProbeSource, claudeScreenStopProbeSource, copilotTranscriptStopProbeSource:
		return true
	default:
		return false
	}
}

func (h *Handler) dispatchASLSupervisorNotification(notification *aslSupervisorNotification) {
	if notification == nil {
		return
	}
	if notification.bypassCooldown && h.notifier != nil {
		h.notifier.ResetActionCooldown(notification.windowID, "elicitation")
	}
	h.maybeNotifyTraced(
		notification.windowID,
		notification.sessionTitle,
		notification.account,
		"elicitation",
		false,
		NotificationTrace{
			SourceEventID: notification.sourceEventID,
			Harness:       notification.harness,
			Profile:       notification.account,
			SessionID:     notification.sessionID,
			RunID:         notification.runID,
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
	ref := aslLeaseRef{windowID: windowID, key: runID}
	delete(h.aslSupervisorProjections, ref)
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		return
	}
	source := aslSupervisorQuestionSource(runID)
	if !st.Reducer.canRetract(source) {
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
	if windowID <= 0 || runID == "" {
		return
	}
	h.stateMu.Lock()
	ref := aslLeaseRef{windowID: windowID, key: runID}
	delete(h.aslSupervisorTargets, ref)
	delete(h.aslSupervisorClosureCutoffs, ref)
	seq := uint64(0)
	if st, ok := h.windowStateLocked(windowID); ok && st.Reducer != nil {
		if fact := st.Reducer.facts[aslSupervisorQuestionSource(runID)]; fact != nil {
			seq = fact.seq + 1
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
