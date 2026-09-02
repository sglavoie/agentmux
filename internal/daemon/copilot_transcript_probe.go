package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

const (
	// copilotTranscriptStopProbeSource keys the SourceProbe fact the Copilot
	// transcript stop settles under. It is deliberately distinct from the Codex
	// (codex_screen) and Claude (claude_screen) probe keys so the three
	// harnesses' idle evidence never collide.
	copilotTranscriptStopProbeSource = "copilot_transcript"

	// copilotTranscriptStopProbeEvent names the synthetic export/event-log row
	// this probe records, distinguishing structured transcript evidence from the
	// Codex/Claude ScreenStopProbe rows for diagnostic provenance.
	copilotTranscriptStopProbeEvent = "TranscriptStopProbe"

	// copilotTurnEndRequiredSamples is the number of consecutive probe
	// observations that must read a terminal assistant.turn_end at the SAME
	// transcript generation before a normal completion settles the stuck ⚡.
	// Installed Copilot CLI 1.0.70 emits many mid-interaction turn_end→turn_start
	// pairs inside one still-running user interaction, so a single momentary
	// terminal read (probed in the sub-second gap before the next turn_start is
	// written) must never settle. Two stable samples one probe interval apart
	// distinguish a genuinely final turn_end (the transcript stops growing) from
	// an intermediate one (a turn_start arrives and advances the generation). A
	// whole-session terminal — a user-initiated abort or session.shutdown — cannot
	// be intermediate and settles immediately (one sample), as before.
	copilotTurnEndRequiredSamples = 2
)

// ProbeCopilotTranscriptStop scans managed Copilot windows for a finished
// interactive turn — a normal completion or a user interruption (abort) — whose
// native agentStop/sessionEnd hook never arrived, leaving a stuck ⚡. It lists
// windows, runs the probe against the snapshot, and renders. Production drives
// the probe from runScreenProbes with the cached snapshot; this wrapper mirrors
// ProbeClaudeScreenStop for callers (and tests) that want the full list+render
// round-trip.
func (h *Handler) ProbeCopilotTranscriptStop(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("copilot transcript probe: list windows failed", "err", err)
		return
	}
	h.probeCopilotTranscriptStopWithSnapshot(now, probes.NewSnapshot(windows, reducerReceipts))
	h.renderSnapshot(windows)
}

// probeCopilotTranscriptStopWithSnapshot runs the Copilot transcript stop probe
// against an already-published Kitty snapshot. Direct sessions retain the
// Copilot title-owner rule; borrowed sessions follow their exact producer route
// to the managed target. Only a target whose reduced icon is a stuck ⚡ or this
// probe's recoverable synthetic 🛑 is considered. Unlike the Codex/Claude probes
// this reads no screen — the authoritative evidence is the matching producer
// session's transcript.
func (h *Handler) probeCopilotTranscriptStopWithSnapshot(now time.Time, snapshot probes.Snapshot) {
	for _, binding := range h.copilotTranscriptProbeBindings(now, snapshot) {
		_, tab, target, ok := kitty.FindWindowLocationByID(snapshot.Windows(), binding.TargetWindowID)
		if !ok {
			continue
		}
		h.probeCopilotTranscriptWindow(now, binding, palette.ParseTitleParts(tab.Title), target.UserVars)
	}
}

// copilotTranscriptProbeBindings combines live interactive producer routes with
// exact post-settle recovery routes. Recovery is derived from the probe stop's
// session memo and the retired producer fact, so it retains the original source
// pane without making that terminal session eligible for ordinary continuation.
func (h *Handler) copilotTranscriptProbeBindings(now time.Time, snapshot probes.Snapshot) []probes.Binding {
	bindings := h.screenProbeBindings(snapshot, harness.Copilot)
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	var recovery []probes.Binding
	for i := range snapshot.Windows() {
		for j := range snapshot.Windows()[i].Tabs {
			for k := range snapshot.Windows()[i].Tabs[j].Windows {
				target := &snapshot.Windows()[i].Tabs[j].Windows[k]
				if target.UserVars.EffectiveProfile() == "" && target.UserVars.EffectiveHarness() == "" {
					continue
				}
				st, ok := h.windowStateLocked(target.ID)
				if !ok || st == nil || st.Reducer == nil {
					continue
				}
				identity, sourceWindowID, ok := st.Reducer.ProbeStopRecoveryRoute(
					source, harness.Copilot, target.ID, now.Unix(),
				)
				if !ok {
					continue
				}
				binding, found := probes.NewBinding(snapshot, identity, sourceWindowID, target.ID)
				if !found {
					continue
				}
				binding.RecoverySource = source
				binding.RecoveryAt = now.Unix()
				if h.screenProbeBindingCurrentLocked(binding) {
					recovery = append(recovery, binding)
				}
			}
		}
	}

	// One producer/source pair may recover at most one managed target. Duplicate
	// restored facts fail closed rather than fanning one transcript observation
	// across two panes.
	type routeKey struct {
		producer string
		session  string
		source   int
	}
	counts := make(map[routeKey]int, len(recovery))
	for _, binding := range recovery {
		counts[routeKey{producer: binding.Producer.Harness, session: binding.Producer.SessionID, source: binding.SourceWindowID}]++
	}
	for _, binding := range recovery {
		key := routeKey{producer: binding.Producer.Harness, session: binding.Producer.SessionID, source: binding.SourceWindowID}
		if counts[key] == 1 {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// copilotTranscriptStopProbeCanOverwrite reports whether the current visible
// icon is one the transcript probe may act on. Two states qualify:
//
//   - ⚡ (bolt): a stuck active turn the probe may settle to 🛑 once terminal
//     transcript evidence stabilizes.
//   - 🛑 (stop): a candidate for POST-SETTLE RECOVERY. A synthetic stop the
//     probe itself applied can be stranded when Copilot appends an active record
//     after the apply-time freshness check, so the probe must keep looking at
//     stopped tabs to self-heal that race. This admits every stopped tab into
//     probeCopilotTranscriptWindow, which immediately bails (before any
//     transcript I/O) unless the reducer confirms the stop is this probe's own
//     sole visible contribution (ProbeStopRecovery); a native or otherwise
//     non-probe-owned 🛑 is left untouched.
//
// question/permission/error/warning/subagent/compact remain protected states the
// idle path must never downgrade. The reducer's own precedence is the final
// guard (a live ASL lease, subagent, or pipe-mode fact still wins at reduce time
// even for a bolt tab), so this filter is an early-exit, not the safety boundary.
func copilotTranscriptStopProbeCanOverwrite(iconState string) bool {
	return iconState == "bolt" || iconState == "stop"
}

// probeCopilotTranscriptWindow recovers a Copilot tab whose ⚡ is stuck on a
// finished assistant turn that left no native terminal hook. It mirrors
// probeClaudeIdleWindow, but its evidence is the interactive session transcript
// rather than a screen read:
//
//   - the reducer receipt sequence is captured before the Kitty snapshot and
//     revalidated while the current interactive conversation is resolved under
//     stateMu BEFORE the slow off-lock transcript read, so a concurrent prompt
//     (or any reducer event) landing anywhere after snapshot capture discards
//     the stale route/read;
//   - the session ID comes from the binding's exact live Copilot producer fact
//     (or the exact retired fact memoized by this probe's recoverable stop), so a
//     pipe-only child and another harness's conversation can never select the
//     transcript the probe settles;
//   - only a CopilotLifecycleTerminal evidence state drives the synthetic stop.
//     Active, Unknown, a missing/unreadable file, a malformed tail, an unsafe
//     session ID, or a missing profile home all fail closed: the stabilization
//     streak is reset and the stuck bolt is left untouched.
//
// The synthetic stop is applied through the race-safe authoritative-idle path
// under a distinct SourceProbe fact, so ASL leases, subagents, pipe-mode
// children, and higher-precedence attention/error icons all still win at reduce
// time.
//
// A bare assistant.turn_end is NOT trusted on a single read: installed Copilot
// emits many mid-interaction turn_end→turn_start pairs, so a normal completion
// must stabilize across copilotTurnEndRequiredSamples probe observations at the
// SAME transcript generation, and a final apply-time freshness check discards
// the probe if the transcript grew (a turn_start appended) between the terminal
// read and the settle. A user-initiated abort or session.shutdown ends the whole
// session and cannot be intermediate, so it settles immediately (one sample).
//
// A tab already showing 🛑 enters RECOVERY mode instead. The apply-time freshness
// check bounds but cannot make atomic the read-then-settle ordering: Copilot's
// external writer can append an active record after the check and before the
// reducer mutation, stranding this probe's own synthetic stop. So when the
// visible stop is this probe's sole contribution (ProbeStopRecovery), the probe
// re-reads the memoized session's transcript and, if it now classifies Active,
// takes its stop back and reinstates the interactive session — self-healing the
// post-stat append with no external hook or prompt. A native or otherwise
// non-probe-owned 🛑 bails before any transcript I/O.
func (h *Handler) probeCopilotTranscriptWindow(
	now time.Time,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	windowID := binding.TargetWindowID
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource}
	ref := activity.Ref{WindowID: windowID, Key: source.Key}

	// Revalidate the complete producer/source/target binding and resolve which session's transcript
	// backs this tab before the off-lock read. A stop tab is a recovery candidate: the
	// interactive fact was retired by the settle, so its session comes from the
	// probe fact's memo (ProbeStopRecovery), which also confirms the stop is this
	// probe's own sole visible contribution — otherwise there is nothing safe to
	// recover and we bail before any transcript I/O. A bolt tab uses the live
	// interactive conversation, as before.
	h.stateMu.Lock()
	if !h.screenProbeBindingCurrentLocked(binding) {
		h.stateMu.Unlock()
		slog.Debug("copilot transcript probe: discarded stale snapshot", "source_window_id", binding.SourceWindowID,
			"target_window_id", windowID)
		return
	}
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return
	}
	if !copilotTranscriptStopProbeCanOverwrite(st.Effective.Icon) {
		h.stateMu.Unlock()
		return
	}
	recovering := st.Effective.Icon == "stop"
	var sessionID string
	if recovering {
		sessionID, ok = st.Reducer.ProbeStopRecovery(source, now.Unix())
		if !ok {
			// A native or otherwise non-probe-owned 🛑: not this probe's synthetic
			// stop, so there is nothing to recover.
			h.clearIdleSampleLocked(ref)
			h.stateMu.Unlock()
			return
		}
	} else if st.Reducer.HasLiveProducerRoute(binding.Producer, binding.SourceWindowID, binding.TargetWindowID) {
		sessionID = binding.Producer.SessionID
	}
	sessionTitle := titleParts.Base
	account := userVars.EffectiveProfile()
	muted := titleParts.Muted || userVars.Mute != "" || st.Muted
	if st.LastSessionTitle != "" {
		sessionTitle = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		account = st.LastAccount
	}
	h.stateMu.Unlock()

	if sessionID == "" {
		// No live Copilot interactive conversation owns this tab (e.g. only a
		// pipe-mode child, a stale/acked fact, or a different harness): there is
		// no session transcript to consult.
		h.ClearIdleSample(windowID, source)
		return
	}
	probeTraceID := diagnostics.NewTraceID()
	probeSourceEventID := fmt.Sprintf("copilot-transcript-probe:%d:%d:%d", binding.SourceWindowID, windowID, binding.TargetReceipt)
	probeReq := protocol.Request{
		TraceID: probeTraceID, SourceEventID: probeSourceEventID,
		Harness: harness.Copilot, Account: account, WindowID: windowID,
	}
	h.recordProbeStage(probeTraceID, "", probeReq, windowID, "attempted", "pending", "transcript_read_started", map[string]string{
		"source_window_id": fmt.Sprint(binding.SourceWindowID),
		"session_id":       sessionID,
	})

	path, err := transcript.ResolveCopilotTranscript(sessionID, h.copilotTranscriptHomes, h.copilotDefaultHome)
	if err != nil {
		// An unsafe session ID or no configured Copilot home to resolve against:
		// no observation.
		slog.Debug("copilot transcript probe: resolve failed", "window_id", windowID, "err", err)
		h.recordProbeStage(probeTraceID, "", probeReq, windowID, "completed", "failed", "transcript_resolve_failed", nil)
		h.ClearIdleSample(windowID, source)
		return
	}

	evidence, err := h.copilotLifecycleReader(path)
	if err != nil {
		// Missing/unreadable file or a malformed tail: fail closed, never settle.
		slog.Debug("copilot transcript probe: read failed", "window_id", windowID, "path", path, "err", err)
		h.recordProbeStage(probeTraceID, "", probeReq, windowID, "completed", "failed", "transcript_read_failed", nil)
		h.ClearIdleSample(windowID, source)
		return
	}

	if recovering {
		// Recovery: only fresh Active evidence takes the stranded stop back. A
		// still-terminal read (the common case: the stop was real) or any
		// untrustworthy evidence leaves 🛑 in place.
		h.recoverCopilotTranscriptStop(now, binding, source, evidence.State)
		h.recordProbeStage(probeTraceID, "", probeReq, windowID, "completed", "success", "recovery_evaluated", map[string]string{
			"lifecycle_state": fmt.Sprint(evidence.State),
		})
		return
	}

	if evidence.State != transcript.CopilotLifecycleTerminal {
		// Active or Unknown main-turn evidence: still working, or no state-bearing
		// event. Reset the stabilization streak so a later transient frame cannot
		// settle live work.
		h.ClearIdleSample(windowID, source)
		h.recordProbeStage(probeTraceID, "", probeReq, windowID, "completed", "success", "non_terminal", map[string]string{
			"lifecycle_state": fmt.Sprint(evidence.State),
		})
		return
	}
	// A normal completion (assistant.turn_end) is the intermediate-prone signal:
	// require it to hold steady across two probe observations at the same
	// transcript generation, and re-verify at apply time that no record was
	// appended since the read. Whole-session terminals (abort, session.shutdown)
	// settle immediately with no generation or freshness gate.
	requiredSamples := 1
	generation := int64(0)
	var confirmStable func() bool
	var confirmNotify func() bool
	if evidence.EventType == "assistant.turn_end" {
		requiredSamples = copilotTurnEndRequiredSamples
		generation = evidence.Size
		capturedSize := evidence.Size
		confirmStable = func() bool {
			info, statErr := os.Stat(path)
			if statErr != nil {
				// Cannot confirm the transcript is unchanged: fail closed.
				return false
			}
			// A grown transcript means a record (e.g. a new assistant.turn_start)
			// was appended after the terminal read: this turn_end was intermediate.
			return info.Size() <= capturedSize
		}
		// Before dispatching the (unrevocable) stop notification, re-read the
		// transcript. If a record landed after the ConfirmStable check but before
		// the settle, this synthetic stop now sits over an active session that the
		// recovery path will restore to ⚡ next cycle — so suppress the false stop
		// notification. Suppress only on a CONFIRMED active read, mirroring
		// recoverCopilotTranscriptStop: an unreadable/terminal re-read leaves the
		// stop (and its notification) intact, exactly as recovery leaves 🛑 in place.
		confirmNotify = func() bool {
			ev, readErr := h.copilotLifecycleReader(path)
			if readErr != nil {
				return true
			}
			return ev.State != transcript.CopilotLifecycleActive
		}
	}

	applied := h.applyAuthoritativeIdle(authoritativeIdle{
		WindowID:        windowID,
		Source:          source,
		Harness:         harness.Copilot,
		CapturedSeq:     binding.TargetReceipt,
		ProbeBinding:    binding,
		At:              now.Unix(),
		RequiredSamples: requiredSamples,
		Generation:      generation,
		ConfirmStable:   confirmStable,
		ConfirmNotify:   confirmNotify,
		SessionTitle:    sessionTitle,
		Account:         account,
		PipeMode:        false,
		Muted:           muted,
		SessionID:       sessionID,
		SourceEventID:   probeSourceEventID,
		TraceID:         probeTraceID,
		DiagnosticEvent: copilotTranscriptStopProbeEvent,
	})
	outcome, reason := "pending", "stabilizing_or_stale"
	if applied {
		outcome, reason = "success", "terminal_applied"
	}
	h.recordProbeStage(probeTraceID, "", probeReq, windowID, "completed", outcome, reason, map[string]string{
		"event_type": fmt.Sprint(evidence.EventType),
	})
}

// recoverCopilotTranscriptStop self-heals a synthetic 🛑 that a post-settle
// transcript append stranded. It is the race-safe counterpart to
// applyAuthoritativeIdle: the freshness classification (the transcript now reads
// Active) and the reducer mutation are made atomic under stateMu, closing the
// post-stat ordering the apply-time ConfirmStable check cannot. Only fresh
// Active evidence recovers; the captured receipt sequence must still be current
// (no reducer event since capture), and the probe's stop must still be its own
// sole visible contribution (re-checked under the lock), or the recovery is
// discarded and the stop left intact.
func (h *Handler) recoverCopilotTranscriptStop(
	now time.Time,
	binding probes.Binding,
	source protocol.SourceID,
	state transcript.CopilotLifecycleState,
) {
	if state != transcript.CopilotLifecycleActive {
		// Still terminal (the stop was genuinely final) or no trustworthy
		// evidence: leave 🛑 in place.
		return
	}

	windowID := binding.TargetWindowID
	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		h.stateMu.Unlock()
		return
	}
	// A reducer event landed during the read (native hook, a fresh prompt): it
	// already owns convergence and may have retired the probe stop, so discard.
	if !h.screenProbeBindingCurrentLocked(binding) {
		h.stateMu.Unlock()
		return
	}
	// Re-confirm under the lock that the probe stop is still the sole visible
	// contribution and recover the memoized session — this is the atomic
	// freshness-plus-mutation the settle path's ConfirmStable could not be.
	sessionID, stillRecovering := st.Reducer.ProbeStopRecovery(source, now.Unix())
	if !stillRecovering || sessionID != binding.Producer.SessionID {
		h.stateMu.Unlock()
		return
	}
	before := st.Effective
	at := now.Unix()
	st.ShadowSeq++
	if !st.Reducer.RecoverInteractiveFromProbeStop(source, sessionID, at) {
		// Nothing to reinstate (the retired interactive fact is gone): leave the
		// stop untouched rather than strand the window on an empty state.
		h.stateMu.Unlock()
		return
	}
	st.Reducer.Compact(at)
	effective := st.Reducer.Reduce(at)
	h.publishEffectiveLocked(windowID, st, effective)

	// Record the ACTUAL reduced result under the transcript-probe source for
	// provenance. Recovery never transitions TO stop, so no notification fires.
	resultAction := effective.Icon
	if resultAction == "" {
		resultAction = "idle"
	}
	h.appendWatchdogEventLocked(windowID, binding.SourceWindowID, time.Unix(at, 0), copilotTranscriptStopProbeEvent, resultAction,
		string(source.Kind)+":"+source.Key,
		fmt.Sprintf("copilot transcript recovery superseded synthetic stop %q→%q after a post-stat active append",
			before.Icon, effective.Icon))
	h.syncRendererMetadataLocked(windowID)

	if effective != before {
		h.markWindowDirtyLocked(windowID)
	} else {
		h.markWindowActivityDirtyLocked(windowID)
	}
	h.stateMu.Unlock()
}
