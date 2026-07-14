package daemon

import (
	"fmt"
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
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("codex prompt probe: list windows failed", "err", err)
		return
	}
	h.probeCodexScreenPromptsWithSnapshot(now, screenProbeSnapshot{windows: windows, reducerReceipts: reducerReceipts})
	h.renderSnapshot(windows)
}

func (h *Handler) ProbeCodexScreenPromptsWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeCodexScreenPromptsWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeCodexScreenPromptsWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			parts := palette.ParseTitleParts(tab.Title)
			win, ok := codexPromptProbeTarget(tab)
			if !ok {
				continue
			}
			capturedSeq := snapshot.receipt(win.ID)
			if parts.State == "stop" {
				h.probeCodexClearWindow(now, win.ID, parts, win.UserVars, capturedSeq, snapshot.gen)
				continue
			}
			if !codexPromptProbeCanOverwrite(parts.State) {
				continue
			}
			h.probeCodexWindow(now, win.ID, parts, win.UserVars, capturedSeq, snapshot.gen)
			// Idle fallback: only a still-bolt tab can be a stuck active turn.
			// Prompt classification above wins first (it clears the bolt to
			// key/question when a prompt is visible), and both idle classifiers
			// bail on any visible prompt, so this only fires when no prompt was
			// found and the turn genuinely finished — normal completion or a user
			// interruption — with no Stop hook.
			if parts.State == "bolt" {
				h.probeCodexIdleWindow(now, win.ID, parts, win.UserVars, capturedSeq, snapshot.gen)
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
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	// The receipt was captured before the ListWindows call that produced the
	// cached eligibility evidence. Reject it before beginning the screen read if
	// any reducer event landed since; the apply path rechecks after the read.
	if !h.screenProbeEligibilityCurrent(windowID, capturedSeq, capturedSnapshotGen, harness.Codex, func(icon string) bool {
		return codexPromptProbeCanOverwrite(icon)
	}) {
		slog.Debug("codex prompt probe: discarded stale snapshot", "window_id", windowID)
		return
	}

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
		h.retractCodexPromptObservation(now, windowID, capturedSeq, capturedSnapshotGen)
		return
	}
	h.applyCodexPromptObservation(now, windowID, titleParts, userVars, classification, capturedSeq, capturedSnapshotGen)
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
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	h.stateMu.Lock()
	if !h.screenProbeGenerationCurrentLocked(capturedSnapshotGen) ||
		!h.windowReceiptCurrentLocked(windowID, capturedSeq) {
		h.stateMu.Unlock()
		slog.Debug("codex prompt probe: discarded stale observation", "window_id", windowID)
		return
	}
	if classification.Action == "elicitation" {
		h.guard.Clear(windowID)
	}
	allowed, effectiveAction := h.guard.TryUpdate(windowID, classification.Action, true)
	if !allowed {
		h.appendCodexPromptProbeEventLocked(now, windowID, titleParts.Base, titleParts.State, userVars, classification, IconStatePreserve, true)
		h.stateMu.Unlock()
		return
	}

	iconState := IconState(effectiveAction)
	if iconState == "" {
		iconState = classification.IconState
	}
	before, effective := h.recordCodexPromptProbeStateLocked(now, windowID, titleParts.Base, titleParts.State, userVars, classification)
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
func (h *Handler) retractCodexPromptObservation(now time.Time, windowID int, capturedSeq, capturedSnapshotGen uint64) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.screenProbeGenerationCurrentLocked(capturedSnapshotGen) ||
		!h.windowReceiptCurrentLocked(windowID, capturedSeq) {
		return
	}
	st, ok := h.windowStateLocked(windowID)
	if !ok || st.Reducer == nil {
		return
	}
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	if !st.Reducer.canRetract(source) {
		return
	}
	nextSeq := st.ShadowSeq + 1
	if !st.Reducer.Retract(source, nextSeq, now.Unix()) {
		return
	}
	st.ShadowSeq = nextSeq
	h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
	h.markWindowDirtyLocked(windowID)
}

func (h *Handler) probeCodexClearWindow(
	now time.Time,
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	// Bind the stale-stop eligibility from the cached title to its reducer
	// receipt before either slow read. The apply path rechecks after both.
	if !h.screenProbeEligibilityCurrent(windowID, capturedSeq, capturedSnapshotGen, harness.Codex, func(icon string) bool {
		return icon == "" || icon == "stop"
	}) {
		slog.Debug("codex clear probe: discarded stale snapshot", "window_id", windowID)
		return
	}

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
	h.applyCodexClearObservation(now, windowID, titleParts, userVars, capturedSeq, capturedSnapshotGen)
}

// applyCodexClearObservation folds a classified cleared Codex screen into the
// window atomically under stateMu. The receipt recheck happens before ANY
// mutation — mute cache, pending notification, guard, reset bookkeeping,
// reducer, audit, persistence generation, or renderer queue — so a prompt or
// protected state accepted during either screen read survives unchanged.
// Holding stateMu through the guard and notifier calls is safe: stateMu →
// guard.mu is already established by markWindowStateDirtyLocked, and notifier
// cancellation never calls back into Handler state.
//
// This is intentionally a probe-specific application path rather than a call
// to Handle: the ordinary state-update pipeline releases stateMu between its
// legacy bookkeeping and reducer publication, which cannot make a receipt
// recheck atomic with the complete synthetic clear.
func (h *Handler) applyCodexClearObservation(
	now time.Time,
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	req := Request{
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
	}

	// Serialize the complete clear with the final application of any deferred
	// Stop that has already fired and claimed its timer generation. A callback
	// paused before this boundary remains cancelable below; a callback that
	// already crossed it applies first and advances the receipt before this
	// observation can recheck freshness.
	h.resetStopCommitMu.Lock()
	defer h.resetStopCommitMu.Unlock()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.screenProbeEligibilityCurrentLocked(windowID, capturedSeq, capturedSnapshotGen, harness.Codex, func(icon string) bool {
		return icon == "" || icon == "stop"
	}) {
		slog.Debug("codex clear probe: discarded stale observation", "window_id", windowID)
		return
	}
	// The receipt covers deferred reset-sensitive stops as well as applied
	// reducer events. Cancel the pending generation that existed when this clear
	// was captured before publishing the cleared reducer state. Because a newer
	// deferred stop advances ShadowSeq under this same lock, the freshness check
	// above rejects a stale clear before it can delete that newer generation.
	h.cancelPendingResetStopLocked(windowID)

	// Preserve the ordinary reset-flow provenance and final marker state: an
	// explicit reset-scoped user_clear enters and immediately completes the
	// reset flow, leaving PendingReset clear while the audit row retains
	// ResetFlow=true.
	req = h.applyResetFlowLocked(req)
	h.touchWindowMetadataLocked(req)
	st := h.ensureWindowStateLocked(windowID)
	st.Muted = titleParts.Muted || userVars.Mute != ""
	st.CopilotToolPermissionState = copilotToolPermissionNone
	st.SubagentCount = 0
	st.NotifiedStale = false
	st.ContextLimitNotified = false
	st.Activity = now
	st.LastSessionTitle = titleParts.Base
	st.LastPipeMode = false
	st.LastAccount = ""
	if profile := userVars.EffectiveProfile(); profile != "" {
		st.LastAccount = profile
		h.lastSeenEventAt[strings.ToLower(profile)] = now
		delete(h.notifiedHookSilence, strings.ToLower(profile))
	}
	st.LastHarness = harness.Codex
	st.LastAction = ""
	st.LastActiveEvent = ""

	// user_clear always removes the legacy guard and any pending permission
	// notification. These calls stay inside the receipt critical section so a
	// later event cannot be cleared after it has advanced the reducer receipt.
	h.guard.TryUpdate(windowID, "user_clear", false)
	h.cancelPendingNotification(windowID)

	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(ReducerEvent{
		Source: userActionSource(),
		Action: "user_clear",
		Seq:    st.ShadowSeq,
		At:     now.Unix(),
	})
	st.Reducer.Compact(now.Unix())
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
	h.clearIdleSamplesForWindowLocked(windowID)

	meta := h.windowMetadata[windowID]
	meta.Durable.ActivityAt = now.Unix()
	h.windowMetadata[windowID] = meta
	h.appendEventLocked(windowID, sessionEventFromRequest(req, req.Action, effective.Icon, titleParts.State))
	h.syncRendererMetadataLocked(windowID)
	// A real clear must always publish both durable reducer acknowledgement and
	// title cleanup, including when the stopped icon existed only in the Kitty
	// snapshot and the in-process reducer was previously empty.
	h.markWindowDirtyLocked(windowID)
}

// probeCodexIdleWindow recovers a Codex tab whose bolt icon is stuck on a
// finished assistant turn — a normal completion or a user interruption — that
// left no Stop hook behind. Codex is authoritative for its own screen, so the
// daemon reads the completed/interrupted UI and settles the stale interactive
// bolt through the race-safe authoritative-idle path.
//
// The reducer receipt sequence is captured before the cached Kitty snapshot and
// revalidated under stateMu BEFORE the slow screen read: applyAuthoritativeIdle
// rechecks it after the read, so any reducer event (a fresh prompt, activity, or
// a native terminal hook) landing after capture discards the observation. The
// synthetic stop is applied under the SourceProbe `codex_screen`
// fact, never the legacy scalar machine: a live ASL lease, a higher-precedence
// attention/error icon, a newer interactive bolt, or a pipe-mode/subagent fact
// all still win at reduce time. The caller only invokes this for a `bolt` tab,
// so protected on-screen icons never reach here in the first place.
func (h *Handler) probeCodexIdleWindow(
	now time.Time,
	windowID int,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	capturedSeq uint64,
	capturedSnapshotGen uint64,
) {
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}

	// Revalidate the snapshot receipt before the off-lock GetText. A window with no
	// reducer facts (an unmanaged/foreground-only bolt) has no stale interactive
	// work for the probe to settle, so bail without a screen read and drop any
	// in-flight stabilization streak.
	h.stateMu.Lock()
	if !h.screenProbeEligibilityCurrentLocked(windowID, capturedSeq, capturedSnapshotGen, harness.Codex, func(icon string) bool {
		return icon == "bolt"
	}) {
		h.stateMu.Unlock()
		slog.Debug("codex stop probe: discarded stale snapshot", "window_id", windowID)
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
		slog.Debug("codex stop probe: get screen failed", "window_id", windowID, "err", err)
		h.ClearIdleSample(windowID, source)
		return
	}
	if !ClassifyCodexCompletedTurnScreen(screen) && !ClassifyCodexInterruptedTurnScreen(screen) {
		// Still working, a bare composer with no terminal marker, or unrelated
		// output: reset the stabilization streak so a later transient idle frame
		// cannot settle live work.
		h.ClearIdleSample(windowID, source)
		return
	}
	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:            windowID,
		Source:              source,
		Harness:             harness.Codex,
		CapturedSeq:         capturedSeq,
		CapturedSnapshotGen: capturedSnapshotGen,
		At:                  now.Unix(),
		RequiredSamples:     1,
		SessionTitle:        titleParts.Base,
		Account:             userVars.EffectiveProfile(),
		PipeMode:            false,
		Muted:               titleParts.Muted || userVars.Mute != "",
		SourceEventID:       fmt.Sprintf("codex-screen-stop:%d:%d", windowID, capturedSeq),
		DiagnosticEvent:     screenStopProbeEvent,
	})
}

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
) (before, effective EffectiveState) {
	st := h.ensureWindowStateLocked(windowID)
	before = st.Effective
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
	effective = st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(windowID, st, effective)
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

	// The audit row records the ACTUAL reduced icon, not the requested prompt
	// icon, so a prompt masked by a higher-precedence fact never claims an icon
	// the window does not actually show.
	h.appendEventLocked(windowID, codexPromptProbeEventRow(now, currentIconState, classification, effective.Icon, false))
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
) {
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
