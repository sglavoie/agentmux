package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// The screen idle probe recovers a managed tab whose ⚡ is stuck on a finished
// assistant turn — a normal completion or a user interruption — that left no
// Stop hook behind. There is ONE body for it. Claude and Codex differ only in
// the harness adapter that reads the screen, the reducer fact the synthetic
// stop settles under, and whether the pane title buys extra samples; all three
// are data on a screenIdleSpec, not a second copy of the probe.
//
// The ordering below is load-bearing and is preserved exactly from the two
// bodies this replaced:
//
//  1. the reducer receipt is revalidated under stateMu BEFORE the slow
//     off-lock screen read, so a concurrent prompt (or any reducer event)
//     landing after snapshot capture discards the stale route without paying
//     for a Kitty round-trip;
//  2. the read and the classification happen off the lock;
//  3. applyAuthoritativeIdle rechecks the same receipt under stateMu before it
//     mutates anything, so evidence that went stale during the read is dropped.
//
// Every tier requires at least two consecutive samples that agree on the SAME
// normalized screen fingerprint (see screen_idle_policy.go for the one place
// that number lives), so a pane still repainting its elapsed timer cannot
// settle even when the classifier reads it as idle.

// screenIdleSpec is everything one harness contributes to the shared body.
type screenIdleSpec struct {
	// harness is the canonical harness name. It selects the screenstate
	// adapter, keys the sample policy table, and labels the reducer fact.
	harness string
	// sourceKey is the SourceProbe key the synthetic stop settles under —
	// claudeScreenStopProbeSource or screenPromptProbeSource. The two are
	// deliberately distinct so the harnesses' idle evidence never collides.
	sourceKey string
	// event is the diagnostic event name recorded for the settle.
	event string
	// eventIDPrefix opens the deduplicating SourceEventID.
	eventIDPrefix string
	// sampleBump reports how many samples the pane title buys ON TOP of the
	// screen's own tier. Claude's marker rune is the only such evidence today;
	// nil means the harness has none.
	sampleBump func(sourceWindowTitle string) int
	// fields collects the notification context under the same stateMu critical
	// section as the eligibility recheck. It is per-harness because the two
	// probes genuinely disagree about pipe mode and about which mute flags a
	// routed binding may read.
	fields func(st *state.WindowState, binding screenProbeBinding, titleParts palette.TitleParts, userVars kitty.UserVars) screenIdleFields
}

// screenIdleFields is the presentation/notification context read off the
// window under stateMu, before the screen read.
type screenIdleFields struct {
	sessionTitle string
	account      string
	pipeMode     bool
	muted        bool
}

// probeScreenIdle runs the whole shared probe for one binding: the pre-read
// eligibility gate, this tick's cached screen capture, and the classification
// body below. It is the only entry point production uses.
func (h *Handler) probeScreenIdle(
	now time.Time,
	spec screenIdleSpec,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
	sourceWindowTitle string,
	screens *activity.ScreenTextCache,
) {
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: spec.sourceKey}
	targetWindowID := binding.TargetWindowID

	// Revalidate the snapshot receipt before the off-lock read. A window with no
	// reducer facts (an unmanaged/foreground-only bolt) has no stale interactive
	// work for the probe to settle, so bail without a screen read and drop any
	// in-flight stabilization streak.
	h.stateMu.Lock()
	if !h.screenProbeBindingEligibilityCurrentLocked(binding, titleParts.State, activity.ScreenProbeEligibleIcon) {
		// The probe no longer owns this window: the route moved, the receipt
		// advanced, the pane was recreated, or the reduced icon left ⚡. Whatever
		// the classifier was refusing, it is not refusing it for this binding any
		// more — so the decline streak stops being a live counter here rather
		// than being frozen at whatever it reached.
		h.retractScreenDeclinesLocked(targetWindowID)
		h.stateMu.Unlock()
		slog.Debug("screen stop probe: discarded stale snapshot", "harness", spec.harness,
			"source_window_id", binding.SourceWindowID, "target_window_id", targetWindowID)
		return
	}
	st, ok := h.windowStateLocked(targetWindowID)
	if !ok || st.Reducer == nil {
		h.clearIdleSampleLocked(activity.Ref{WindowID: targetWindowID, Key: source.Key})
		h.retractScreenDeclinesLocked(targetWindowID)
		h.stateMu.Unlock()
		return
	}
	fields := spec.fields(st, binding, titleParts, userVars)
	h.stateMu.Unlock()

	screen, err := screens.Screen(binding.SourceWindowID)
	if err != nil {
		slog.Debug("screen stop probe: get screen failed", "harness", spec.harness,
			"source_window_id", binding.SourceWindowID, "target_window_id", targetWindowID, "err", err)
		h.ClearIdleSample(targetWindowID, source)
		return
	}
	h.probeScreenIdleWindow(now, spec, binding, fields, sourceWindowTitle, screen)
}

// probeScreenIdleWindow classifies one already-captured frame and, when it is
// safe, hands the synthetic stop to the race-safe authoritative-idle path under
// the spec's own SourceProbe fact — so ASL leases, subagents, pipe-mode
// children, and higher-precedence attention/error icons all still win at reduce
// time.
func (h *Handler) probeScreenIdleWindow(
	now time.Time,
	spec screenIdleSpec,
	binding screenProbeBinding,
	fields screenIdleFields,
	sourceWindowTitle string,
	screen string,
) {
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: spec.sourceKey}
	targetWindowID := binding.TargetWindowID

	// One classification, read three times: whether the screen may settle at
	// all, how strong the evidence was, and what fingerprint the stabilization
	// gate compares. Splitting these across boolean shims would let the answers
	// be computed from different readings of the same capture.
	obs := activity.ClassifyScreenState(spec.harness, screen)
	if !obs.SettleableTerminal() {
		// Still working, a queued-input composer, a blocking prompt, a
		// context-limited screen, or unrelated output: reset the stabilization
		// streak so a later transient idle frame cannot settle live work. The
		// named vetoes are the diagnostic half of Layer A — without them a
		// declined settle is indistinguishable from a pane nobody looked at.
		// Vetoes render through screenstate.Veto.String, so this names every
		// reason rather than printing a bitset, and the fingerprint is the same
		// value the stabilization gate would have compared: two consecutive
		// declines carrying the SAME fingerprint mean the screen is quiet and
		// Layer A vetoed it, while a changing one means the pane is repainting.
		//
		// RecordScreenProbeDecline performs that same stabilization reset and,
		// in the one lock acquisition, credits the decline against the frame's
		// fingerprint — which is the paragraph above made machine-readable, so a
		// classifier that has stopped matching a redesigned harness UI is a
		// counter someone can read rather than a debug line someone has to be
		// tailing at the right moment.
		// The same three values the log line below prints are also handed to the
		// recorder, which publishes them on the window's export metadata: a
		// classifier that stopped matching a redesigned harness UI is then a
		// counter plus the exact copy it refused, rather than a debug line
		// someone has to be tailing at the right moment.
		decline := h.RecordScreenProbeDecline(targetWindowID, source, activity.ScreenDeclineEvidence{
			Fingerprint: obs.Fingerprint,
			Vetoes:      obs.Vetoes.String(),
			StatusLine:  obs.StatusLine,
			At:          now.Unix(),
		})
		slog.Debug("screen stop probe: not settleable", "harness", spec.harness,
			"target_window_id", targetWindowID,
			"source_window_id", binding.SourceWindowID,
			"state", obs.State.String(), "tier", obs.Tier.String(),
			"vetoes", obs.Vetoes.String(), "fingerprint", obs.Fingerprint,
			"decline_streak", decline.Streak,
			"stable_fingerprint", decline.StableFingerprint,
			"status_line", obs.StatusLine)
		return
	}
	// The tier decides the run length; the pane title only lengthens it. An
	// explicit terminal marker buys no one-sample fast path — a marker regex
	// matches anywhere on the visible screen, including one a previous turn left
	// scrolled above a turn that is running right now, and at one sample the
	// generation below is never compared to anything.
	bump := 0
	if spec.sampleBump != nil {
		bump = spec.sampleBump(sourceWindowTitle)
	}

	settled := h.applyAuthoritativeIdle(AuthoritativeIdle{
		WindowID:     targetWindowID,
		Source:       source,
		Harness:      spec.harness,
		CapturedSeq:  binding.TargetReceipt,
		ProbeBinding: binding,
		At:           now.Unix(),
		// The screen's own normalized fingerprint is the stabilization gate's
		// generation, mirroring the Copilot transcript probe's size generation.
		// Consecutive samples must observe the SAME screen to accumulate, so a
		// pane repainting an elapsed timer between ticks can never reach the
		// required count — no matter what the classification above concluded.
		// This is the harness-independent half of the defense: it survives any
		// change to either harness's status-line copy.
		RequiredSamples: activity.RequiredIdleSamples(spec.harness, obs.Tier, bump),
		// The tier that bought that count, carried purely so the durable event
		// ring can name WHY a settle cost what it did. It is a diagnostic label
		// and is never read back as a decision input.
		EvidenceTier: obs.Tier.String(),
		Generation:   obs.Fingerprint,
		SessionTitle: fields.sessionTitle,
		Account:      fields.account,
		PipeMode:     fields.pipeMode,
		Muted:        fields.muted,
		SourceEventID: fmt.Sprintf("%s:%d:%d:%d", spec.eventIDPrefix,
			binding.SourceWindowID, targetWindowID, binding.TargetReceipt),
		DiagnosticEvent: spec.event,
	})
	if settled {
		// A settle that actually landed is the classifier proving it can still
		// carry this pane all the way to a terminal screen, so whatever declines
		// preceded it were the ordinary cost of a turn rather than evidence of a
		// classifier that stopped matching. Merely reaching this branch is NOT
		// enough: a settleable frame that is still stabilizing has proven
		// nothing yet, and applyAuthoritativeIdle is the only thing that knows
		// which of the two happened. The second stateMu acquisition is safe
		// because the streak is diagnostic — nothing reads it to make a
		// transition decision.
		h.ClearScreenProbeDecline(targetWindowID, source)
	}
}
