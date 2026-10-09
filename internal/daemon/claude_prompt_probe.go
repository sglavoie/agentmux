package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	// screenActiveProbeEvent records the positive half of the Claude pane-title
	// probe: a current busy marker restored an otherwise empty managed tab to
	// active. The reducer fact shares claudeScreenStopProbeSource with the idle
	// half so a later non-busy idle screen can replace it with stop cleanly.
	screenActiveProbeEvent = "ScreenActiveProbe"

	// claudeScreenStopProbeSource keys the SourceProbe fact the Claude idle
	// stop and busy-title active recovery settle under. It is deliberately
	// distinct from the Codex probe's
	// codex_screen key so the two harnesses' idle evidence never collide, and
	// distinct from the context-limit probe's own SourceProbe fact
	// (claudeContextLimitFactSource) so a stuck-turn "stop" and a context-limit
	// "error" never share one reducer fact.
	claudeScreenStopProbeSource = "claude_screen"
)

// claudeScreenIdleSpec binds Claude into the shared screen idle probe body. The
// sample counts it looks up live in screen_idle_policy.go.
var claudeScreenIdleSpec = screenIdleSpec{
	harness:       harness.Claude,
	sourceKey:     claudeScreenStopProbeSource,
	event:         screenStopProbeEvent,
	eventIDPrefix: "claude-screen-stop",
	sampleBump:    claudeIdleProbeTitleBump,
	fields:        claudeScreenIdleFields,
}

// claudeIdleProbeTitleBump is the pane-title half of Claude's sample policy: a
// title still carrying a work-marker rune lengthens the run of agreeing samples
// the screen's own tier already bought.
func claudeIdleProbeTitleBump(sourceWindowTitle string) int {
	if activity.ClaudeTitleHasMarker(sourceWindowTitle) {
		return activity.ClaudeIdleProbeMarkerSampleBump
	}
	return 0
}

// claudeScreenIdleFields reads the notification context off the target window
// under stateMu. Claude carries pipe mode (a `claude -p` child's bolt is the
// probe's to describe, not to invent) and only folds the tab title's mute flag
// in for a direct session, where the tab title really does belong to the pane
// being read.
func claudeScreenIdleFields(
	st *state.WindowState,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) screenIdleFields {
	fields := screenIdleFields{
		sessionTitle: titleParts.Base,
		account:      userVars.EffectiveProfile(),
		pipeMode:     st.LastPipeMode,
		muted:        userVars.Mute != "",
	}
	if binding.SourceWindowID == binding.TargetWindowID {
		fields.muted = fields.muted || titleParts.Muted
	}
	if st.LastSessionTitle != "" {
		fields.sessionTitle = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		fields.account = st.LastAccount
	}
	fields.muted = fields.muted || st.Muted
	return fields
}

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
	h.probeClaudeScreenStopWithSnapshot(now, probes.NewSnapshot(windows, reducerReceipts),
		h.screenTextCacheFor())
	h.renderSnapshot(windows)
}

// probeClaudeScreenStopWithSnapshot walks the Claude routes in one snapshot and
// hands each to the shared screen idle probe body (screen_idle_probe.go). The
// only Claude-specific step left here is the busy-title recovery below.
//
// Direct sessions retain the managed title-owner selection; a borrowed session
// follows its live reducer binding to read the unmanaged producer pane and apply
// the result to the managed target. A busy Claude pane title can also restore an
// empty target to ⚡. Otherwise, only a target whose current reduced icon is a
// stuck ⚡ is considered: any other visible state is either already terminal or a
// higher-precedence icon the idle path must never downgrade.
func (h *Handler) probeClaudeScreenStopWithSnapshot(
	now time.Time,
	snapshot probes.Snapshot,
	screens *activity.ScreenTextCache,
) {
	for _, binding := range h.screenProbeBindings(snapshot, harness.Claude) {
		_, tab, target, ok := kitty.FindWindowLocationByID(snapshot.Windows(), binding.TargetWindowID)
		if !ok {
			continue
		}
		_, _, source, ok := kitty.FindWindowLocationByID(snapshot.Windows(), binding.SourceWindowID)
		if !ok {
			continue
		}
		titleParts := palette.ParseTitleParts(tab.Title)

		// A description title recovers a hook-missed empty tab to ⚡ before the
		// bolt-only idle eligibility gate inside the shared body. The helper
		// revalidates the exact snapshot binding and only mutates an empty reduced
		// icon; terminal and higher-precedence states are never replaced.
		//
		// It deliberately does not veto the screen read that follows. Claude keeps
		// a description title long after its turn ends, so a title that stopped the
		// probe here would leave an idle pane permanently unable to settle. A
		// committed promotion bumps ShadowSeq and so fails the eligibility check on
		// this same tick; every other tick reads the screen, which is authoritative
		// for whether a turn is actually running.
		if activity.ClaudeWindowTitleHasDescriptionMarker(source.Title) {
			h.recoverClaudeBusyTitle(now, binding, titleParts, target.UserVars, source.Title)
		}

		// Labelling an already-blocked pane is disjoint from settling a stuck ⚡:
		// this reads only 🔑/❓ windows and the idle probe below reads only ⚡
		// ones, so no pane is classified twice and the per-tick screen cache is
		// never asked for a capture both would want.
		h.stampClaudePromptKind(binding, screens)

		h.probeScreenIdle(now, claudeScreenIdleSpec, binding, titleParts, target.UserVars, source.Title, screens)
	}
}

// stampClaudePromptKind labels a Claude window that is ALREADY blocked with
// which prompt is on its screen, feeding state.WindowState.PromptKind — the
// same field the Codex prompt probe writes (see
// recordCodexPromptProbeStateLocked).
//
// It is a pure labeller and that is the whole design. It applies no reducer
// event, publishes no state, and raises no icon: the Claude hook path already
// painted 🔑/❓, and nothing here may change what a pane looks like. Compare the
// Codex probe, which classifies in order to RAISE the state and therefore folds
// its result into the reducer.
//
// The blocking-icon gate is also what bounds the cost. It runs before the
// screen read, so an ordinary working or resting pane pays nothing, and a pane
// that IS blocked is a pane whose user is being waited on — one cached Kitty
// capture per probe tick is not a cost anyone is counting. The gate is
// promptKindBlockingIcon, the same predicate stampPromptKindLocked clears
// against, so this can never write a kind that the next publish would strip.
//
// Nothing is marked dirty: PromptKind is deliberately not persisted (see the
// field) and does not yet reach the wire, so there is no durable or rendered
// consequence to flush. Whatever projects it onto a row must revisit that.
func (h *Handler) stampClaudePromptKind(binding probes.Binding, screens *activity.ScreenTextCache) {
	targetWindowID := binding.TargetWindowID

	h.stateMu.Lock()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, "", promptKindBlockingIcon) {
		h.stateMu.Unlock()
		return
	}
	h.stateMu.Unlock()

	screen, err := probeScreenText(screens, binding)
	if err != nil {
		slog.Debug("claude prompt kind: get screen failed",
			"source_window_id", binding.SourceWindowID, "target_window_id", targetWindowID, "err", err)
		return
	}
	// A screen that names no kind leaves the existing one alone rather than
	// clearing it. An override with a flat blocking_prompt answers false for
	// every screen, and so does a Claude build that reworded its prompt: in both
	// cases "no label available" is the fact, and blanking a good label the last
	// tick read would be a worse answer than keeping it. Leaving the pane's
	// blocking icon is what clears the kind, and stampPromptKindLocked owns that.
	kind, ok := activity.ClassifyClaudePromptScreen(screen)
	if !ok {
		return
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	// Revalidated after the off-lock read, exactly as the idle probe revalidates
	// before it mutates: the prompt may have been answered while the capture was
	// in flight, and a kind written onto a window that has moved on is the stale
	// reason this whole field is arranged to prevent.
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, "", promptKindBlockingIcon) {
		return
	}
	st, ok := h.windowStateLocked(targetWindowID)
	if !ok || st == nil {
		return
	}
	st.PromptKind = string(kind)
}

// recoverClaudeBusyTitle promotes an empty managed Claude target to active from
// the pane title's busy marker. It deliberately performs no screen read: the
// marker is already part of the causally-bound Kitty snapshot, and the caller
// scrapes the screen on the same tick. Existing icons are left untouched,
// including stop and every attention/error state. Promotion is one-shot per
// marker body (see WindowState.promotedBusyBody): a title that outlives the turn
// that produced it recovers ⚡ once, not on every tick after the watchdog expires
// the promoted fact. An operator clear of a settled icon holds promotion
// outright until the next real hook/ASL event (see WindowState.busyPromotionHeld), so a cleared icon
// stays cleared under a title that has not changed since.
func (h *Handler) recoverClaudeBusyTitle(
	now time.Time,
	binding probes.Binding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	sourceWindowTitle string,
) {
	targetWindowID := binding.TargetWindowID
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: claudeScreenStopProbeSource}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	if !h.screenProbeBindingCurrentLocked(binding) {
		slog.Debug("claude active probe: discarded stale snapshot",
			"source_window_id", binding.SourceWindowID, "target_window_id", targetWindowID)
		return
	}

	currentIcon := titleParts.State
	st, ok := h.windowStateLocked(targetWindowID)
	if ok && st != nil {
		currentIcon = st.Effective.Icon
		// Reset flows own their transient empty state until their cleanup event
		// commits; the next snapshot tick may recover normally once the flow ends.
		if st.PendingReset != state.ResetFlowNone {
			return
		}
		if st.BusyPromotionHeld() {
			return
		}
	}
	if currentIcon != "" {
		return
	}
	// The marker rune is an animation frame frozen at an arbitrary value, so the
	// one-shot is keyed on the body alone: a raw-title key re-arms itself whenever
	// the frozen frame differs over the same description.
	promotedBody, _ := activity.ClaudeTitleMarkerBody(sourceWindowTitle)
	if ok && st != nil && st.PromotedBusyBody() == promotedBody {
		return
	}

	st = h.ensureWindowStateLocked(targetWindowID)
	before := st.Effective
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}

	nextSeq := st.ShadowSeq + 1
	decision := st.Reducer.Apply(protocol.ReducerEvent{
		Source:         source,
		Action:         "active",
		Seq:            nextSeq,
		At:             now.Unix(),
		Prompt:         true,
		OriginWindowID: binding.SourceWindowID,
	})
	if decision != protocol.DecisionApplied {
		return
	}
	st.ShadowSeq = nextSeq

	// Only a committed promotion invalidates accumulated idle evidence. The
	// caller's screen read now depends on that streak surviving every tick where
	// the title merely repeats itself.
	h.clearIdleSampleLocked(activity.Ref{WindowID: targetWindowID, Key: source.Key})

	st.Activity = now
	if titleParts.Base != "" {
		st.LastSessionTitle = titleParts.Base
	}
	if profile := userVars.EffectiveProfile(); profile != "" && st.LastAccount == "" {
		st.LastAccount = profile
	}
	if st.LastHarness == "" {
		st.LastHarness = harness.Claude
	}
	st.LastAction = "active"
	st.LastActiveEvent = screenActiveProbeEvent
	st.NotifiedStale = false
	st.SetPromotedBusyBody(promotedBody)

	st.Reducer.Compact(now.Unix())
	effective := st.Reducer.Reduce(now.Unix())
	h.publishEffectiveLocked(targetWindowID, st, effective)

	meta := h.windowMetadata[targetWindowID]
	if titleParts.Base != "" {
		meta.Durable.SessionTitle = titleParts.Base
	}
	if profile := userVars.EffectiveProfile(); profile != "" && meta.Durable.Profile == "" {
		meta.Durable.Profile = profile
	}
	if meta.Durable.Harness == "" {
		meta.Durable.Harness = harness.Claude
	}
	meta.Durable.ActivityAt = now.Unix()
	meta.Durable.LastAction = "active"
	meta.Durable.LastActiveEvent = screenActiveProbeEvent
	h.windowMetadata[targetWindowID] = meta

	resultAction := effective.Icon
	if resultAction == "" {
		resultAction = "idle"
	}
	h.appendWatchdogEventLocked(
		targetWindowID,
		binding.SourceWindowID,
		now,
		screenActiveProbeEvent,
		resultAction,
		string(source.Kind)+":"+source.Key,
		fmt.Sprintf("Claude busy pane title restored visible state %q→%q", before.Icon, effective.Icon),
	)
	h.syncRendererMetadataLocked(targetWindowID)
	if effective != before {
		h.markWindowDirtyLocked(targetWindowID)
	} else {
		h.markWindowActivityDirtyLocked(targetWindowID)
	}
}
