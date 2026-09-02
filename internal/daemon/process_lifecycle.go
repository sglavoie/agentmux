package daemon

// Process lifecycle reconciliation is an independent fail-safe for harnesses
// that exit without emitting Stop or SessionEnd. Candidate capture and commit
// happen under stateMu, but the process-table read is always off-lock. A window
// receipt plus exact fact fingerprints form the optimistic concurrency token:
// any newer hook, watchdog expiry, reset transition, restore merge, or probe
// mutation invalidates the whole observation before it can touch state.

import (
	"context"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

const (
	harnessProcessLifecycleInterval         = time.Second
	processInspectionFailureJournalInterval = time.Minute
	processLifecycleJournalStage            = "daemon.process_lifecycle"
	processTerminalHookEvidenceAbsent       = "absent"
	processTerminalHookEvidenceObserved     = "observed"
	// Retain the package-local name used by existing focused tests while the
	// exported constant is the public schema contract.
	eventHarnessProcessExited = protocol.EventHarnessProcessExited
)

type processLifecycleFactReceipt struct {
	source         protocol.SourceID
	seq            uint64
	causalOrder    uint64
	pipeMode       bool
	originWindowID int
	sessionID      string
	terminated     bool
}

type processLifecycleCandidate struct {
	windowID int
	receipt  uint64
	identity protocol.HarnessProcessIdentity
	pipeMode bool
	facts    []processLifecycleFactReceipt
}

type pendingLifecycleNotificationDisposition struct {
	disposition notification.NotificationDisposition
	cancelled   bool
}

type processLifecycleDiagnostic struct {
	windowID             int
	identity             protocol.HarnessProcessIdentity
	observation          probes.HarnessProcessInspection
	mode                 string
	terminalHookEvidence string
	result               state.HarnessProcessExitResult
	sources              []protocol.ProcessLifecycleSourceEvidence
	renderGeneration     uint64
	sessionID            string
}

// SetHarnessProcessInspector replaces the production PID/lstart inspector.
// It is safe to call while runHarnessProcessLifecycle is active. A nil
// inspector disables lifecycle polling while retaining watchdog fallback
// behavior.
func (h *Handler) SetHarnessProcessInspector(inspector probes.HarnessProcessInspector) {
	h.processInspectorMu.Lock()
	defer h.processInspectorMu.Unlock()
	h.processInspector = inspector
}

func (h *Handler) harnessProcessInspector() probes.HarnessProcessInspector {
	h.processInspectorMu.RLock()
	defer h.processInspectorMu.RUnlock()
	return h.processInspector
}

func (h *Handler) runHarnessProcessLifecycle(ctx context.Context) {
	h.runHarnessProcessLifecycleWithInterval(ctx, harnessProcessLifecycleInterval)
}

func (h *Handler) runHarnessProcessLifecycleWithInterval(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = harnessProcessLifecycleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.processLifecycleTick(ctx, now)
		}
	}
}

// processLifecycleTick performs one capture/inspect/revalidate pass. It
// returns the number of grouped settlements committed, primarily as a focused
// test seam. Missing and replaced-instance observations are both confirmed
// exits for the old fingerprint; alive and unknown observations are no-ops.
func (h *Handler) processLifecycleTick(ctx context.Context, observedAt time.Time) int {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	inspector := h.harnessProcessInspector()
	if inspector == nil {
		return 0
	}
	candidates := h.collectProcessLifecycleCandidates()
	if len(candidates) == 0 {
		return 0
	}

	identities := deduplicatedLifecycleIdentities(candidates)
	inspections := inspector.InspectHarnessProcesses(ctx, identities)
	if ctx.Err() != nil {
		return 0
	}
	h.recordProcessInspectionFailures(observedAt, identities, inspections)

	committed := 0
	pendingDispositions := make([]pendingLifecycleNotificationDisposition, 0)
	diagnosticsToRecord := make([]processLifecycleDiagnostic, 0)
	bypassedHolds := make([]pendingStopHold, 0)
	// A verified process exit is genuinely terminal, so it settles the window
	// directly and never enters the confirmed-idle gate. Taking the commit mutex
	// before stateMu — the order applyCodexClearObservation established — is what
	// makes that bypass complete: a hold callback that already passed its claim is
	// parked here until this pass has withdrawn the window's facts, and then finds
	// its entry gone instead of re-applying a stop over the settled state.
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	for _, candidate := range candidates {
		status := inspections[candidate.identity]
		if status != probes.HarnessProcessMissing && status != probes.HarnessProcessReplacedInstance {
			continue
		}
		st, ok := h.windowStateLocked(candidate.windowID)
		if !ok || st.Reducer == nil || st.ShadowSeq != candidate.receipt {
			continue
		}
		facts := st.Reducer.Facts()
		if !processLifecycleCandidateCurrent(candidate, st.PendingReset, facts) {
			continue
		}

		before := st.Effective
		result := st.Reducer.SettleHarnessProcessExit(candidate.identity, observedAt.Unix(), state.HarnessProcessExitOptions{
			PipeMode:  candidate.pipeMode,
			ClearOnly: st.PendingReset != state.ResetFlowNone,
		})
		if !result.Changed {
			continue
		}
		// Only a settlement of this exact producer retires the hold: a pass that
		// changed nothing has not replaced the withheld stop with anything, and a
		// pass that settled a different process in the same window has not either.
		bypassedTerminalHold := false
		if dropped, held := h.cancelStopHoldForProcessExitLocked(candidate.windowID, candidate.identity, facts); held {
			bypassedHolds = append(bypassedHolds, dropped)
			bypassedTerminalHold = dropped.class == stopHoldClassTerminal
		}
		st.ShadowSeq++
		st.Reducer.Compact(observedAt.Unix())
		after := st.Reducer.Reduce(observedAt.Unix())
		h.publishEffectiveLocked(candidate.windowID, st, after)
		h.syncLegacyLifecycleWinnerLocked(candidate.windowID, st, after, observedAt)
		h.clearIdleSamplesForWindowLocked(candidate.windowID)

		if h.notifier != nil {
			disposition, cancelled := h.notifier.CancelPendingDisposition(candidate.windowID)
			pendingDispositions = append(pendingDispositions, pendingLifecycleNotificationDisposition{
				disposition: disposition,
				cancelled:   cancelled,
			})
		}

		action := processLifecycleActionForIcon(after.Icon)
		if action == "" {
			action = "idle"
		}
		visibleChanged := after.Icon != before.Icon || after.Looping != before.Looping
		renderGeneration := st.Seq + 1
		sources, primary, sessionID := processLifecycleSources(candidate, result, facts)
		terminalEvidence := processTerminalHookEvidenceAbsent
		if !result.SynthesizedStop {
			for _, receipt := range candidate.facts {
				if receipt.terminated {
					terminalEvidence = processTerminalHookEvidenceObserved
					break
				}
			}
		}
		if bypassedTerminalHold {
			// The confirmed-idle gate was withholding a native terminal hook that
			// genuinely arrived, so no fact carries it. The exit still synthesizes
			// the reducer stop, but calling the hook absent would contradict this
			// window's own held and bypassed rows.
			terminalEvidence = processTerminalHookEvidenceObserved
		}
		h.appendProcessLifecycleEventLocked(
			candidate.windowID, observedAt, action, status, candidate.identity,
			candidate.pipeMode, terminalEvidence, result, sources, primary, renderGeneration,
		)
		if visibleChanged {
			h.markWindowDirtyLocked(candidate.windowID)
		} else {
			h.markWindowActivityDirtyLocked(candidate.windowID)
		}
		h.syncRendererMetadataLocked(candidate.windowID)
		diagnosticsToRecord = append(diagnosticsToRecord, processLifecycleDiagnostic{
			windowID: candidate.windowID, identity: candidate.identity, observation: status,
			mode: processLifecycleMode(candidate.pipeMode), terminalHookEvidence: terminalEvidence,
			result: result, sources: sources, renderGeneration: renderGeneration, sessionID: sessionID,
		})
		committed++
	}
	h.stateMu.Unlock()
	h.resetStopCommitMu.Unlock()

	// Observer callbacks may perform bounded diagnostic work and can re-enter
	// handler state. Emit cancellation dispositions only after stateMu is free.
	for _, pending := range pendingDispositions {
		if pending.cancelled {
			h.notifier.EmitDisposition(pending.disposition)
		}
	}
	for _, dropped := range bypassedHolds {
		h.recordBypassedStopHold(dropped)
	}
	for _, record := range diagnosticsToRecord {
		h.recordProcessLifecycleSettlement(observedAt, record)
	}
	return committed
}
