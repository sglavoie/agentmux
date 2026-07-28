package daemon

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

const (
	aslSupervisorQuestionProbePrefix = "asl_supervisor:"
	eventASLSupervisorQuestion       = "ASLSupervisorQuestion"
	agentSessionLoopMailboxReply     = "AgentSessionLoopMailboxReply"
	aslMailboxReplyReason            = "mailbox_reply_appended"
)

// aslSupervisorTarget is one live ASL run whose adjacent fixed-size supervisor
// lease can be refreshed without rescanning the complete ASL state root.
type aslSupervisorTarget struct {
	windowID       int
	originWindowID int
	candidate      agentloop.Candidate
}

type aslSupervisorNotification struct {
	windowID      int
	sessionTitle  string
	account       string
	harness       string
	sessionID     string
	runID         string
	sourceEventID string
}

func aslSupervisorQuestionSource(runID string) SourceID {
	return SourceID{Kind: SourceProbe, Key: aslSupervisorQuestionProbePrefix + runID}
}

func (h *Handler) registerASLSupervisorTargetLocked(target aslSupervisorTarget) {
	if target.windowID <= 0 || target.candidate.RunID == "" || target.candidate.StateDir == "" {
		return
	}
	if target.originWindowID <= 0 {
		target.originWindowID = target.windowID
	}
	ref := aslLeaseRef{windowID: target.windowID, key: target.candidate.RunID}
	h.aslSupervisorTargets[ref] = target
}

// registerASLSupervisorLifecycleTarget captures the state directory carried by
// an authenticated ASL lifecycle hook. Reconciliation remains the fallback when
// an older producer omits state_path or the path cannot be canonicalized.
func (h *Handler) registerASLSupervisorLifecycleTarget(windowID, originWindowID int, runID, statePath string) {
	runID = strings.TrimSpace(runID)
	statePath = strings.TrimSpace(statePath)
	if windowID <= 0 || runID == "" || statePath == "" {
		return
	}
	canonical, err := agentloop.CanonicalizePath(statePath)
	if err != nil || filepath.Base(canonical) != "state.json" {
		return
	}
	h.stateMu.Lock()
	h.registerASLSupervisorTargetLocked(aslSupervisorTarget{
		windowID:       windowID,
		originWindowID: originWindowID,
		candidate: agentloop.Candidate{
			RunID:    runID,
			StateDir: filepath.Dir(canonical),
		},
	})
	h.stateMu.Unlock()
}

// refreshASLSupervisorQuestions polls only registered 512-byte lease records.
// Reads happen off stateMu; a target identity is rechecked before any result is
// accepted, so a run finishing or a window closing during I/O wins.
func (h *Handler) refreshASLSupervisorQuestions(now time.Time) {
	h.stateMu.Lock()
	targets := make([]aslSupervisorTarget, 0, len(h.aslSupervisorTargets))
	for _, target := range h.aslSupervisorTargets {
		targets = append(targets, target)
	}
	h.stateMu.Unlock()

	for _, target := range targets {
		projection, ok, err := agentloop.ReadSupervisorQuestionProjection(target.candidate, now)
		if err != nil {
			slog.Debug("asl supervisor question refresh failed; preserving prior evidence",
				"window_id", target.windowID,
				"run_id", target.candidate.RunID,
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
	target aslSupervisorTarget,
	projection agentloop.SupervisorQuestionProjection,
) {
	ref := aslLeaseRef{windowID: target.windowID, key: target.candidate.RunID}
	h.stateMu.Lock()
	current, exists := h.aslSupervisorTargets[ref]
	if !exists || current.candidate.StateDir != target.candidate.StateDir ||
		current.candidate.RunID != projection.RunID {
		h.stateMu.Unlock()
		return
	}
	if cutoff, ok := h.aslSupervisorReplyCutoffs[ref]; ok {
		if projection.ProjectionSeq <= cutoff {
			h.stateMu.Unlock()
			return
		}
		delete(h.aslSupervisorReplyCutoffs, ref)
	}

	if projection.UnansweredQuestions == 0 {
		delete(h.aslSupervisorProjections, ref)
		h.clearASLSupervisorQuestionLocked(
			target.windowID,
			target.candidate.RunID,
			projection.ProjectionSeq,
			now,
			"fresh supervisor projection reports no unanswered questions",
		)
		h.stateMu.Unlock()
		return
	}

	h.aslSupervisorProjections[ref] = projection
	notification := h.applyASLSupervisorQuestionLocked(now, target, projection)
	h.stateMu.Unlock()
	h.dispatchASLSupervisorNotification(notification)
}

// handleASLSupervisorMailboxReply consumes ASL's authoritative, text-free
// fast-clear marker. It only retracts the exact single-question projection the
// producer answered; runner lifecycle facts remain untouched.
func (h *Handler) handleASLSupervisorMailboxReply(
	payload hookevent.HookPayload,
	now time.Time,
) {
	runID := strings.TrimSpace(payload.EffectiveRunID())
	questionID := strings.TrimSpace(payload.QuestionID)
	if strings.TrimSpace(payload.Source) != "agent_session_loop" ||
		strings.TrimSpace(payload.Reason) != aslMailboxReplyReason ||
		runID == "" ||
		!validASLMailboxQuestionID(questionID) {
		return
	}
	replySeq, _, err := parseSourceSeqAt(payload)
	if err != nil || replySeq == 0 {
		return
	}
	projectionRaw := strings.TrimSpace(
		payload.EffectiveMailboxProjectionSequence(),
	)
	projectionSeq, err := strconv.ParseUint(projectionRaw, 10, 64)
	if err != nil || projectionSeq == 0 || replySeq <= projectionSeq {
		return
	}
	if strings.TrimSpace(payload.EffectiveSourceEventID()) !=
		fmt.Sprintf("%s:mailbox-reply:%d", runID, replySeq) {
		return
	}
	canonicalStatePath, err := agentloop.CanonicalizePath(payload.StatePath)
	if err != nil || filepath.Base(canonicalStatePath) != "state.json" {
		return
	}
	stateDir := filepath.Dir(canonicalStatePath)

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for ref, target := range h.aslSupervisorTargets {
		if ref.key != runID ||
			target.candidate.RunID != runID ||
			target.candidate.StateDir != stateDir {
			continue
		}
		if replySeq > h.aslSupervisorReplyCutoffs[ref] {
			h.aslSupervisorReplyCutoffs[ref] = replySeq
		}
		projection, ok := h.aslSupervisorProjections[ref]
		if !ok ||
			projection.ProjectionSeq != projectionSeq ||
			projection.UnansweredQuestions != 1 ||
			projection.LatestQuestionID != questionID {
			continue
		}
		h.clearASLSupervisorQuestionLocked(
			ref.windowID,
			runID,
			replySeq,
			now,
			fmt.Sprintf(
				"matching ASL mailbox reply resolved supervisor question %s",
				questionID,
			),
		)
	}
}

func validASLMailboxQuestionID(raw string) bool {
	compact := strings.ReplaceAll(raw, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

// correlateASLSupervisorQuestion re-evaluates cached projections after a clean
// interactive Stop. It performs no filesystem I/O and is therefore safe on the
// hook path; the periodic refresh handles the inverse ordering where Stop
// arrives before the projection.
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
// when the newest interactive harness fact is a clean Stop no older than the
// pending mailbox question. If a live ASL waiting phase already contributes the
// same visible state, this distinct probe adds no visual change but supplies
// per-question persistence and notification deduplication.
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

	before := st.Reducer.Reduce(now.Unix())
	source := aslSupervisorQuestionSource(target.candidate.RunID)
	decision := st.Reducer.Apply(ReducerEvent{
		Source:         source,
		Action:         "elicitation",
		Seq:            projection.ProjectionSeq,
		At:             now.Unix(),
		OriginWindowID: target.originWindowID,
		RunID:          target.candidate.RunID,
	})
	if decision != DecisionApplied {
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
	reason := fmt.Sprintf(
		"clean supervisor stop correlated with %d unanswered ASL mailbox question(s), latest id %s",
		projection.UnansweredQuestions,
		projection.LatestQuestionID,
	)
	h.appendWatchdogEventLocked(
		target.windowID,
		target.originWindowID,
		now,
		eventASLSupervisorQuestion,
		"elicitation",
		string(source.Kind)+":"+source.Key,
		reason,
	)

	sourceEventID := fmt.Sprintf(
		"asl-supervisor:%s:%s",
		target.candidate.RunID,
		projection.LatestQuestionID,
	)
	if effective.Icon != "question" ||
		!h.shouldNotifySourceEventLocked(st, sourceEventID) {
		return nil
	}
	return &aslSupervisorNotification{
		windowID:     target.windowID,
		sessionTitle: st.LastSessionTitle,
		account:      st.LastAccount,
		harness: firstNonEmpty(
			harnessFromSourceKey(stop.source.Key, stop.sessionID),
			st.LastHarness,
		),
		sessionID:     stop.sessionID,
		runID:         target.candidate.RunID,
		sourceEventID: sourceEventID,
	}
}

func latestInteractiveSupervisorFact(r *Reducer, now int64) (*sourceFact, bool) {
	var current *sourceFact
	for _, fact := range r.facts {
		probeIsSupervisorEvidence := fact.source.Kind == SourceProbe &&
			fact.icon != "" &&
			!strings.HasPrefix(fact.source.Key, aslSupervisorQuestionProbePrefix)
		eligibleSource := fact.source.Kind == SourceHarness ||
			fact.source.Kind == SourceLegacy ||
			probeIsSupervisorEvidence
		if !eligibleSource || fact.pipeMode || !conversationFactLive(fact, now) {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
		}
	}
	return current, current != nil
}

func (h *Handler) dispatchASLSupervisorNotification(notification *aslSupervisorNotification) {
	if notification == nil {
		return
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
	delete(h.aslSupervisorReplyCutoffs, ref)
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
