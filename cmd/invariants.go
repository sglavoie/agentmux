package cmd

// Shared invariant evaluation over the daemon's schema-v9 export snapshot.
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
)

const (
	severityFailure = "failure"
	severityWarning = "warning"
	severityInfo    = "info"
)

// Invariant check names, stable identifiers for JSON consumers.
const (
	checkASLActiveState        = "asl_active_state"
	checkSubagentVisibility    = "subagent_visibility"
	checkActiveAfterTerminal   = "active_after_terminal_churn"
	checkShadowCurrentMismatch = "shadow_current_mismatch"
	checkQueueSaturation       = "queue_saturation"
	checkRenderGenerationDrift = "render_generation_drift"
	checkRenderCausalBound     = "render_causal_bound"
	checkBuildMismatch         = "build_mismatch"
	checkHarnessDrift          = "harness_drift"
	checkClosedTombstone       = "closed_tombstone"
	checkAcknowledgedState     = "acknowledged_state"
	checkOwnerMismatchPrompt   = "owner_mismatch_prompt"
	checkOwnerMismatchBurst    = "owner_mismatch_burst"
	checkUnknownProducerFact   = "unknown_producer_fact"
	checkAcknowledgedPressure  = "acknowledged_fact_pressure"
)

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
func evaluateDaemonInvariants(snapshot daemon.EventsExport, health daemon.Health, cliBuild buildinfo.Info, liveByID map[int]daemon.WindowLiveMetadata, now time.Time) []doctorFinding {
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
			if fact.Kind != daemon.SourceASL || fact.Acked || !fact.LeaseLive {
				continue
			}
			if fact.Phase != daemon.ASLPhaseTurn && fact.Phase != daemon.ASLPhaseVerification {
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
			if fact.Kind == daemon.SourceSubagent && !fact.Terminated && fact.Icon == "subagent" {
				liveAgents = true
				break
			}
			if fact.Kind == daemon.SourceLegacy && fact.Agents > 0 {
				liveAgents = true
				break
			}
		}
		if liveAgents && daemon.IconPrecedence(desired) < daemon.IconPrecedence("subagent") {
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
			if len(generation.TraceIDs) > daemon.MaxRenderCausalIDs ||
				len(generation.RequestIDs) > daemon.MaxRenderCausalIDs ||
				len(generation.SourceEventIDs) > daemon.MaxRenderCausalIDs {
				add(severityFailure, checkRenderCausalBound, id,
					"render generation %d exceeds the %d-ID causal bound (traces=%d requests=%d source_events=%d)",
					generation.Generation, daemon.MaxRenderCausalIDs, len(generation.TraceIDs),
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

func isPromptSessionEvent(event daemon.SessionEvent) bool {
	switch strings.ToLower(strings.TrimSpace(event.Event)) {
	case "userpromptsubmit", "userpromptsubmitted":
		return true
	default:
		return false
	}
}

func isUnknownProducerFact(fact daemon.SourceFactSnapshot) bool {
	if fact.Kind != daemon.SourceLegacy || fact.Acked || (fact.Icon == "" && !fact.LeaseLive) {
		return false
	}
	return strings.HasPrefix(fact.Key, "win:") || strings.HasPrefix(fact.Key, "window:")
}

func isEvictableAcknowledgedFact(fact daemon.SourceFactSnapshot) bool {
	if !fact.Acked || fact.Terminated {
		return false
	}
	return fact.Kind != daemon.SourceASL || !fact.LeaseLive
}

func hasASLFact(facts []daemon.SourceFactSnapshot) bool {
	for _, fact := range facts {
		if fact.Kind == daemon.SourceASL {
			return true
		}
	}
	return false
}

func allFactsAcked(facts []daemon.SourceFactSnapshot) bool {
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
