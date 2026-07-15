package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	// claudeScreenStopProbeSource keys the SourceProbe fact the Claude idle
	// stop settles under. It is deliberately distinct from the Codex probe's
	// codex_screen key so the two harnesses' idle evidence never collide, and
	// distinct from the context-limit probe's own SourceProbe fact
	// (claudeContextLimitFactSource) so a stuck-turn "stop" and a context-limit
	// "error" never share one reducer fact.
	claudeScreenStopProbeSource = "claude_screen"

	// claudeIdleProbeRequiredSamples is the number of consecutive agreeing
	// screen reads required before a composer-idle screen settles the stale
	// interactive bolt. Claude's completed-turn screen is quieter than a running
	// one, so two samples defend against a single transient frame. The
	// interruption path is stronger evidence and settles immediately (one
	// sample).
	claudeIdleProbeRequiredSamples = 2
)

// ProbeClaudeScreenStop scans managed Claude windows for a finished interactive
// turn — a normal completion or a user interruption — whose native Stop hook
// never arrived, leaving a stuck ⚡. It lists windows, runs the probe against the
// snapshot, and renders. Production drives the probe from runScreenProbes with
// the cached snapshot; this wrapper mirrors ProbeClaudeContextLimit for callers
// (and tests) that want the full list+render round-trip.
func (h *Handler) ProbeClaudeScreenStop(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("claude stop probe: list windows failed", "err", err)
		return
	}
	h.probeClaudeScreenStopWithSnapshot(now, screenProbeSnapshot{windows: windows, reducerReceipts: reducerReceipts})
	h.renderSnapshot(windows)
}

// ProbeClaudeScreenStopWithSnapshot runs the Claude idle stop probe against an
// already-published Kitty snapshot. Direct sessions retain the managed
// title-owner selection; a borrowed session follows its live reducer binding to
// read the unmanaged producer pane and apply the result to the managed target.
// Only a target whose current reduced icon is a stuck ⚡ is considered: any
// other visible state is either already terminal or a higher-precedence icon
// the idle path must never downgrade.
func (h *Handler) ProbeClaudeScreenStopWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeClaudeScreenStopWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeClaudeScreenStopWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	for _, binding := range h.screenProbeBindings(snapshot, harness.Claude) {
		_, tab, target, ok := kitty.FindWindowLocationByID(snapshot.windows, binding.TargetWindowID)
		if !ok {
			continue
		}
		h.probeClaudeIdleWindow(now, binding, palette.ParseTitleParts(tab.Title), target.UserVars)
	}
}

// probeClaudeIdleWindow recovers a Claude tab whose ⚡ is stuck on a finished
// assistant turn — a normal completion or a user interruption — that left no
// Stop hook behind. It mirrors probeCodexIdleWindow: the reducer receipt
// sequence is captured before the cached Kitty snapshot and revalidated under
// stateMu BEFORE the slow off-lock screen read, so a concurrent prompt (or any
// reducer event) landing after capture discards the stale route/read; the synthetic stop is applied through the
// race-safe authoritative-idle path under a distinct SourceProbe fact, so ASL
// leases, subagents, pipe-mode children, and higher-precedence attention/error
// icons all still win at reduce time.
//
// The interruption marker is stronger evidence than a bare idle composer, so it
// settles immediately (one sample) while a plain composer-idle screen must agree
// across two consecutive samples first.
func (h *Handler) probeClaudeIdleWindow(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	source := SourceID{Kind: SourceProbe, Key: claudeScreenStopProbeSource}
	targetWindowID := binding.TargetWindowID

	// Revalidate the snapshot receipt before the off-lock GetText. A window with no
	// reducer facts (an unmanaged/foreground-only bolt) has no stale interactive
	// work for the probe to settle, so bail without a screen read and drop any
	// in-flight stabilization streak.
	h.stateMu.Lock()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return icon == "bolt"
	}) {
		h.stateMu.Unlock()
		slog.Debug("claude stop probe: discarded stale snapshot",
			"source_window_id", binding.SourceWindowID, "target_window_id", targetWindowID)
		return
	}
	st, ok := h.windowStateLocked(targetWindowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(idleSampleRef{windowID: targetWindowID, key: source.Key})
		h.stateMu.Unlock()
		return
	}
	sessionTitle := titleParts.Base
	account := userVars.EffectiveProfile()
	pipeMode := st.LastPipeMode
	muted := userVars.Mute != ""
	if binding.SourceWindowID == targetWindowID {
		muted = muted || titleParts.Muted
	}
	if st.LastSessionTitle != "" {
		sessionTitle = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		account = st.LastAccount
	}
	muted = muted || st.Muted
	h.stateMu.Unlock()

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "screen")
	if err != nil {
		slog.Debug("claude stop probe: get screen failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		h.ClearIdleSample(targetWindowID, source)
		return
	}

	interrupted := ClassifyClaudeInterruptedScreen(screen)
	if !interrupted && !ClassifyClaudeIdleScreen(screen) {
		// Still working, a queued-input composer, a blocking prompt, a
		// context-limited screen, or unrelated output: reset the stabilization
		// streak so a later transient idle frame cannot settle live work.
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	required := claudeIdleProbeRequiredSamples
	if interrupted {
		required = 1
	}

	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:            targetWindowID,
		Source:              source,
		Harness:             harness.Claude,
		CapturedSeq:         binding.TargetReceipt,
		CapturedSnapshotGen: binding.SnapshotGen,
		ProbeBinding:        binding,
		At:                  now.Unix(),
		RequiredSamples:     required,
		SessionTitle:        sessionTitle,
		Account:             account,
		PipeMode:            pipeMode,
		Muted:               muted,
		SourceEventID:       fmt.Sprintf("claude-screen-stop:%d:%d:%d", binding.SourceWindowID, targetWindowID, binding.TargetReceipt),
		DiagnosticEvent:     screenStopProbeEvent,
	})
}
