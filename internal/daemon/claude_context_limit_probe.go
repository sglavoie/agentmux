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
// don't churn the tab title on every 1.5 s tick. The flag is cleared by an
// "active"-class event re-entering the window (S3) or when the window closes.
func (h *Handler) ProbeClaudeContextLimit(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	if !h.claudeContextLimitProbeEnabled {
		return
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("claude context-limit probe: list windows failed", "err", err)
		return
	}
	h.ProbeClaudeContextLimitWithSnapshot(now, windows)
	h.renderSnapshot(windows)
}

func (h *Handler) ProbeClaudeContextLimitWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	if !h.claudeContextLimitProbeEnabled {
		return
	}
	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			if !claudeContextLimitProbeCanOverwrite(parts.State) {
				continue
			}
			win, ok := claudeContextLimitProbeTarget(tab)
			if !ok {
				continue
			}
			h.probeClaudeContextLimitWindow(now, win.ID, parts, win.UserVars)
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
) {
	h.stateMu.Lock()
	st, _ := h.windowStateLocked(windowID)
	if st != nil && st.ContextLimitNotified {
		h.stateMu.Unlock()
		return
	}
	// Capture pipeMode before recordClaudeContextLimitProbeState resets it to
	// false so the notify block below sees the value hooks last reported.
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

	allowed, _ := h.guard.TryUpdate(windowID, claudeContextLimitProbeAction, true)
	if !allowed {
		slog.Debug("claude context-limit probe: guard blocked stop_failure", "window_id", windowID)
		return
	}

	resultIconState := IconState(claudeContextLimitProbeAction)
	h.recordClaudeContextLimitProbeState(now, windowID, titleParts.Base, titleParts.State, userVars, resultIconState)

	if h.notifier != nil {
		// The screen-based probe only fires on interactive Claude sessions —
		// pipe-mode windows cannot show the context-limit banner. We still pass
		// the pre-recorded pipeMode through MaybeNotify so the notifier's own
		// suppression remains the single source of truth.
		h.stateMu.Lock()
		account := ""
		if st, ok := h.windowStateLocked(windowID); ok {
			account = st.LastAccount
		}
		h.stateMu.Unlock()
		h.notifier.MaybeNotify(windowID, titleParts.Base, account, claudeContextLimitProbeAction, pipeMode)
	}
}

func (h *Handler) recordClaudeContextLimitProbeState(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
	resultIconState string,
) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	st := h.ensureWindowStateLocked(windowID)
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
	st.DesiredIcon = resultIconState
	st.StateSet = true
	h.markWindowDirtyLocked(windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.SessionTitle = baseTitle
	meta.Durable.Profile = profile
	meta.Durable.Harness = harness.Claude
	meta.Durable.PipeMode = false
	meta.Durable.ActivityAt = now.Unix()
	meta.Durable.LastAction = claudeContextLimitProbeAction
	h.windowMetadata[windowID] = meta

	h.appendEventLocked(windowID, SessionEvent{
		At:               now.Unix(),
		Event:            claudeContextLimitProbeEvent,
		Action:           claudeContextLimitProbeAction,
		Source:           claudeContextLimitProbeSource,
		ResultIconState:  resultIconState,
		CurrentIconState: currentIconState,
		ContextLimit:     true,
	})
}
