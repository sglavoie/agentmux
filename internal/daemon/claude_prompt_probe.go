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
// already-published Kitty snapshot. It reuses the context-limit probe's managed
// title-owner selection (only the Claude tab-title owner is scanned, never an
// unmanaged foreground sibling) and only considers a tab whose current icon is a
// stuck ⚡: any other visible state is either already terminal or a
// higher-precedence icon the idle path must never downgrade.
func (h *Handler) ProbeClaudeScreenStopWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeClaudeScreenStopWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeClaudeScreenStopWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			if !claudeStopProbeCanOverwrite(parts.State) {
				continue
			}
			win, ok := claudeContextLimitProbeTarget(tab)
			if !ok {
				continue
			}
			h.probeClaudeIdleWindow(now, win.ID, parts, win.UserVars, snapshot.receipt(win.ID), snapshot.gen)
		}
	}
}

// claudeStopProbeCanOverwrite reports whether the current visible icon is a
// stuck active turn the idle probe may settle. Only ⚡ (bolt) qualifies: a
// completed session already shows 🛑, and question/permission/error/warning/
// subagent/compact are protected states the idle path must never downgrade. The
// reducer's own precedence is the final guard (a live ASL lease, subagent, or
// pipe-mode fact still wins at reduce time even for a bolt tab), so this filter
// is an early-exit, not the safety boundary.
func claudeStopProbeCanOverwrite(iconState string) bool {
	return iconState == "bolt"
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
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	source := SourceID{Kind: SourceProbe, Key: claudeScreenStopProbeSource}

	// Revalidate the snapshot receipt before the off-lock GetText. A window with no
	// reducer facts (an unmanaged/foreground-only bolt) has no stale interactive
	// work for the probe to settle, so bail without a screen read and drop any
	// in-flight stabilization streak.
	h.stateMu.Lock()
	if !h.screenProbeEligibilityCurrentLocked(windowID, capturedSeq, capturedSnapshotGen, harness.Claude, func(icon string) bool {
		return icon == "bolt"
	}) {
		h.stateMu.Unlock()
		slog.Debug("claude stop probe: discarded stale snapshot", "window_id", windowID)
		return
	}
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(idleSampleRef{windowID: windowID, key: source.Key})
		h.stateMu.Unlock()
		return
	}
	h.stateMu.Unlock()

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("claude stop probe: get screen failed", "window_id", windowID, "err", err)
		h.ClearIdleSample(windowID, source)
		return
	}

	interrupted := ClassifyClaudeInterruptedScreen(screen)
	if !interrupted && !ClassifyClaudeIdleScreen(screen) {
		// Still working, a queued-input composer, a blocking prompt, a
		// context-limited screen, or unrelated output: reset the stabilization
		// streak so a later transient idle frame cannot settle live work.
		h.ClearIdleSample(windowID, source)
		return
	}
	required := claudeIdleProbeRequiredSamples
	if interrupted {
		required = 1
	}

	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:            windowID,
		Source:              source,
		Harness:             harness.Claude,
		CapturedSeq:         capturedSeq,
		CapturedSnapshotGen: capturedSnapshotGen,
		At:                  now.Unix(),
		RequiredSamples:     required,
		SessionTitle:        titleParts.Base,
		Account:             userVars.EffectiveProfile(),
		PipeMode:            false,
		Muted:               titleParts.Muted || userVars.Mute != "",
		SourceEventID:       fmt.Sprintf("claude-screen-stop:%d:%d", windowID, capturedSeq),
		DiagnosticEvent:     screenStopProbeEvent,
	})
}
