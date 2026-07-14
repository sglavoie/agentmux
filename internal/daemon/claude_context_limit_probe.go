package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	claudeContextLimitProbeEvent  = "ScreenContextLimitProbe"
	claudeContextLimitProbeSource = "claude_screen"
	claudeContextLimitProbeAction = "stop_failure"

	// claudeContextLimitFactSource keys the SourceProbe fact the context-limit
	// banner settles its sticky stop_failure under. It is deliberately distinct
	// from the Claude idle-stop probe's claude_screen key (claudeScreenStopProbeSource)
	// so a stuck-turn "stop" observation and a context-limit "error" observation
	// never share one reducer fact and overwrite each other's terminal state.
	claudeContextLimitFactSource = "claude_context_limit"
)

// ProbeClaudeContextLimit scans each tab's managed Claude title owner when the
// current icon is either active-class (bolt, subagent) or empty for the
// "Context limit reached" recovery banner. When found, the window's icon is
// swapped to the error state via the stop_failure action so the user has a
// visual signal that the session is stuck and needs /compact or /clear.
//
// Empty state is included because some upstream Claude hook events resolve
// to "idle" or "clear" and wipe the bolt to empty before the probe ticks;
// without covering empty, a stalled session would show no emoji at all.
// Banner detection itself (contextlimit.ScreenShowsMarker) remains the real
// gate, so the broader filter cannot trigger false positives.
//
// The icon swap is deduped per stall episode by contextLimitNotified so we
// don't churn the tab title on every probe cycle (the shared renderInterval
// ticker driving runScreenProbes). The flag is cleared by an
// "active"-class event re-entering the window (S3) or when the window closes.
func (h *Handler) ProbeClaudeContextLimit(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	if !h.claudeContextLimitProbeEnabled {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("claude context-limit probe: list windows failed", "err", err)
		return
	}
	h.probeClaudeContextLimitWithSnapshot(now, screenProbeSnapshot{windows: windows, reducerReceipts: reducerReceipts})
	h.renderSnapshot(windows)
}

func (h *Handler) ProbeClaudeContextLimitWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeClaudeContextLimitWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeClaudeContextLimitWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	if !h.claudeContextLimitProbeEnabled {
		return
	}
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			if !claudeContextLimitProbeCanOverwrite(parts.State) {
				continue
			}
			win, ok := claudeContextLimitProbeTarget(tab)
			if !ok {
				continue
			}
			h.probeClaudeContextLimitWindow(now, win.ID, parts, win.UserVars, snapshot.receipt(win.ID), snapshot.gen)
		}
	}
}

// Claude deliberately probes only the managed tab-title owner. Unlike the
// Codex prompt probe, it has no unmanaged foreground fallback because context
// limit recovery should only mutate windows already owned by Claude hooks.
func claudeContextLimitProbeTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	owner, ok := kitty.FindTabTitleOwner(tab)
	if !ok {
		return nil, false
	}
	return owner, owner.UserVars.EffectiveHarness() == harness.Claude
}

func claudeContextLimitProbeCanOverwrite(iconState string) bool {
	switch iconState {
	case "", "bolt", "subagent":
		return true
	default:
		return false
	}
}

func (h *Handler) probeClaudeContextLimitWindow(
	now time.Time,
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	h.stateMu.Lock()
	if !h.screenProbeEligibilityCurrentLocked(windowID, capturedSeq, capturedSnapshotGen, harness.Claude, func(icon string) bool {
		return claudeContextLimitProbeCanOverwrite(icon)
	}) {
		h.stateMu.Unlock()
		slog.Debug("claude context-limit probe: discarded stale snapshot", "window_id", windowID)
		return
	}
	st, _ := h.windowStateLocked(windowID)
	if st != nil && st.ContextLimitNotified {
		h.stateMu.Unlock()
		return
	}
	// Capture pipeMode before recordClaudeContextLimitProbeStateLocked resets
	// it to false so the notify block below sees the value hooks last reported.
	// The window-wide receipt arrived with the cached snapshot and was just
	// revalidated in this same critical section.
	pipeMode := false
	if st != nil {
		pipeMode = st.LastPipeMode
	}
	h.stateMu.Unlock()

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("claude context-limit probe: get-text failed", "window_id", windowID, "err", err)
		return
	}
	if !contextlimit.ScreenShowsMarker(screen) {
		return
	}

	h.stateMu.Lock()
	// Receipt recheck before ANY mutation: a reducer event that landed during
	// the screen read — a fresh prompt, a terminal hook, an error, a /clear —
	// makes this banner observation stale, and a stale read must not reassert
	// an error the window has since moved past. Discarding performs no guard,
	// reducer, audit, renderer, or notification mutation. Holding stateMu
	// through the guard call and the reducer application keeps them atomic with
	// this check; stateMu → guard.mu is an established ordering
	// (markWindowStateDirtyLocked snapshots the guard under stateMu).
	if !h.screenProbeGenerationCurrentLocked(capturedSnapshotGen) ||
		!h.windowReceiptCurrentLocked(windowID, capturedSeq) {
		h.stateMu.Unlock()
		slog.Debug("claude context-limit probe: discarded stale observation", "window_id", windowID)
		return
	}
	allowed, _ := h.guard.TryUpdate(windowID, claudeContextLimitProbeAction, true)
	if !allowed {
		h.stateMu.Unlock()
		slog.Debug("claude context-limit probe: guard blocked stop_failure", "window_id", windowID)
		return
	}
	before, effective, account := h.recordClaudeContextLimitProbeStateLocked(now, windowID, titleParts.Base, titleParts.State, userVars)
	h.stateMu.Unlock()

	if h.notifier != nil {
		// Notification derives from the ACTUAL reduced transition, not the
		// requested action: fire only when the reduced visible icon truly
		// becomes the error state. A probe whose synthetic stop_failure is
		// masked by a higher-precedence contribution — or whose error was
		// already visible — must never report a synthetic error.
		//
		// The screen-based probe only fires on interactive Claude sessions —
		// pipe-mode windows cannot show the context-limit banner. We still pass
		// the pre-recorded pipeMode through MaybeNotify so the notifier's own
		// suppression remains the single source of truth.
		if effective.Icon == "error" && before.Icon != "error" {
			h.notifier.MaybeNotify(windowID, titleParts.Base, account, claudeContextLimitProbeAction, pipeMode)
		}
	}
}

// recordClaudeContextLimitProbeStateLocked folds a detected context-limit
// banner into the window's reducer as a sticky SourceProbe stop_failure fact
// and publishes the reduced result as the authoritative visible state. It
// returns the reduced effective state before and after the settle (so the
// caller can drive a transition-only notification) plus the window's account.
// Because the error is a real reducer fact (not a bare scalar write) it stays
// consistent with the durable shadow: a later reducer publication reduces over
// the same fact set, a passive idle/clear preserves the probe-owned error
// (idlePreservesIcon covers "error"), and an explicit user_clear or a new
// prompt clears it through the normal reducer paths. Callers hold stateMu and
// have already verified the window's reducer receipt is still current.
func (h *Handler) recordClaudeContextLimitProbeStateLocked(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
) (before, effective EffectiveState, account string) {
	st := h.ensureWindowStateLocked(windowID)
	before = st.Effective
	st.Activity = now
	st.LastSessionTitle = baseTitle
	st.LastPipeMode = false
	profile := userVars.EffectiveProfile()
	if profile != "" {
		st.LastAccount = profile
	}
	st.LastHarness = harness.Claude
	st.LastAction = claudeContextLimitProbeAction
	st.ContextLimitNotified = true
	st.NotifiedStale = false

	// The reducer owns visible state: fold the banner onto the probe's own
	// SourceProbe fact as a sticky stop_failure rather than writing the error
	// scalar directly. Advancing ShadowSeq makes this a window-wide receipt
	// mutation, so an in-flight authoritative-idle probe that captured an older
	// generation is discarded and cannot retire the error; publishEffectiveLocked
	// keeps DesiredIcon, Looping, Effective, and the exported Shadow effective
	// fields consistent with the authoritative reducer.
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceProbe, Key: claudeContextLimitFactSource},
		Action: claudeContextLimitProbeAction,
		Seq:    st.ShadowSeq,
		At:     now.Unix(),
	})
	st.Reducer.Compact(now.Unix())
	effective = st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
	h.markWindowDirtyLocked(windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.SessionTitle = baseTitle
	meta.Durable.Profile = profile
	meta.Durable.Harness = harness.Claude
	meta.Durable.PipeMode = false
	meta.Durable.ActivityAt = now.Unix()
	meta.Durable.LastAction = claudeContextLimitProbeAction
	h.windowMetadata[windowID] = meta

	// The audit row records the ACTUAL reduced icon, not the requested action, so
	// a masked probe never claims an error the window does not actually show.
	h.appendEventLocked(windowID, SessionEvent{
		At:               now.Unix(),
		Event:            claudeContextLimitProbeEvent,
		Action:           claudeContextLimitProbeAction,
		Source:           claudeContextLimitProbeSource,
		ResultIconState:  effective.Icon,
		CurrentIconState: currentIconState,
		ContextLimit:     true,
	})
	return before, effective, st.LastAccount
}
