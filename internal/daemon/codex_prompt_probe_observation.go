package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

func (h *Handler) probeCodexWindow(
	now time.Time,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	screens *activity.ScreenTextCache,
) {
	targetWindowID := binding.TargetWindowID
	// The receipt was captured before the ListWindows call that produced the
	// cached eligibility evidence. Reject it before beginning the screen read if
	// any reducer event landed since; the apply path rechecks after the read.
	h.stateMu.Lock()
	fresh := h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return codexPromptProbeCanOverwrite(icon)
	})
	h.stateMu.Unlock()
	if !fresh {
		slog.Debug("codex prompt probe: discarded stale snapshot", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID)
		return
	}

	screen, err := screens.Screen(binding.SourceWindowID)
	if err != nil {
		slog.Debug("codex prompt probe: get-text failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	classification, ok := activity.ClassifyCodexPromptScreen(screen)
	if !ok {
		// No prompt on screen: withdraw any earlier probe observation so an
		// answered prompt cannot strand a key/question fact over the session's
		// real activity.
		h.retractCodexPromptObservation(now, binding)
		return
	}
	h.applyCodexPromptObservation(now, binding, titleParts, userVars, classification)
}

// applyCodexPromptObservation folds one classified screen prompt into the
// window atomically under stateMu. The receipt recheck runs before ANY
// mutation — guard, reducer, audit, renderer, or notification — so a stale
// observation is discarded completely and a concurrent reducer event always
// outranks the stale screen evidence. Holding stateMu across the guard calls
// is safe: stateMu → guard.mu is an established ordering
// (markWindowStateDirtyLocked snapshots the guard under stateMu) and keeps the
// guard mutation atomic with the recheck.
//
// The audit row and the notification both derive from the ACTUAL reduced
// transition, never the requested prompt icon: a prompt masked by a
// higher-precedence fact records the icon that actually won and does not
// notify.
func (h *Handler) applyCodexPromptObservation(
	now time.Time,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	classification activity.CodexPromptClassification,
) {
	windowID := binding.TargetWindowID
	h.stateMu.Lock()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return codexPromptProbeCanOverwrite(icon)
	}) {
		h.stateMu.Unlock()
		slog.Debug("codex prompt probe: discarded stale observation", "window_id", windowID)
		return
	}
	if classification.Action == "elicitation" {
		h.guard.Clear(windowID)
	}
	allowed, effectiveAction := h.guard.TryUpdate(windowID, classification.Action, true)
	if !allowed {
		h.appendCodexPromptProbeEventLocked(now, windowID, titleParts.Base, titleParts.State, userVars,
			classification, protocol.IconStatePreserve, true, binding.LegacyDirectManaged, binding.SourceWindowID)
		h.stateMu.Unlock()
		return
	}

	iconState := protocol.IconState(effectiveAction)
	if iconState == "" {
		iconState = classification.IconState
	}
	before, effective := h.recordCodexPromptProbeStateLocked(now, windowID, titleParts.Base, titleParts.State,
		userVars, classification, binding.LegacyDirectManaged, binding.SourceWindowID)
	h.stateMu.Unlock()

	if effective.Icon == iconState && before.Icon != effective.Icon {
		// Notify only when the reduced visible state actually transitioned to the
		// prompt's icon, so a still-visible prompt does not re-dispatch every
		// probe tick and a prompt masked by a higher-precedence fact never
		// reports itself; the notifier's per-(window, action) cooldown and
		// permission debounce dedupe against any hook-path notification for the
		// same prompt. pipeMode is deliberately false: the probe only scans
		// interactive Codex windows.
		h.maybeNotify(windowID, titleParts.Base, userVars.EffectiveProfile(), classification.Action, false)
	}
}

// retractCodexPromptObservation withdraws the screen probe's own fact once
// the prompt it observed is gone, then republishes the reduced state. The
// no-prompt read happened off-lock, so it is discarded like any other stale
// observation when the window's reducer receipt moved past capturedSeq: the
// reducer event that landed during the read owns the window's state, and a
// prompt it may have re-created must not be withdrawn on stale evidence.
func (h *Handler) retractCodexPromptObservation(now time.Time, binding probes.Binding) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.screenProbeBindingCurrentLocked(binding) {
		return
	}
	windowID := binding.TargetWindowID
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		return
	}
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource}
	if !st.Reducer.CanRetract(source) {
		return
	}
	nextSeq := st.ShadowSeq + 1
	if !st.Reducer.Retract(source, nextSeq, now.Unix()) {
		return
	}
	// The screen probe installed the elicitation guard together with its fact.
	// If Codex never emits PostToolUse, withdrawing only the reducer fact would
	// leave ordinary activity suppressed behind a question that is no longer
	// visible. The receipt check above proves no newer hook state landed during
	// the read, so it is safe to withdraw the matching guard as well.
	h.guard.Clear(windowID)
	st.ShadowSeq = nextSeq
	effective := st.Reducer.Reduce(now.Unix())
	st.LastAction = processLifecycleActionForIcon(effective.Icon)
	meta := h.windowMetadata[windowID]
	meta.Durable.LastAction = st.LastAction
	h.windowMetadata[windowID] = meta
	h.publishEffectiveLocked(windowID, st, effective)
	h.syncRendererMetadataLocked(windowID)
	h.markWindowDirtyLocked(windowID)
}

func (h *Handler) probeCodexClearWindow(
	now time.Time,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	screens *activity.ScreenTextCache,
) {
	targetWindowID := binding.TargetWindowID
	// Bind the stale-stop eligibility from the cached title to its reducer
	// receipt before either slow read. The apply path rechecks after both.
	h.stateMu.Lock()
	fresh := h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
		return icon == "" || icon == "stop"
	})
	h.stateMu.Unlock()
	if !fresh {
		slog.Debug("codex clear probe: discarded stale snapshot", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID)
		return
	}

	screen, err := screens.Screen(binding.SourceWindowID)
	if err != nil {
		slog.Debug("codex clear probe: get screen failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	if !screenstate.CodexScreenLooksCleared(screen) {
		return
	}

	allText, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "all")
	if err != nil {
		slog.Debug("codex clear probe: get full text failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	if !screenstate.ClassifyCodexClearScreen(screen, allText) {
		return
	}
	h.applyCodexClearObservation(now, binding, titleParts, userVars)
}
