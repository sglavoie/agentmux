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

// ProbeClaudeContextLimit scans managed Claude windows whose current icon is
// either active-class (bolt, subagent) or empty for the "Context limit
// reached" recovery banner. When found, the window's icon is swapped to the
// error state via the stop_failure action so the user has a visual signal
// that the session is stuck and needs /compact or /clear.
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

	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			parts := palette.ParseTitleParts(tab.Title)
			if !claudeContextLimitProbeCanOverwrite(parts.State) {
				continue
			}
			for _, win := range tab.Windows {
				if win.UserVars.EffectiveHarness() != harness.Claude {
					continue
				}
				h.probeClaudeContextLimitWindow(now, win.ID, tab.Title, parts, win.UserVars)
			}
		}
	}
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
	tabTitle string,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	h.stateMu.Lock()
	if h.contextLimitNotified[windowID] {
		h.stateMu.Unlock()
		return
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

	if resultIconState != titleParts.State {
		newParts := titleParts
		newParts.State = resultIconState
		newTitle := palette.BuildTitleParts(newParts)
		if newTitle != tabTitle {
			if err := h.kittyClient.SetTabTitle(kitty.MatchTabByWindowID(windowID), newTitle); err != nil {
				slog.Debug("claude context-limit probe: set tab title failed", "window_id", windowID, "err", err)
			}
		}
	}

	if h.notifier != nil {
		// The screen-based probe only fires on interactive Claude sessions —
		// pipe-mode windows cannot show the context-limit banner. We still pass
		// the resolved pipeMode through MaybeNotify so the notifier's own
		// suppression remains the single source of truth.
		h.stateMu.Lock()
		pipeMode := h.lastPipeMode[windowID]
		account := h.lastAccount[windowID]
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

	h.activity[windowID] = now
	h.lastSessionTitle[windowID] = baseTitle
	h.lastPipeMode[windowID] = false
	profile := userVars.EffectiveProfile()
	if profile != "" {
		h.lastAccount[windowID] = profile
	}
	h.lastAction[windowID] = claudeContextLimitProbeAction
	h.contextLimitNotified[windowID] = true
	delete(h.notifiedStale, windowID)

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
