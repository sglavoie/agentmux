package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// recordCodexPromptProbeStateLocked folds a classified screen prompt into the
// window's reducer as the probe's own SourceProbe fact and publishes the
// reduced result as the authoritative visible state. It returns the reduced
// effective state before and after the application so the caller can derive
// the notification from the actual reduced transition. Callers hold stateMu
// and have already verified the window's reducer receipt is still current.
func (h *Handler) recordCodexPromptProbeStateLocked(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
	defaultCodexOwnership bool,
	originWindowID int,
) (before, effective state.EffectiveState) {
	st := h.ensureWindowStateLocked(windowID)
	before = st.Effective
	st.Activity = now
	st.LastSessionTitle = baseTitle
	// The probe only scans interactive Codex windows — same reason
	// probeCodexWindow's maybeNotify passes pipeMode=false.
	st.LastPipeMode = false
	profile := userVars.EffectiveProfile()
	if profile != "" && st.LastAccount == "" {
		st.LastAccount = profile
	}
	targetHarness := harness.NormalizeKnown(userVars.EffectiveHarness())
	if targetHarness == "" && defaultCodexOwnership {
		targetHarness = harness.Codex
	}
	if st.LastHarness == "" && targetHarness != "" {
		st.LastHarness = targetHarness
	}
	st.LastAction = classification.Action
	if classification.Action == "active" {
		st.LastActiveEvent = screenPromptProbeEvent
	}
	st.NotifiedStale = false
	// The reducer owns visible state, so the screen-probe observation must
	// become a fact: fold it onto the probe's own source and publish the
	// reduced outcome. The screen is authoritative for Codex (the probe only
	// fires on states it may overwrite), so the event is marked as a prompt —
	// it recovers a terminal fact the same way the legacy prompt bypass did.
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(protocol.ReducerEvent{
		Source:         protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource},
		Action:         classification.Action,
		Seq:            st.ShadowSeq,
		At:             now.Unix(),
		Prompt:         true,
		OriginWindowID: originWindowID,
	})
	st.Reducer.Compact(now.Unix())
	effective = st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
	h.markWindowDirtyLocked(windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.SessionTitle = baseTitle
	if meta.Durable.Profile == "" && profile != "" {
		meta.Durable.Profile = profile
	}
	if meta.Durable.Harness == "" {
		meta.Durable.Harness = targetHarness
	}
	meta.Durable.PipeMode = false
	meta.Durable.ActivityAt = now.Unix()
	meta.Durable.LastAction = classification.Action
	if classification.Action == "active" {
		meta.Durable.LastActiveEvent = screenPromptProbeEvent
	}
	h.windowMetadata[windowID] = meta

	// The audit row records the ACTUAL reduced icon, not the requested prompt
	// icon, so a prompt masked by a higher-precedence fact never claims an icon
	// the window does not actually show.
	h.appendEventLocked(windowID, codexPromptProbeEventRow(now, currentIconState, classification, effective.Icon, false, originWindowID))
	return before, effective
}

// appendCodexPromptProbeEventLocked records a probe audit row (used for the
// guard-suppressed outcome) without touching reducer state. Callers hold
// stateMu and have already verified the window's reducer receipt is still
// current.
func (h *Handler) appendCodexPromptProbeEventLocked(
	now time.Time,
	windowID int,
	baseTitle string,
	currentIconState string,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
	resultIconState string,
	suppressed bool,
	defaultCodexOwnership bool,
	originWindowID int,
) {
	meta := h.windowMetadata[windowID]
	if baseTitle != "" {
		meta.Durable.SessionTitle = baseTitle
	}
	if profile := userVars.EffectiveProfile(); profile != "" {
		if meta.Durable.Profile == "" {
			meta.Durable.Profile = profile
		}
	}
	if meta.Durable.Harness == "" {
		targetHarness := harness.NormalizeKnown(userVars.EffectiveHarness())
		if targetHarness == "" && defaultCodexOwnership {
			targetHarness = harness.Codex
		}
		if targetHarness != "" {
			meta.Durable.Harness = targetHarness
		}
	}
	h.windowMetadata[windowID] = meta

	h.appendEventLocked(windowID, codexPromptProbeEventRow(now, currentIconState, classification, resultIconState, suppressed, originWindowID))
}

func codexPromptProbeEventRow(
	now time.Time,
	currentIconState string,
	classification CodexPromptClassification,
	resultIconState string,
	suppressed bool,
	originWindowID int,
) protocol.SessionEvent {
	ev := protocol.SessionEvent{
		At:               now.Unix(),
		Event:            screenPromptProbeEvent,
		Action:           classification.Action,
		Source:           screenPromptProbeSource,
		PromptKind:       string(classification.Kind),
		ResultIconState:  resultIconState,
		CurrentIconState: currentIconState,
		OriginWindowID:   originWindowID,
	}
	if suppressed {
		ev.Suppressed = true
		ev.SuppressionReason = suppressionReasonGuardBlocked
	}
	return ev
}
