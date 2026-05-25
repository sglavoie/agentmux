package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	screenPromptProbeEvent  = "ScreenPromptProbe"
	screenPromptProbeSource = "codex_screen"
)

// ProbeCodexScreenPrompts scans managed Codex windows and applies any visible
// prompt found on the current screen. The screen is authoritative for Codex:
// edit/request_user_input prompts can replace existing bolt/key guards with the
// lower-priority elicitation state, while terminal and special-purpose icons are
// never overwritten.
func (h *Handler) ProbeCodexScreenPrompts(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("codex prompt probe: list windows failed", "err", err)
		return
	}

	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			baseTitle, iconState, starred := palette.ParseTitle(tab.Title)
			if !codexPromptProbeCanOverwrite(iconState) {
				continue
			}
			for _, win := range tab.Windows {
				if win.UserVars.EffectiveHarness() != harness.Codex {
					continue
				}
				h.probeCodexWindow(now, win.ID, tab.Title, baseTitle, iconState, starred, win.UserVars)
			}
		}
	}
}

func codexPromptProbeCanOverwrite(iconState string) bool {
	switch iconState {
	case "", "bolt", "key", "question":
		return true
	default:
		return false
	}
}

func (h *Handler) probeCodexWindow(
	now time.Time,
	windowID int,
	tabTitle string,
	baseTitle string,
	currentIconState string,
	starred bool,
	userVars kitty.UserVars,
) {
	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("codex prompt probe: get-text failed", "window_id", windowID, "err", err)
		return
	}
	classification, ok := ClassifyCodexPromptScreen(screen)
	if !ok {
		return
	}

	if classification.Action == "elicitation" {
		h.guard.Clear(windowID)
	}
	allowed, effective := h.guard.TryUpdate(windowID, classification.Action, true)
	if !allowed {
		h.appendCodexPromptProbeEvent(now, windowID, baseTitle, currentIconState, userVars, classification, IconStatePreserve, true)
		return
	}

	iconState := IconState(effective)
	if iconState == "" {
		iconState = classification.IconState
	}
	h.recordCodexPromptProbeState(now, windowID, baseTitle, currentIconState, userVars, classification, iconState)
	if iconState != currentIconState {
		newTitle := palette.BuildTitle(baseTitle, iconState, starred)
		if newTitle != tabTitle {
			if err := h.kittyClient.SetTabTitle(kitty.MatchTabByWindowID(windowID), newTitle); err != nil {
				slog.Debug("codex prompt probe: set tab title failed", "window_id", windowID, "err", err)
			}
		}
	}
}

func (h *Handler) recordCodexPromptProbeState(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
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
	h.lastAction[windowID] = classification.Action
	if classification.Action == "active" {
		h.lastActiveEvent[windowID] = screenPromptProbeEvent
	}
	delete(h.notifiedStale, windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.SessionTitle = baseTitle
	meta.Durable.Profile = profile
	meta.Durable.Harness = harness.Codex
	meta.Durable.PipeMode = false
	meta.Durable.ActivityAt = now.Unix()
	meta.Durable.LastAction = classification.Action
	if classification.Action == "active" {
		meta.Durable.LastActiveEvent = screenPromptProbeEvent
	}
	h.windowMetadata[windowID] = meta

	h.appendEventLocked(windowID, codexPromptProbeEventRow(now, currentIconState, classification, resultIconState, false))
}

func (h *Handler) appendCodexPromptProbeEvent(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
	resultIconState string,
	suppressed bool,
) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	meta := h.windowMetadata[windowID]
	if baseTitle != "" {
		meta.Durable.SessionTitle = baseTitle
	}
	if profile := userVars.EffectiveProfile(); profile != "" {
		meta.Durable.Profile = profile
	}
	meta.Durable.Harness = harness.Codex
	h.windowMetadata[windowID] = meta

	h.appendEventLocked(windowID, codexPromptProbeEventRow(now, currentIconState, classification, resultIconState, suppressed))
}

func codexPromptProbeEventRow(
	now time.Time,
	currentIconState string,
	classification CodexPromptClassification,
	resultIconState string,
	suppressed bool,
) SessionEvent {
	ev := SessionEvent{
		At:               now.Unix(),
		Event:            screenPromptProbeEvent,
		Action:           classification.Action,
		Source:           screenPromptProbeSource,
		PromptKind:       string(classification.Kind),
		ResultIconState:  resultIconState,
		CurrentIconState: currentIconState,
	}
	if suppressed {
		ev.Suppressed = true
		ev.SuppressionReason = suppressionReasonGuardBlocked
	}
	return ev
}
