package daemon

// The Handler half of ASL supervision: polling registered lease records,
// deciding what a fresh projection does to a window, and consuming ASL's
// mailbox handoffs. Parsing those payloads and retaining the per-run
// bookkeeping live with the domain in internal/daemon/asl; every reducer
// mutation below stays here, under stateMu.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

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
	runID = strings.TrimSpace(runID)
	if windowID <= 0 || runID == "" {
		return
	}
	stateDir, ok := asl.LifecycleStateDir(statePath)
	if !ok {
		return
	}
	h.stateMu.Lock()
	h.aslSupervisors.Register(asl.SupervisorTarget{
		WindowID:       windowID,
		OriginWindowID: originWindowID,
		Candidate: agentloop.Candidate{
			RunID:    runID,
			StateDir: stateDir,
		},
	})
	h.stateMu.Unlock()
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
	question, ok := asl.ParseMailboxQuestion(payload)
	if !ok {
		return
	}
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
		target := question.Target(routeTarget.windowID, originWindowID)
		projection := question.Projection(now)

		h.stateMu.Lock()
		ref := asl.LeaseRef{WindowID: target.WindowID, Key: question.RunID}
		if h.aslSupervisors.ClosureBlocks(ref, question.ProjectionSeq) {
			h.stateMu.Unlock()
			continue
		}
		st := h.ensureWindowStateLocked(target.WindowID)
		if st.Reducer == nil {
			st.Reducer = state.NewReducer(h.reducerCfg)
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
