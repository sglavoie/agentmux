package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"time"

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
	h.probeCopilotTranscriptStopWithSnapshot(now, screenProbeSnapshot{windows: windows, reducerReceipts: reducerReceipts})
	h.renderSnapshot(windows)
}

// ProbeCopilotTranscriptStopWithSnapshot runs the Copilot transcript stop probe
// against an already-published Kitty snapshot. Only the Copilot tab-title owner
// is scanned (never an unmanaged foreground sibling), and only a tab whose
// current icon is a stuck ⚡: any other visible state is either already terminal
// or a higher-precedence icon (🤖 subagent, 🔁 ASL, key/question/warning/error)
// the idle path must never downgrade. Unlike the Codex/Claude probes this reads
// no screen — the authoritative evidence is the session transcript's latest
// state-bearing event.
func (h *Handler) ProbeCopilotTranscriptStopWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeCopilotTranscriptStopWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeCopilotTranscriptStopWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			if !copilotTranscriptStopProbeCanOverwrite(parts.State) {
				continue
			}
			win, ok := copilotTranscriptProbeTarget(tab)
			if !ok {
				continue
			}
			h.probeCopilotTranscriptWindow(now, win.ID, parts, win.UserVars, snapshot.receipt(win.ID), snapshot.gen)
		}
	}
}

// copilotTranscriptProbeTarget returns the tab's managed title owner when it is a
// Copilot-managed window. Like the Claude probes (and unlike the Codex prompt
// probe) it has no unmanaged-foreground fallback: a tab with no managed title
// owner has no interactive session identity to resolve a transcript against.
func copilotTranscriptProbeTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	owner, ok := kitty.FindTabTitleOwner(tab)
	if !ok {
		return nil, false
	}
	return owner, owner.UserVars.EffectiveHarness() == harness.Copilot
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
//   - the session ID comes from the reducer's CurrentInteractiveConversation,
//     which resolves ONLY a live interactive owner and never falls back to a
//     sole pipe-mode `copilot -p` child, so a background pipe invocation never
//     selects the transcript the probe settles nor overwrites the parent-session
//     memo a masked probe stop needs for post-stat recovery;
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
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	source := SourceID{Kind: SourceProbe, Key: copilotTranscriptStopProbeSource}
	ref := idleSampleRef{windowID: windowID, key: source.Key}

	// Revalidate the snapshot receipt and resolve which session's transcript
	// backs this tab before the off-lock read. A stop tab is a recovery candidate: the
	// interactive fact was retired by the settle, so its session comes from the
	// probe fact's memo (ProbeStopRecovery), which also confirms the stop is this
	// probe's own sole visible contribution — otherwise there is nothing safe to
	// recover and we bail before any transcript I/O. A bolt tab uses the live
	// interactive conversation, as before.
	h.stateMu.Lock()
	if !h.screenProbeEligibilityCurrentLocked(windowID, capturedSeq, capturedSnapshotGen, harness.Copilot, func(icon string) bool {
		return icon == titleParts.State
	}) {
		h.stateMu.Unlock()
		slog.Debug("copilot transcript probe: discarded stale snapshot", "window_id", windowID)
		return
	}
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(ref)
		h.stateMu.Unlock()
		return
	}
	recovering := titleParts.State == "stop"
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
	} else if identity, hasConversation := st.CurrentInteractiveConversation(now.Unix()); hasConversation &&
		identity.Harness == harness.Copilot && identity.SessionID != "" {
		sessionID = identity.SessionID
	}
	h.stateMu.Unlock()

	if sessionID == "" {
		// No live Copilot interactive conversation owns this tab (e.g. only a
		// pipe-mode child, a stale/acked fact, or a different harness): there is
		// no session transcript to consult.
		h.ClearIdleSample(windowID, source)
		return
	}

	path, err := transcript.ResolveCopilotTranscript(sessionID, h.copilotTranscriptHomes, h.copilotDefaultHome)
	if err != nil {
		// An unsafe session ID or no configured Copilot home to resolve against:
		// no observation.
		slog.Debug("copilot transcript probe: resolve failed", "window_id", windowID, "err", err)
		h.ClearIdleSample(windowID, source)
		return
	}

	evidence, err := h.copilotLifecycleReader(path)
	if err != nil {
		// Missing/unreadable file or a malformed tail: fail closed, never settle.
		slog.Debug("copilot transcript probe: read failed", "window_id", windowID, "path", path, "err", err)
		h.ClearIdleSample(windowID, source)
		return
	}

	if recovering {
		// Recovery: only fresh Active evidence takes the stranded stop back. A
		// still-terminal read (the common case: the stop was real) or any
		// untrustworthy evidence leaves 🛑 in place.
		h.recoverCopilotTranscriptStop(now, windowID, source, capturedSeq, capturedSnapshotGen, evidence.State)
		return
	}

	if evidence.State != transcript.CopilotLifecycleTerminal {
		// Active or Unknown main-turn evidence: still working, or no state-bearing
		// event. Reset the stabilization streak so a later transient frame cannot
		// settle live work.
		h.ClearIdleSample(windowID, source)
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

	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:            windowID,
		Source:              source,
		Harness:             harness.Copilot,
		CapturedSeq:         capturedSeq,
		CapturedSnapshotGen: capturedSnapshotGen,
		At:                  now.Unix(),
		RequiredSamples:     requiredSamples,
		Generation:          generation,
		ConfirmStable:       confirmStable,
		ConfirmNotify:       confirmNotify,
		SessionTitle:        titleParts.Base,
		Account:             userVars.EffectiveProfile(),
		PipeMode:            false,
		Muted:               titleParts.Muted || userVars.Mute != "",
		SessionID:           sessionID,
		SourceEventID:       fmt.Sprintf("copilot-transcript-stop:%d:%d", windowID, capturedSeq),
		DiagnosticEvent:     copilotTranscriptStopProbeEvent,
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
	windowID int,
	source SourceID,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
	state transcript.CopilotLifecycleState,
) {
	if state != transcript.CopilotLifecycleActive {
		// Still terminal (the stop was genuinely final) or no trustworthy
		// evidence: leave 🛑 in place.
		return
	}

	h.stateMu.Lock()
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		h.stateMu.Unlock()
		return
	}
	// A reducer event landed during the read (native hook, a fresh prompt): it
	// already owns convergence and may have retired the probe stop, so discard.
	if st.ShadowSeq != capturedSeq || !h.screenProbeGenerationCurrentLocked(capturedSnapshotGen) {
		h.stateMu.Unlock()
		return
	}
	// Re-confirm under the lock that the probe stop is still the sole visible
	// contribution and recover the memoized session — this is the atomic
	// freshness-plus-mutation the settle path's ConfirmStable could not be.
	sessionID, stillRecovering := st.Reducer.ProbeStopRecovery(source, now.Unix())
	if !stillRecovering {
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
	h.appendWatchdogEventLocked(windowID, time.Unix(at, 0), copilotTranscriptStopProbeEvent, resultAction,
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
