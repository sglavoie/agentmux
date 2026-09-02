package cmd

// Shared invariant evaluation over the daemon's current export snapshot.
// `amx doctor` evaluates one snapshot; `amx daemon audit` re-evaluates every
// sample interval. Severities: a "failure" is an observed correctness
// violation (strict doctor and audit exit nonzero on any), a "warning" is
// suspicious but not provably wrong, an "info" is expected historical state
// (closed tombstones, acknowledged facts) surfaced for context only.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

const (
	severityFailure = "failure"
	severityWarning = "warning"
	severityInfo    = "info"
)

// Invariant check names, stable identifiers for JSON consumers.
const (
	checkASLActiveState           = "asl_active_state"
	checkSubagentVisibility       = "subagent_visibility"
	checkActiveAfterTerminal      = "active_after_terminal_churn"
	checkShadowCurrentMismatch    = "shadow_current_mismatch"
	checkQueueSaturation          = "queue_saturation"
	checkRenderGenerationDrift    = "render_generation_drift"
	checkRenderCausalBound        = "render_causal_bound"
	checkBuildMismatch            = "build_mismatch"
	checkHarnessDrift             = "harness_drift"
	checkClosedTombstone          = "closed_tombstone"
	checkAcknowledgedState        = "acknowledged_state"
	checkOwnerMismatchPrompt      = "owner_mismatch_prompt"
	checkOwnerMismatchBurst       = "owner_mismatch_burst"
	checkUnknownProducerFact      = "unknown_producer_fact"
	checkAcknowledgedPressure     = "acknowledged_fact_pressure"
	checkMalformedProcessIdentity = "malformed_process_identity"
	checkStopHoldStuck            = "stop_hold_stuck"
	checkScreenPersistentDecline  = "screenstate_persistent_decline"
)

// screenDeclinePersistentStreak is how many consecutive refusals of the SAME
// unchanging screen turn a screen-probe decline streak into a health failure.
// The probe ticks every rendering.Interval (2s), so this is two minutes.
//
// The conjunction is what makes it safe: a pane doing real work repaints its
// elapsed timer, which changes the fingerprint and restarts the streak at one,
// so no amount of legitimate activity can reach this. A streak this long with a
// stable fingerprint means the screen went quiet and the classifier vetoed it
// anyway while the tab kept showing ⚡ — the exact signature of a harness UI
// change the compiled-in patterns no longer match.
const screenDeclinePersistentStreak = 60

const (
	ownerMismatchBurstCount          = 3
	acknowledgedPressureWarningCount = 49
	acknowledgedPressureFailureCount = 64
)

// activeAfterTerminalGracePeriod is the window over which repeated
// active_after_terminal suppressions on one window count as churn: a stream of
// trailing events still being suppressed long after a terminal state suggests
// the source is genuinely active and the terminal fact is wrong.
const activeAfterTerminalGracePeriod = 15 * time.Minute

// activeAfterTerminalFailureCount is how many suppressions within the grace
// period turn churn into a health failure.
const activeAfterTerminalFailureCount = 3

// stopHoldStuckTimeoutMultiple is how many configured stop_confirm timeouts a
// terminal stop hold may stay armed before it is treated as stuck. The hold's
// own hard deadline is one timeout after it armed, so twice that is already
// well past every release path: the window is showing an active icon for a 🛑
// that will never paint, and the withheld stop is invisible everywhere else.
const stopHoldStuckTimeoutMultiple = 2

// renderGenerationDriftMaxSeconds is two render intervals (the daemon renders
// every 2s): a window still dirty past this age means the render pipeline is
// not keeping up or a write is stuck.
const renderGenerationDriftMaxSeconds = 4

// doctorFinding is one structured diagnostic outcome.
type doctorFinding struct {
	Severity string `json:"severity"`
	Check    string `json:"check"`
	WindowID int    `json:"window_id,omitempty"`
	Message  string `json:"message"`
}

// evaluateDaemonInvariants applies the correctness invariants to one export
// snapshot. liveByID may be nil when Kitty was unreachable (the live-join
// checks are skipped). Tombstoned windows are reported as informational and
// exempt from failure checks: their live state was garbage-collected and the
// retained metadata is post-mortem evidence, not a running session.
func evaluateDaemonInvariants(snapshot protocol.EventsExport, health daemon.Health, cliBuild buildinfo.Info, liveByID map[int]protocol.WindowLiveMetadata, now time.Time) []doctorFinding {
	findings := make([]doctorFinding, 0)
	add := func(severity, check string, windowID int, format string, args ...any) {
		findings = append(findings, doctorFinding{
			Severity: severity,
			Check:    check,
			WindowID: windowID,
			Message:  fmt.Sprintf(format, args...),
		})
	}

	ids := make([]int, 0, len(snapshot.WindowMetadata)+len(snapshot.EventsByWindow))
	seenIDs := make(map[int]struct{}, cap(ids))
	for id := range snapshot.WindowMetadata {
		seenIDs[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range snapshot.EventsByWindow {
		if _, ok := seenIDs[id]; ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		meta := snapshot.WindowMetadata[id]
		if meta.Watchdog.ClosedAt != 0 {
			add(severityInfo, checkClosedTombstone, id,
				"window closed at %s; metadata retained for post-mortem export only",
				time.Unix(meta.Watchdog.ClosedAt, 0).Format(time.RFC3339))
			continue
		}

		desired := meta.Renderer.DesiredIcon
		facts := meta.Shadow.Facts

		// owner_mismatch means an event-producing pane could not be safely
		// associated with a managed target. A direct prompt is authoritative
		// focus evidence and must always route, so even one recent prompt mismatch
		// is a correctness failure. Non-prompt traffic can legitimately race with
		// focus changes, but a burst on one pane is suspicious.
		mismatchCutoff := now.Add(-doctorSuppressionWindow).Unix()
		promptMismatches := 0
		nonPromptMismatches := 0
		for _, event := range snapshot.EventsByWindow[id] {
			if !event.Suppressed || event.SuppressionReason != "owner_mismatch" || event.At < mismatchCutoff {
				continue
			}
			if isPromptSessionEvent(event) {
				promptMismatches++
			} else {
				nonPromptMismatches++
			}
		}
		if promptMismatches > 0 {
			add(severityFailure, checkOwnerMismatchPrompt, id,
				"%d prompt event(s) suppressed as owner_mismatch within %s",
				promptMismatches, doctorSuppressionWindow)
		}
		if nonPromptMismatches >= ownerMismatchBurstCount {
			add(severityWarning, checkOwnerMismatchBurst, id,
				"%d non-prompt event(s) suppressed as owner_mismatch within %s",
				nonPromptMismatches, doctorSuppressionWindow)
		}

		// Active ASL work (a live turn/verification lease) must never render as
		// stopped or empty: the loop is executing right now.
		for _, fact := range facts {
			if fact.Kind != protocol.SourceASL || fact.Acked || !fact.LeaseLive {
				continue
			}
			if fact.Phase != state.ASLPhaseTurn && fact.Phase != state.ASLPhaseVerification {
				continue
			}
			if desired == "" || desired == "stop" {
				add(severityFailure, checkASLActiveState, id,
					"ASL run %q is in phase %q but the window shows icon %q",
					fact.RunID, fact.Phase, desired)
			}
			break
		}

		// A live subagent contribution must surface 🤖 or a higher-priority
		// attention icon; bolt/stop/empty means the agent's work is invisible.
		liveAgents := false
		for _, fact := range facts {
			if fact.Acked {
				continue
			}
			if fact.Kind == protocol.SourceSubagent && !fact.Terminated && fact.Icon == "subagent" {
				liveAgents = true
				break
			}
			if fact.Kind == protocol.SourceLegacy && fact.Agents > 0 {
				liveAgents = true
				break
			}
		}
		if liveAgents && protocol.IconPrecedence(desired) < protocol.IconPrecedence("subagent") {
			add(severityFailure, checkSubagentVisibility, id,
				"live subagent facts present but the window shows icon %q (below 🤖 precedence)", desired)
		}

		// An unacknowledged window-scoped legacy fact has no known producer
		// harness. If it contributes visible state or a lease, attribution and
		// route continuity are degraded even when the reduced icon is plausible.
		unknownProducerFacts := 0
		for _, fact := range facts {
			if isUnknownProducerFact(fact) {
				unknownProducerFacts++
			}
		}
		if unknownProducerFacts > 0 {
			add(severityWarning, checkUnknownProducerFact, id,
				"%d unacknowledged legacy/window fact(s) contribute an icon or live lease",
				unknownProducerFacts)
		}

		// Process identity is a zero-or-complete persisted triple bound to an
		// ordinary compatible source. This check inspects exported facts only;
		// debug status/doctor must not perform another live process scan.
		malformedProcessFacts := 0
		for _, fact := range facts {
			if fact.HasMalformedHarnessProcessIdentity() {
				malformedProcessFacts++
			}
		}
		if malformedProcessFacts > 0 {
			add(severityFailure, checkMalformedProcessIdentity, id,
				"%d source fact(s) contain malformed or source-incompatible harness process identity",
				malformedProcessFacts)
		}

		// Repeated active_after_terminal suppressions inside the grace period:
		// the source keeps producing activity the terminal fact keeps eating.
		cutoff := now.Add(-activeAfterTerminalGracePeriod).Unix()
		suppressed := 0
		for _, event := range snapshot.EventsByWindow[id] {
			if event.Suppressed && event.SuppressionReason == "active_after_terminal" && event.At >= cutoff {
				suppressed++
			}
		}
		if suppressed >= activeAfterTerminalFailureCount {
			add(severityFailure, checkActiveAfterTerminal, id,
				"%d active_after_terminal suppressions within %s", suppressed, activeAfterTerminalGracePeriod)
		}

		// A terminal stop hold withholds a received stop from the reducer while
		// the window keeps showing its active icon, so it is deliberately absent
		// from every other health surface. Its own deadline bounds it; one still
		// armed at twice the configured timeout has outlived every release path
		// and the 🛑 it is holding will never arrive.
		if hold := meta.Shadow.PendingTerminalStop; hold != nil && hold.Class == protocol.StopHoldClassTerminal {
			timeout := hold.Deadline - hold.ArmedAt
			armedFor := now.Unix() - hold.ArmedAt
			if timeout > 0 && armedFor > stopHoldStuckTimeoutMultiple*timeout {
				add(severityWarning, checkStopHoldStuck, id,
					"terminal stop held for %ds, past %dx the %ds stop_confirm timeout; the window still shows icon %q",
					armedFor, stopHoldStuckTimeoutMultiple, timeout, desired)
			}
		}

		// A screen classifier that has stopped matching its harness's UI leaves
		// the tab holding ⚡ forever and reports nothing: the patterns simply
		// never fire. The decline counter is the daemon watching itself refuse,
		// and only the streak-plus-stable-fingerprint conjunction is evidence —
		// see screenDeclinePersistentStreak. The vetoes and status line are
		// carried into the message because they are what an operator diffs
		// against the shipped patterns to find the copy that changed.
		//
		// The finding is a statement about the window's CURRENT health, so it is
		// gated on the window still being the stuck ⚡ the streak describes. The
		// daemon retracts a streak itself when the window moves off ⚡, but this
		// gate is not merely a second copy of that: a doctor run reads one export
		// snapshot, which may have been captured before the retraction or emitted
		// by a daemon predating it, and the retained evidence must not fail
		// strict doctor for a window that has already recovered.
		stuckOnScreen := screenDeclineStillCurrent(meta, liveByID, id)
		for _, decline := range meta.Watchdog.ScreenDeclines {
			if !stuckOnScreen || !decline.StableFingerprint || decline.Streak < screenDeclinePersistentStreak {
				continue
			}
			statusLine := decline.StatusLine
			if strings.TrimSpace(statusLine) == "" {
				statusLine = "(none)"
			}
			vetoes := decline.Vetoes
			if strings.TrimSpace(vetoes) == "" {
				vetoes = "(none)"
			}
			add(severityFailure, checkScreenPersistentDecline, id,
				"screen probe %q declined the same unchanging screen %d consecutive times (threshold %d); vetoes=%s status line=%q — the classifier may no longer match this harness's UI",
				decline.Source, decline.Streak, screenDeclinePersistentStreak, vetoes, statusLine)
		}

		// Post-cutover the reducer's effective state IS the visible state; a
		// desired icon that disagrees with the effective state means some path
		// still writes the scalar directly.
		hasShadow := len(facts) > 0 || meta.Shadow.EffectiveGen > 0
		if hasShadow && meta.Renderer.StateSet && desired != meta.Shadow.EffectiveIcon {
			add(severityFailure, checkShadowCurrentMismatch, id,
				"reducer effective icon %q but renderer desired icon %q", meta.Shadow.EffectiveIcon, desired)
		}
		if hasShadow && meta.Renderer.LoopingSet && hasASLFact(facts) &&
			meta.Renderer.Looping != meta.Shadow.EffectiveLooping {
			add(severityFailure, checkShadowCurrentMismatch, id,
				"reducer effective looping %t but renderer looping %t", meta.Shadow.EffectiveLooping, meta.Renderer.Looping)
		}

		// Render-generation drift: dirty for longer than two render intervals.
		if meta.Renderer.DirtyAgeSeconds > renderGenerationDriftMaxSeconds {
			add(severityFailure, checkRenderGenerationDrift, id,
				"window dirty for %ds (max %ds); queued gen %d, rendered gen %d, failed gen %d",
				meta.Renderer.DirtyAgeSeconds, renderGenerationDriftMaxSeconds,
				meta.Renderer.QueuedGen, meta.Renderer.RenderedGen, meta.Renderer.FailedGen)
		}

		for _, generation := range meta.Renderer.Generations {
			if len(generation.TraceIDs) > protocol.MaxRenderCausalIDs ||
				len(generation.RequestIDs) > protocol.MaxRenderCausalIDs ||
				len(generation.SourceEventIDs) > protocol.MaxRenderCausalIDs {
				add(severityFailure, checkRenderCausalBound, id,
					"render generation %d exceeds the %d-ID causal bound (traces=%d requests=%d source_events=%d)",
					generation.Generation, protocol.MaxRenderCausalIDs, len(generation.TraceIDs),
					len(generation.RequestIDs), len(generation.SourceEventIDs))
			}
		}

		// Acknowledged nonterminal facts are bounded replay cutoffs. Approaching
		// the cap is worth surfacing; exceeding it means compaction failed. Live
		// ASL leases stay outside the count because they still drive looping.
		acknowledgedPressure := 0
		for _, fact := range facts {
			if isEvictableAcknowledgedFact(fact) {
				acknowledgedPressure++
			}
		}
		if acknowledgedPressure >= acknowledgedPressureWarningCount {
			severity := severityWarning
			if acknowledgedPressure > acknowledgedPressureFailureCount {
				severity = severityFailure
			}
			add(severity, checkAcknowledgedPressure, id,
				"%d evictable acknowledged facts retained (warning at %d; maximum %d)",
				acknowledgedPressure, acknowledgedPressureWarningCount, acknowledgedPressureFailureCount)
		}

		// Ordinary acknowledged historical state remains informational.
		if len(facts) > 0 && allFactsAcked(facts) {
			add(severityInfo, checkAcknowledgedState, id,
				"all %d source facts are acknowledged; retained as history", len(facts))
		}

		// Foreground harness (live Kitty user vars) vs stored harness drift.
		if live, ok := liveByID[id]; ok && live.Found &&
			live.Harness != "" && meta.Durable.Harness != "" &&
			!strings.EqualFold(live.Harness, meta.Durable.Harness) {
			add(severityWarning, checkHarnessDrift, id,
				"foreground harness %q differs from stored harness %q", live.Harness, meta.Durable.Harness)
		}
	}

	// Queue saturation: the ingress/dedupe queue-full counter is cumulative,
	// so any nonzero value means overload degradation has occurred.
	if health.Alive && health.Metrics != nil && health.Metrics.QueueFull > 0 {
		add(severityFailure, checkQueueSaturation, 0,
			"queue-full degradations observed: %d", health.Metrics.QueueFull)
	}

	// CLI/daemon build identity. A live daemon that reports no build identity
	// predates this diagnostics surface — definitionally not the artifact the
	// CLI was built from.
	if health.Alive {
		switch {
		case health.Build == nil:
			add(severityFailure, checkBuildMismatch, 0,
				"daemon reports no build identity (binary predates build stamping); restart from the installed binary")
		case !cliBuild.SameArtifact(*health.Build):
			add(severityFailure, checkBuildMismatch, 0,
				"CLI is %s but daemon runs %s", cliBuild.String(), health.Build.String())
		}
	}

	return findings
}

// screenDeclineStillCurrent reports whether a window's retained screen-probe
// decline evidence still describes its health RIGHT NOW, which is the only
// thing screenstate_persistent_decline claims. The streak means "the classifier
// is refusing this pane's screen and the tab is stuck on ⚡"; the moment either
// half stops being true the failure has to retract, even though the evidence
// that produced it is legitimately retained.
//
// Two conditions, in the order they can go wrong:
//
//  1. The window is still live. A closed window is already exempt one level up
//     as a tombstone, but a window Kitty no longer reports at all is equally not
//     a pane anyone is looking at. When Kitty was unreachable the live join never
//     ran (liveByID is nil), and suppressing the finding for the whole report
//     would hide the fault it exists for, so the export's own view stands alone.
//
//  2. The screen probe is still eligible for it. The probe classifies a window
//     only while its reduced icon is the ⚡ activity.ScreenProbeEligibleIcon names,
//     so a native stop, a blocking prompt, an error, or a clear/reset recovers
//     the window by taking it out of the probe's reach entirely — and the probe
//     then has no further tick in which to notice. Every icon the export reports
//     for the window is checked rather than one chosen field: the renderer's
//     intent, the reducer's authoritative answer, and the live tab title are
//     three views of the same window, and a window that is genuinely still stuck
//     shows ⚡ in all of them.
func screenDeclineStillCurrent(meta protocol.WindowDebugMetadata, liveByID map[int]protocol.WindowLiveMetadata, id int) bool {
	live, joined := protocol.WindowLiveMetadata{}, liveByID != nil
	if joined {
		if live = liveByID[id]; !live.Found {
			return false
		}
	}
	icons := make([]string, 0, 3)
	if meta.Renderer.StateSet || meta.Renderer.DesiredIcon != "" {
		icons = append(icons, meta.Renderer.DesiredIcon)
	}
	if meta.Shadow.EffectiveGen > 0 {
		icons = append(icons, meta.Shadow.EffectiveIcon)
	}
	// A found live window always contributes its icon, including the empty one.
	// Found already separates "Kitty has no such window" from "the tab is
	// there", so an empty state on a tab that exists is not a missing reading —
	// it is the clear/reset itself, and it is the only reading of the three that
	// cannot be stale. Skipping it would let an export captured before the
	// daemon republished keep the finding alive off its own two bolt values
	// after the operator has already cleared the pane.
	if joined {
		icons = append(icons, live.IconState)
	}
	// No reported icon at all is not evidence of a stuck ⚡; a decline streak
	// with nothing to attribute it to is stale by definition.
	if len(icons) == 0 {
		return false
	}
	for _, icon := range icons {
		if !activity.ScreenProbeEligibleIcon(icon) {
			return false
		}
	}
	return true
}

func isPromptSessionEvent(event protocol.SessionEvent) bool {
	switch strings.ToLower(strings.TrimSpace(event.Event)) {
	case "userpromptsubmit", "userpromptsubmitted":
		return true
	default:
		return false
	}
}

func isUnknownProducerFact(fact protocol.SourceFactSnapshot) bool {
	if fact.Kind != protocol.SourceLegacy || fact.Acked || (fact.Icon == "" && !fact.LeaseLive) {
		return false
	}
	return strings.HasPrefix(fact.Key, "win:") || strings.HasPrefix(fact.Key, "window:")
}

func isEvictableAcknowledgedFact(fact protocol.SourceFactSnapshot) bool {
	if !fact.Acked || fact.Terminated {
		return false
	}
	return fact.Kind != protocol.SourceASL || !fact.LeaseLive
}

func hasASLFact(facts []protocol.SourceFactSnapshot) bool {
	for _, fact := range facts {
		if fact.Kind == protocol.SourceASL {
			return true
		}
	}
	return false
}

func allFactsAcked(facts []protocol.SourceFactSnapshot) bool {
	for _, fact := range facts {
		if !fact.Acked {
			return false
		}
	}
	return true
}

func countFindings(findings []doctorFinding, severity string) int {
	n := 0
	for _, finding := range findings {
		if finding.Severity == severity {
			n++
		}
	}
	return n
}
