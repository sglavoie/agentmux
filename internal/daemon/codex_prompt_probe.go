package daemon

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
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
	bindings := h.screenProbeBindings(snapshot, harness.Codex)
	bindings = append(bindings, h.screenProbeTerminalBindings(snapshot, harness.Codex, now.Unix())...)
	for _, binding := range bindings {
		_, tab, target, ok := kitty.FindWindowLocationByID(snapshot.windows, binding.TargetWindowID)
		if !ok {
			continue
		}
		h.probeCodexBinding(now, binding, palette.ParseTitleParts(tab.Title), target.UserVars)
	}

	// Preserve the historical foreground fallback for a tab with no managed
	// target. The executable check is positive Codex producer evidence; encode
	// that as an explicit known-producer direct binding rather than allowing an
	// empty harness to fall through configuration's default-to-Claude path.
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			win, ok := codexPromptProbeTarget(tab)
			if !ok {
				continue
			}
			binding, found := newScreenProbeBinding(
				snapshot,
				conversation.Identity{Harness: harness.Codex},
				win.ID,
				win.ID,
			)
			if !found {
				continue
			}
			binding.legacyDirectManaged = true
			h.probeCodexBinding(now, binding, palette.ParseTitleParts(tab.Title), win.UserVars)
		}
	}
}

func (h *Handler) probeCodexBinding(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	titleParts, userVars, ok := h.codexProbeTargetContext(binding, titleParts, userVars)
	if !ok {
		return
	}
	if titleParts.State == "stop" {
		h.probeCodexClearWindow(now, binding, titleParts, userVars)
		return
	}
	if binding.terminalProducer {
		// A terminal binding exists only to authorize the producer's routed
		// cleared-screen check. It never reopens prompt or idle classification.
		return
	}
	if !codexPromptProbeCanOverwrite(titleParts.State) {
		return
	}
	h.probeCodexWindow(now, binding, titleParts, userVars)
	// Prompt classification wins first. If it found no prompt and left the
	// receipt current, the idle classifier may settle a genuinely finished bolt.
	if titleParts.State == "bolt" {
		h.probeCodexIdleWindow(now, binding, titleParts, userVars)
	}
}

// codexProbeTargetContext derives presentation and eligibility from the managed
// target, not from whichever pane currently renders the shared tab title. This
// preserves an inactive borrowed target's title/profile/owner metadata while
// the binding continues to read the producer pane.
func (h *Handler) codexProbeTargetContext(
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) (palette.TitleParts, kitty.UserVars, bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.screenProbeBindingCurrentLocked(binding) {
		return titleParts, userVars, false
	}
	st, ok := h.windowStateLocked(binding.TargetWindowID)
	if !ok || st == nil {
		return titleParts, userVars, true
	}
	// A legacy direct stopped title may intentionally repair title-only drift
	// while the in-process reducer is empty (including a deferred Stop that has
	// advanced only the receipt). Preserve that snapshot stop candidate; routed
	// bindings always derive their branch from the target reducer.
	if st.Effective.Icon != "" || !binding.legacyDirectManaged || titleParts.State != "stop" {
		titleParts.State = st.Effective.Icon
	}
	titleParts.Muted = titleParts.Muted || st.Muted
	if st.LastSessionTitle != "" {
		titleParts.Base = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		userVars.Profile = st.LastAccount
	}
	if targetHarness := harness.NormalizeKnown(st.LastHarness); targetHarness != "" {
		userVars.Harness = targetHarness
	}
	return titleParts, userVars, true
}

func codexPromptProbeTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	if _, ok := kitty.FindTabTitleOwner(tab); ok {
		return nil, false
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

// codexPromptProbeCmdlineIsCodex accepts only an interactive Codex invocation.
// A non-interactive run such as `codex exec` is a background subprocess in an
// unmanaged pane, not a session the dashboard reports on.
func codexPromptProbeCmdlineIsCodex(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	if !strings.EqualFold(filepath.Base(cmdline[0]), "codex") {
		return false
	}
	return !harness.InvocationIsPipeMode(harness.Codex, cmdline[1:])
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
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
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

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "screen")
	if err != nil {
		slog.Debug("codex prompt probe: get-text failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	classification, ok := ClassifyCodexPromptScreen(screen)
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
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	classification CodexPromptClassification,
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
			classification, IconStatePreserve, true, binding.legacyDirectManaged, binding.SourceWindowID)
		h.stateMu.Unlock()
		return
	}

	iconState := IconState(effectiveAction)
	if iconState == "" {
		iconState = classification.IconState
	}
	before, effective := h.recordCodexPromptProbeStateLocked(now, windowID, titleParts.Base, titleParts.State,
		userVars, classification, binding.legacyDirectManaged, binding.SourceWindowID)
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
func (h *Handler) retractCodexPromptObservation(now time.Time, binding screenProbeBinding) {
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
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	if !st.Reducer.canRetract(source) {
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
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
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

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "screen")
	if err != nil {
		slog.Debug("codex clear probe: get screen failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	if !codexScreenLooksCleared(screen) {
		return
	}

	allText, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "all")
	if err != nil {
		slog.Debug("codex clear probe: get full text failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		return
	}
	if !ClassifyCodexClearScreen(screen, allText) {
		return
	}
	h.applyCodexClearObservation(now, binding, titleParts, userVars)
}

// applyCodexClearObservation folds a classified cleared Codex screen into the
// window atomically under stateMu. The receipt recheck happens before ANY
// mutation — mute cache, pending notification, guard, reset bookkeeping,
// reducer, audit, persistence generation, or renderer queue — so a prompt or
// protected state accepted during either screen read survives unchanged.
// Holding stateMu through the guard update and pending-notification removal is
// safe: stateMu → guard.mu is already established by
// markWindowStateDirtyLocked. Notification disposition observers can perform
// Handler work, so cancellation is collected here but emitted only after both
// stateMu and resetStopCommitMu are released.
//
// This is intentionally a probe-specific application path rather than a call
// to Handle: the ordinary state-update pipeline releases stateMu between its
// legacy bookkeeping and reducer publication, which cannot make a receipt
// recheck atomic with the complete synthetic clear.
func (h *Handler) applyCodexClearObservation(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	windowID := binding.TargetWindowID
	targetHarness := harness.NormalizeKnown(userVars.EffectiveHarness())
	if targetHarness == "" && binding.legacyDirectManaged {
		targetHarness = harness.Codex
	}
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
		targetHarness:    targetHarness,
		targetHarnessSet: true,
	}

	// Serialize the complete clear with the final application of any deferred
	// Stop that has already fired and claimed its timer generation. A callback
	// paused before this boundary remains cancelable below; a callback that
	// already crossed it applies first and advances the receipt before this
	// observation can recheck freshness.
	var pendingDisposition NotificationDisposition
	var pendingCancelled bool
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	defer func() {
		h.stateMu.Unlock()
		h.resetStopCommitMu.Unlock()
		if pendingCancelled {
			h.notifier.emitDisposition(pendingDisposition)
		}
	}()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, func(icon string) bool {
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
	if profile := userVars.EffectiveProfile(); profile != "" {
		st.LastAccount = profile
		h.lastSeenEventAt[strings.ToLower(profile)] = now
		delete(h.notifiedHookSilence, strings.ToLower(profile))
	}
	if st.LastHarness == "" && targetHarness != "" {
		st.LastHarness = targetHarness
	}
	st.LastAction = ""
	st.LastActiveEvent = ""

	// user_clear always removes the legacy guard and any pending permission
	// notification. These calls stay inside the receipt critical section so a
	// later event cannot be cleared after it has advanced the reducer receipt.
	h.guard.TryUpdate(windowID, "user_clear", false)
	if h.notifier != nil {
		pendingDisposition, pendingCancelled = h.notifier.cancelPending(windowID)
	}

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
	eventRow := sessionEventFromRequest(req, req.Action, effective.Icon, titleParts.State)
	eventRow.OriginWindowID = binding.SourceWindowID
	h.appendEventLocked(windowID, eventRow)
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
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
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
		slog.Debug("codex stop probe: discarded stale snapshot", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID)
		return
	}
	st, ok := h.windowStateLocked(targetWindowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(idleSampleRef{windowID: targetWindowID, key: source.Key})
		h.stateMu.Unlock()
		return
	}
	h.stateMu.Unlock()

	screen, err := h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), "screen")
	if err != nil {
		slog.Debug("codex stop probe: get screen failed", "source_window_id", binding.SourceWindowID,
			"target_window_id", targetWindowID, "err", err)
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	if !ClassifyCodexCompletedTurnScreen(screen) && !ClassifyCodexInterruptedTurnScreen(screen) {
		// Still working, a bare composer with no terminal marker, or unrelated
		// output: reset the stabilization streak so a later transient idle frame
		// cannot settle live work.
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:        targetWindowID,
		Source:          source,
		Harness:         harness.Codex,
		CapturedSeq:     binding.TargetReceipt,
		ProbeBinding:    binding,
		At:              now.Unix(),
		RequiredSamples: 1,
		SessionTitle:    titleParts.Base,
		Account:         userVars.EffectiveProfile(),
		PipeMode:        false,
		Muted:           titleParts.Muted || userVars.Mute != "",
		SourceEventID:   fmt.Sprintf("codex-screen-stop:%d:%d:%d", binding.SourceWindowID, targetWindowID, binding.TargetReceipt),
		DiagnosticEvent: screenStopProbeEvent,
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
	defaultCodexOwnership bool,
	originWindowID int,
) (before, effective EffectiveState) {
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
		st.Reducer = NewReducer(h.reducerCfg)
	}
	st.ShadowSeq++
	st.Reducer.Apply(ReducerEvent{
		Source:         SourceID{Kind: SourceProbe, Key: screenPromptProbeSource},
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
) SessionEvent {
	ev := SessionEvent{
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
