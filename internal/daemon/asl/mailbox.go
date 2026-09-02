package asl

// Supervisor mailbox handoffs.
//
// ASL emits two body-free markers about the mailbox a supervised run is
// blocked on: a question marker when outbox wait surfaces an actionable open
// set, and a reply/resolution marker once its own post-append snapshot proves
// nothing remains open. Both are authoritative, so both are validated hard
// before anything acts on them — a payload that cannot corroborate its own
// identity, sequence, and plan is not evidence, and is silently ignored.
//
// Deliberately absent from the parsed result: question prose. The markers are
// text-free by contract, and a payload carrying a preview is refused rather
// than trimmed, so a body can never travel this path.

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// The reason a mailbox marker must declare for its event. These are wire
// values: ASL writes them and this package refuses anything else, so the pair
// (event, reason) is what identifies a marker rather than the event alone.
const (
	MailboxQuestionReason   = "mailbox_question_surfaced"
	MailboxReplyReason      = "mailbox_reply_appended"
	MailboxResolutionReason = "mailbox_resolution_appended"
)

// MailboxQuestion is one authenticated open-question handoff.
type MailboxQuestion struct {
	RunID string
	// StateDir is the plan directory derived from the payload's own state
	// path, which is what binds the handoff to a poll target.
	StateDir            string
	QuestionID          string
	QuestionCreatedAt   time.Time
	UnansweredQuestions int
	ProjectionSeq       uint64
	SourceEventID       string
}

// Projection renders the handoff as the supervisor projection it stands in for,
// so an immediate handoff and a polled lease record are indistinguishable
// downstream. now stamps the observation.
func (q MailboxQuestion) Projection(now time.Time) agentloop.SupervisorQuestionProjection {
	return agentloop.SupervisorQuestionProjection{
		RunID:                   q.RunID,
		ProjectionSeq:           q.ProjectionSeq,
		UnansweredQuestions:     q.UnansweredQuestions,
		LatestQuestionID:        q.QuestionID,
		LatestQuestionCreatedAt: q.QuestionCreatedAt,
		UpdatedAt:               now,
	}
}

// Target renders the handoff as a poll target on one managed window, so a run
// first seen through a handoff is polled afterwards exactly like a discovered
// one.
func (q MailboxQuestion) Target(windowID, originWindowID int) SupervisorTarget {
	return SupervisorTarget{
		WindowID:       windowID,
		OriginWindowID: originWindowID,
		Candidate: agentloop.Candidate{
			RunID:    q.RunID,
			StateDir: q.StateDir,
		},
	}
}

// ParseMailboxQuestion validates ASL's body-free open-question handoff. Unlike
// the lease compatibility path, a direct handoff does not wait for a correlated
// harness Stop, so every field it relies on is checked here.
func ParseMailboxQuestion(payload hookevent.HookPayload) (MailboxQuestion, bool) {
	runID := strings.TrimSpace(payload.EffectiveRunID())
	questionID := strings.TrimSpace(payload.QuestionID)
	if strings.TrimSpace(payload.Source) != events.ASLHookSource ||
		strings.TrimSpace(payload.Reason) != MailboxQuestionReason ||
		runID == "" ||
		!validMailboxQuestionID(questionID) ||
		payload.UnansweredQuestions <= 0 ||
		strings.TrimSpace(payload.DeliveryState) != "open" ||
		strings.TrimSpace(payload.QuestionPreview) != "" {
		return MailboxQuestion{}, false
	}
	questionSeq, _, err := events.ParseSourceSeqAt(payload)
	if err != nil || questionSeq == 0 {
		return MailboxQuestion{}, false
	}
	projectionSeq, err := strconv.ParseUint(
		strings.TrimSpace(payload.EffectiveMailboxProjectionSequence()),
		10,
		64,
	)
	if err != nil || projectionSeq < questionSeq {
		return MailboxQuestion{}, false
	}
	sourceEventID := strings.TrimSpace(payload.EffectiveSourceEventID())
	if sourceEventID != fmt.Sprintf("%s:mailbox-question:%d", runID, questionSeq) {
		return MailboxQuestion{}, false
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.QuestionCreatedAt))
	if err != nil {
		return MailboxQuestion{}, false
	}
	stateDir, ok := stateDirOf(payload.StatePath)
	if !ok {
		return MailboxQuestion{}, false
	}
	return MailboxQuestion{
		RunID:               runID,
		StateDir:            stateDir,
		QuestionID:          questionID,
		QuestionCreatedAt:   createdAt,
		UnansweredQuestions: payload.UnansweredQuestions,
		ProjectionSeq:       projectionSeq,
		SourceEventID:       sourceEventID,
	}, true
}

// MailboxClosure is one authenticated reply or resolution marker.
type MailboxClosure struct {
	RunID      string
	StateDir   string
	QuestionID string
	// ClosureSeq is the sequence at and below which this run's projections are
	// already known to be closed.
	ClosureSeq uint64
	// Label names the marker in operator-facing reasons: mailbox-reply or
	// mailbox-resolution.
	Label string
}

// ParseMailboxClosure validates ASL's authoritative, text-free reply/resolution
// fast-clear marker. The producer emits it only after its post-append snapshot
// proves the run has no remaining open questions, so its own sequence and plan
// identity are what authorize it to retire an open set.
func ParseMailboxClosure(event string, payload hookevent.HookPayload) (MailboxClosure, bool) {
	reason := MailboxReplyReason
	label := "mailbox-reply"
	switch event {
	case events.AgentSessionLoopMailboxResolution:
		reason = MailboxResolutionReason
		label = "mailbox-resolution"
		action := strings.TrimSpace(payload.ResolutionAction)
		if action != "continue" && action != "pause" {
			return MailboxClosure{}, false
		}
	case events.AgentSessionLoopMailboxReply:
	default:
		return MailboxClosure{}, false
	}

	runID := strings.TrimSpace(payload.EffectiveRunID())
	questionID := strings.TrimSpace(payload.QuestionID)
	if strings.TrimSpace(payload.Source) != events.ASLHookSource ||
		strings.TrimSpace(payload.Reason) != reason ||
		runID == "" ||
		!validMailboxQuestionID(questionID) {
		return MailboxClosure{}, false
	}
	closureSeq, _, err := events.ParseSourceSeqAt(payload)
	if err != nil || closureSeq == 0 {
		return MailboxClosure{}, false
	}
	projectionSeq, err := strconv.ParseUint(
		strings.TrimSpace(payload.EffectiveMailboxProjectionSequence()),
		10,
		64,
	)
	if err != nil || projectionSeq == 0 || projectionSeq > closureSeq {
		return MailboxClosure{}, false
	}
	if strings.TrimSpace(payload.EffectiveSourceEventID()) !=
		fmt.Sprintf("%s:%s:%d", runID, label, closureSeq) {
		return MailboxClosure{}, false
	}
	stateDir, ok := stateDirOf(payload.StatePath)
	if !ok {
		return MailboxClosure{}, false
	}
	return MailboxClosure{
		RunID:      runID,
		StateDir:   stateDir,
		QuestionID: questionID,
		ClosureSeq: closureSeq,
		Label:      label,
	}, true
}

// stateDirOf canonicalizes a declared state path and returns the plan directory
// holding it. A path that does not resolve, or that does not end in the state
// file itself, identifies no plan and is refused.
func stateDirOf(statePath string) (string, bool) {
	canonical, err := agentloop.CanonicalizePath(statePath)
	if err != nil || filepath.Base(canonical) != "state.json" {
		return "", false
	}
	return filepath.Dir(canonical), true
}

// validMailboxQuestionID accepts only a UUID-shaped identifier. ASL mints them,
// and anything else is a payload describing a question this daemon cannot
// correlate.
func validMailboxQuestionID(raw string) bool {
	compact := strings.ReplaceAll(raw, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

// LifecycleStateDir canonicalizes the state path carried by an authenticated
// ASL lifecycle hook, so a run can register its poll target from a live event
// instead of waiting for the next reconciliation sweep. Reconciliation remains
// the fallback when an older producer omits the path or it cannot be
// canonicalized.
func LifecycleStateDir(statePath string) (string, bool) {
	statePath = strings.TrimSpace(statePath)
	if statePath == "" {
		return "", false
	}
	return stateDirOf(statePath)
}
