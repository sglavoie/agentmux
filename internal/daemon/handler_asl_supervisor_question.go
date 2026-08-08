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
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	aslSupervisorQuestionProbePrefix  = "asl_supervisor:"
	eventASLSupervisorQuestion        = "ASLSupervisorQuestion"
	agentSessionLoopMailboxQuestion   = "AgentSessionLoopMailboxQuestion"
	agentSessionLoopMailboxReply      = "AgentSessionLoopMailboxReply"
	agentSessionLoopMailboxResolution = "AgentSessionLoopMailboxResolution"
	aslMailboxQuestionReason          = "mailbox_question_surfaced"
	aslMailboxReplyReason             = "mailbox_reply_appended"
	aslMailboxResolutionReason        = "mailbox_resolution_appended"
)

// aslSupervisorTarget is one live ASL run whose adjacent fixed-size supervisor
// lease can be refreshed without rescanning the complete ASL state root.
type aslSupervisorTarget struct {
	windowID       int
	originWindowID int
	candidate      agentloop.Candidate
}

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
	if cutoff, ok := h.aslSupervisorClosureCutoffs[ref]; ok {
		if projection.ProjectionSeq <= cutoff {
			h.stateMu.Unlock()
			return
		}
		delete(h.aslSupervisorClosureCutoffs, ref)
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

	previous := h.aslSupervisorProjections[ref]
	if st, ok := h.windowStateLocked(target.windowID); ok && st.Reducer != nil {
		fact := st.Reducer.facts[aslSupervisorQuestionSource(target.candidate.RunID)]
		if fact != nil && fact.acked &&
			previous.LatestQuestionID == projection.LatestQuestionID {
			// F8 dismisses the current open set. Lease heartbeats and unrelated
			// mailbox records may advance ProjectionSeq, but only a different latest
			// question identity is allowed to re-arm the attention probe.
			h.aslSupervisorProjections[ref] = projection
			h.stateMu.Unlock()
			return
		}
	}
	h.aslSupervisorProjections[ref] = projection
	notification := h.applyASLSupervisorQuestionLocked(now, target, projection)
	h.stateMu.Unlock()
	h.dispatchASLSupervisorNotification(notification)
}

// handleASLSupervisorMailboxQuestion consumes ASL's body-free handoff as soon
// as outbox wait surfaces an actionable open set. Unlike the lease compatibility
// path, a direct handoff does not wait for a correlated harness Stop.
func (h *Handler) handleASLSupervisorMailboxQuestion(
	payload hookevent.HookPayload,
	baseReq Request,
	windows []kitty.OSWindow,
	now time.Time,
) {
	runID := strings.TrimSpace(payload.EffectiveRunID())
	questionID := strings.TrimSpace(payload.QuestionID)
	if strings.TrimSpace(payload.Source) != "agent_session_loop" ||
		strings.TrimSpace(payload.Reason) != aslMailboxQuestionReason ||
		runID == "" ||
		!validASLMailboxQuestionID(questionID) ||
		payload.UnansweredQuestions <= 0 ||
		strings.TrimSpace(payload.DeliveryState) != "open" ||
		strings.TrimSpace(payload.QuestionPreview) != "" {
		return
	}
	questionSeq, _, err := parseSourceSeqAt(payload)
	if err != nil || questionSeq == 0 {
		return
	}
	projectionSeq, err := strconv.ParseUint(
		strings.TrimSpace(payload.EffectiveMailboxProjectionSequence()),
		10,
		64,
	)
	if err != nil || projectionSeq < questionSeq {
		return
	}
	if strings.TrimSpace(payload.EffectiveSourceEventID()) !=
		fmt.Sprintf("%s:mailbox-question:%d", runID, questionSeq) {
		return
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.QuestionCreatedAt))
	if err != nil {
		return
	}
	canonicalStatePath, err := agentloop.CanonicalizePath(payload.StatePath)
	if err != nil || filepath.Base(canonicalStatePath) != "state.json" {
		return
	}
	stateDir := filepath.Dir(canonicalStatePath)
	targets, _ := loopLifecycleTargets(windows, payload)
	for _, routeTarget := range targets {
		parts := palette.ParseTitleParts(routeTarget.tab.Title)
		profile := ""
		harnessName := ""
		muted := parts.Muted
		if routeTarget.win != nil {
			profile = routeTarget.win.UserVars.EffectiveProfile()
			harnessName = routeTarget.win.UserVars.EffectiveHarness()
			muted = muted || routeTarget.win.UserVars.Mute != ""
		}
		h.setMutedFromSnapshot(routeTarget.windowID, muted)
		originWindowID := baseReq.WindowID
		if payloadWindowID := payload.EffectiveKittyWindowID(); payloadWindowID > 0 {
			originWindowID = payloadWindowID
		}
		target := aslSupervisorTarget{
			windowID:       routeTarget.windowID,
			originWindowID: originWindowID,
			candidate: agentloop.Candidate{
				RunID:    runID,
				StateDir: stateDir,
			},
		}
		projection := agentloop.SupervisorQuestionProjection{
			RunID:                   runID,
			ProjectionSeq:           projectionSeq,
			UnansweredQuestions:     payload.UnansweredQuestions,
			LatestQuestionID:        questionID,
			LatestQuestionCreatedAt: createdAt,
			UpdatedAt:               now,
		}

		h.stateMu.Lock()
		ref := aslLeaseRef{windowID: target.windowID, key: runID}
		if cutoff, ok := h.aslSupervisorClosureCutoffs[ref]; ok {
			if projectionSeq <= cutoff {
				h.stateMu.Unlock()
				continue
			}
			delete(h.aslSupervisorClosureCutoffs, ref)
		}
		st := h.ensureWindowStateLocked(target.windowID)
		if st.Reducer == nil {
			st.Reducer = NewReducer(h.reducerCfg)
		}
		if st.LastSessionTitle == "" {
			st.LastSessionTitle = parts.Base
		}
		if st.LastAccount == "" {
			st.LastAccount = profile
		}
		if st.LastHarness == "" {
			st.LastHarness = firstNonEmpty(harnessName, baseReq.Harness, payload.Harness)
		}
		h.registerASLSupervisorTargetLocked(target)
		previous := h.aslSupervisorProjections[ref]
		if fact := st.Reducer.facts[aslSupervisorQuestionSource(runID)]; fact != nil && fact.acked &&
			previous.LatestQuestionID == projection.LatestQuestionID {
			h.aslSupervisorProjections[ref] = projection
			h.stateMu.Unlock()
			continue
		}
		h.aslSupervisorProjections[ref] = projection
		notification := h.applyASLSupervisorQuestionEvidenceLocked(
			now,
			target,
			projection,
			strings.TrimSpace(payload.EffectiveSourceEventID()),
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
	runID := strings.TrimSpace(payload.EffectiveRunID())
	questionID := strings.TrimSpace(payload.QuestionID)
	reason := aslMailboxReplyReason
	eventLabel := "mailbox-reply"
	if event == agentSessionLoopMailboxResolution {
		reason = aslMailboxResolutionReason
		eventLabel = "mailbox-resolution"
		action := strings.TrimSpace(payload.ResolutionAction)
		if action != "continue" && action != "pause" {
			return
		}
	} else if event != agentSessionLoopMailboxReply {
		return
	}
	if strings.TrimSpace(payload.Source) != "agent_session_loop" ||
		strings.TrimSpace(payload.Reason) != reason ||
		runID == "" ||
		!validASLMailboxQuestionID(questionID) {
		return
	}
	closureSeq, _, err := parseSourceSeqAt(payload)
	if err != nil || closureSeq == 0 {
		return
	}
	projectionRaw := strings.TrimSpace(
		payload.EffectiveMailboxProjectionSequence(),
	)
	projectionSeq, err := strconv.ParseUint(projectionRaw, 10, 64)
	if err != nil || projectionSeq == 0 || projectionSeq > closureSeq {
		return
	}
	if strings.TrimSpace(payload.EffectiveSourceEventID()) !=
		fmt.Sprintf("%s:%s:%d", runID, eventLabel, closureSeq) {
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
		if closureSeq > h.aslSupervisorClosureCutoffs[ref] {
			h.aslSupervisorClosureCutoffs[ref] = closureSeq
		}
		projection, ok := h.aslSupervisorProjections[ref]
		if !ok || projection.ProjectionSeq > closureSeq {
			continue
		}
		h.clearASLSupervisorQuestionLocked(
			ref.windowID,
			runID,
			closureSeq,
			now,
			fmt.Sprintf(
				"ASL %s closed the surfaced mailbox question set at question %s",
				eventLabel,
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
		identifiedHarness := fact.source.Kind == SourceHarness &&
			fact.sessionID != "" &&
			harnessFromSourceKey(fact.source.Key, fact.sessionID) != ""
		authoritativeStopProbe := fact.source.Kind == SourceProbe &&
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
