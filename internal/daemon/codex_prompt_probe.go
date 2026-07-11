package daemon

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	screenPromptProbeEvent  = "ScreenPromptProbe"
	screenClearProbeEvent   = "ScreenClearProbe"
	screenStopProbeEvent    = "ScreenStopProbe"
	screenPromptProbeSource = "codex_screen"
)

// ProbeCodexScreenPrompts scans managed Codex windows and applies any visible
// prompt found on the current screen. The screen is authoritative for Codex:
// edit/request_user_input prompts can replace existing bolt/key guards with the
// lower-priority elicitation state, while terminal and special-purpose icons are
// never overwritten. A stopped Codex tab gets one extra conservative check for
// the cleared idle UI because Codex does not emit an immediate hook for idle
// `/clear`.
func (h *Handler) ProbeCodexScreenPrompts(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("codex prompt probe: list windows failed", "err", err)
		return
	}
	h.ProbeCodexScreenPromptsWithSnapshot(now, windows)
	h.renderSnapshot(windows)
}

func (h *Handler) ProbeCodexScreenPromptsWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			win, ok := codexPromptProbeTarget(tab)
			if !ok {
				continue
			}
			if parts.State == "stop" {
				h.probeCodexClearWindow(now, win.ID, parts, win.UserVars)
				continue
			}
			if !codexPromptProbeCanOverwrite(parts.State) {
				continue
			}
			h.probeCodexWindow(now, win.ID, parts, win.UserVars)
			// Completed-turn fallback: only a still-bolt tab can be a stuck
			// active turn. Prompt classification above wins first (it clears the
			// bolt to key/question when a prompt is visible), and the completed
			// classifier bails on any visible prompt, so this only fires when no
			// prompt was found and the turn genuinely finished with no Stop hook.
			if parts.State == "bolt" {
				h.probeCodexCompletedTurnWindow(win.ID, parts, win.UserVars)
			}
		}
	}
}

func codexPromptProbeTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		return owner, owner.UserVars.EffectiveHarness() == harness.Codex
	}
	if tab == nil {
		return nil, false
	}

	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if !codexPromptProbeShouldScan(*win) {
			continue
		}
		if fallback == nil {
			fallback = win
		}
		if win.IsActive {
			return win, true
		}
	}
	if fallback == nil {
		return nil, false
	}
	return fallback, true
}

func codexPromptProbeShouldScan(win kitty.Window) bool {
	switch win.UserVars.EffectiveHarness() {
	case harness.Codex:
		return true
	case "":
		return codexPromptProbeHasCodexForeground(win)
	default:
		return false
	}
}

func codexPromptProbeHasCodexForeground(win kitty.Window) bool {
	if codexPromptProbeCmdlineIsCodex(win.Cmdline) {
		return true
	}
	for _, proc := range win.ForegroundProcesses {
		if codexPromptProbeCmdlineIsCodex(proc.Cmdline) {
			return true
		}
	}
	return false
}

func codexPromptProbeCmdlineIsCodex(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	return strings.EqualFold(filepath.Base(cmdline[0]), "codex")
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
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("codex prompt probe: get-text failed", "window_id", windowID, "err", err)
		return
	}
	classification, ok := ClassifyCodexPromptScreen(screen)
	if !ok {
		// No prompt on screen: withdraw any earlier probe observation so an
		// answered prompt cannot strand a key/question fact over the session's
		// real activity.
		h.retractCodexPromptObservation(now, windowID)
		return
	}

	if classification.Action == "elicitation" {
		h.guard.Clear(windowID)
	}
	allowed, effective := h.guard.TryUpdate(windowID, classification.Action, true)
	if !allowed {
		h.appendCodexPromptProbeEvent(now, windowID, titleParts.Base, titleParts.State, userVars, classification, IconStatePreserve, true)
		return
	}

	iconState := IconState(effective)
	if iconState == "" {
		iconState = classification.IconState
	}
	h.recordCodexPromptProbeState(now, windowID, titleParts.Base, titleParts.State, userVars, classification, iconState)
	if iconState != titleParts.State {
		// Notify only on the state transition so a still-visible prompt does not
		// re-dispatch every probe tick; the notifier's per-(window, action)
		// cooldown and permission debounce dedupe against any hook-path
		// notification for the same prompt. pipeMode is deliberately false: the
		// probe only scans interactive Codex windows.
		h.maybeNotify(windowID, titleParts.Base, userVars.EffectiveProfile(), classification.Action, false)
	}
}

// retractCodexPromptObservation withdraws the screen probe's own fact once
// the prompt it observed is gone, then republishes the reduced state.
func (h *Handler) retractCodexPromptObservation(now time.Time, windowID int) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		return
	}
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	st.ShadowSeq++
	if !st.Reducer.Retract(source, st.ShadowSeq, now.Unix()) {
		return
	}
	h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
	h.markWindowDirtyLocked(windowID)
}

func (h *Handler) probeCodexClearWindow(
	_ time.Time,
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("codex clear probe: get screen failed", "window_id", windowID, "err", err)
		return
	}
	if !codexScreenLooksCleared(screen) {
		return
	}

	allText, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "all")
	if err != nil {
		slog.Debug("codex clear probe: get full text failed", "window_id", windowID, "err", err)
		return
	}
	if !ClassifyCodexClearScreen(screen, allText) {
		return
	}
	h.setMutedFromSnapshot(windowID, titleParts.Muted || userVars.Mute != "")

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         windowID,
		Action:           "user_clear",
		Event:            screenClearProbeEvent,
		Source:           screenPromptProbeSource,
		Account:          userVars.EffectiveProfile(),
		Harness:          harness.Codex,
		SessionTitle:     titleParts.Base,
		currentIconState: titleParts.State,
		ResetFlow:        true,
	})
	if !resp.OK {
		slog.Debug("codex clear probe: state update failed", "window_id", windowID, "err", resp.Error)
		return
	}
	if !resp.Allowed || resp.IconState == IconStatePreserve {
		return
	}
}

// probeCodexCompletedTurnWindow recovers a Codex tab whose bolt icon is stuck on
// a finished assistant turn. Codex sometimes leaves the completed-turn UI on
// screen without emitting a Stop hook, so the daemon treats the screen as
// authoritative and drives a synthetic `stop` through the normal state-update
// path. The stop guard priority (2) outranks the stuck active (1), so this
// breaks through, but protected states never reach here because the caller only
// invokes this for a `bolt` tab.
func (h *Handler) probeCodexCompletedTurnWindow(
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		slog.Debug("codex stop probe: get screen failed", "window_id", windowID, "err", err)
		return
	}
	if !ClassifyCodexCompletedTurnScreen(screen) {
		return
	}
	h.setMutedFromSnapshot(windowID, titleParts.Muted || userVars.Mute != "")

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         windowID,
		Action:           "stop",
		Event:            screenStopProbeEvent,
		Source:           screenPromptProbeSource,
		Account:          userVars.EffectiveProfile(),
		Harness:          harness.Codex,
		SessionTitle:     titleParts.Base,
		currentIconState: titleParts.State,
	})
	if !resp.OK {
		slog.Debug("codex stop probe: state update failed", "window_id", windowID, "err", resp.Error)
		return
	}
	if !resp.Allowed || resp.IconState == IconStatePreserve {
		return
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

	st := h.ensureWindowStateLocked(windowID)
	st.Activity = now
	st.LastSessionTitle = baseTitle
	// The probe only scans interactive Codex windows — same reason
	// probeCodexWindow's maybeNotify passes pipeMode=false.
	st.LastPipeMode = false
	profile := userVars.EffectiveProfile()
	if profile != "" {
		st.LastAccount = profile
	}
	st.LastHarness = harness.Codex
	st.LastAction = classification.Action
	if classification.Action == "active" {
		st.LastActiveEvent = screenPromptProbeEvent
	}
	st.NotifiedStale = false
	st.DesiredIcon = resultIconState
	st.StateSet = true
	// The reducer owns visible state, so the screen-probe observation must
	// become a fact: fold it onto the probe's own source and publish the
	// reduced outcome. The screen is authoritative for Codex (the probe only
	// fires on states it may overwrite), so the event is marked as a prompt —
	// it recovers a terminal fact the same way the legacy prompt bypass did.
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceProbe, Key: screenPromptProbeSource},
		Action: classification.Action,
		Seq:    st.ShadowSeq,
		At:     now.Unix(),
		Prompt: true,
	})
	st.Reducer.Compact(now.Unix())
	h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
	h.markWindowDirtyLocked(windowID)

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
