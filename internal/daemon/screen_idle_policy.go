package daemon

import (
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// This file is the ONE place a screen-idle probe learns how many consecutive
// agreeing samples a given (harness, tier) has to produce before a synthetic
// stop may settle. It replaces the per-harness constants that used to sit
// beside each probe body.

const (
	// idleMinimumSamples is 2 because the Generation gate is INERT below 2: a
	// single sample is never compared against anything, so a one-sample tier
	// opts out of stabilization entirely — which is exactly the hole this plan
	// closes. It is the fail-closed answer for a (harness, tier) pair nobody
	// wired, never a general floor applied over the table.
	idleMinimumSamples = 2

	// claudeIdleProbeRequiredSamples is what positive terminal evidence costs on
	// Claude: screenstate.TierTerminalExplicit (an interruption marker Claude
	// generated) and screenstate.TierTerminalShape (a "<word> for <duration>"
	// completion line) both settle here, because both are a marker the harness
	// only emits once a turn is over. Claude's completed-turn screen is quieter
	// than a running one, so two samples defend against a single transient frame.
	claudeIdleProbeRequiredSamples = idleMinimumSamples

	// claudeIdleProbeQuiescentSamples is what screenstate.TierQuiescent costs: a
	// safe screen with an empty composer and no positive terminal marker at all.
	// That tier is not evidence a turn ended, only the absence of evidence that
	// one is running — and it is the exact shape the reported false stop settled
	// on. It therefore buys one sample over the floor (~6s at the 2s probe
	// cadence), which is also one more independent screen fingerprint the pane
	// has to reproduce before it can settle.
	claudeIdleProbeQuiescentSamples = 3

	// claudeIdleProbeMarkerSampleBump is what a pane title still carrying a
	// marker rune adds ON TOP of the screen's own tier, over a description body
	// or Claude's generic resting body alike.
	//
	// It used to be a flat override of 4 that replaced the tier outright. That
	// conflated two different questions: a marker is stale-tolerant evidence
	// (Claude leaves the last animation frame behind after a turn ends), so it
	// should lengthen a run rather than decide its length — an explicit
	// interruption marker on screen is not made weaker by a leftover spinner in
	// the title. As a bump the whole table lands in 2–4 samples, keeping the
	// slowest path at the 4 the flat override charged while letting positive
	// terminal evidence settle in 3.
	claudeIdleProbeMarkerSampleBump = 1

	// codexIdleProbeRequiredSamples is what a terminal Codex screen costs. It sits
	// on the floor because that is the lowest count at which the Generation the
	// Codex probe supplies is compared at all: below 2 there is no second sample
	// to compare against, so Layer B is inert and Codex would be settling on
	// Layer A alone — a marker regex over one frame.
	//
	// It used to be 1, on the reasoning that Codex owns its own screen so a
	// completed-turn read is unambiguous. That reasoning is about
	// CLASSIFICATION and is untouched here; the extra sample answers a different
	// question — whether the pane is still repainting — which no amount of
	// classifier confidence can answer from a single frame. Session 2 measured
	// the cost: an idle codex-cli pane is byte-stable across a 3s window (six
	// independent windows, all stable), while a working one changes its elapsed
	// timer, so the second sample only ever delays recovery by one 2s tick and
	// never strands a genuinely idle pane.
	//
	// This matters most on Codex: it installs 10 hooks against Claude's 28, and
	// Stop is its only terminal one, so screen evidence carries proportionally
	// more of the load here than anywhere else.
	codexIdleProbeRequiredSamples = idleMinimumSamples
)

// idlePolicyKey is one row of the sample table.
type idlePolicyKey struct {
	harness string
	tier    screenstate.Tier
}

// idleRequiredSamples is the single table. Every reachable (harness, tier) pair
// a screen idle probe can produce has a row here; see requiredIdleSamples for
// what a miss means.
//
// Copilot stays out of the table ON PURPOSE. Its Generation is a transcript
// byte size rather than a screen fingerprint, and its whole-session terminals
// (`abort`, `session.shutdown`) legitimately settle on one sample because they
// carry no generation and cannot be intermediate. copilotTurnEndRequiredSamples
// owns that policy and is untouched by this table.
//
// Per session 6, only the Codex TierTerminalExplicit row is reachable today —
// the Codex adapter never grades a screen as Shape or Quiescent. The other two
// rows are forward-compat and deliberately hold the same value, so a future
// Codex tier cannot land on the fail-closed miss path by accident.
var idleRequiredSamples = map[idlePolicyKey]int{
	{harness.Claude, screenstate.TierTerminalExplicit}: claudeIdleProbeRequiredSamples,
	{harness.Claude, screenstate.TierTerminalShape}:    claudeIdleProbeRequiredSamples,
	{harness.Claude, screenstate.TierQuiescent}:        claudeIdleProbeQuiescentSamples,
	{harness.Codex, screenstate.TierTerminalExplicit}:  codexIdleProbeRequiredSamples,
	{harness.Codex, screenstate.TierTerminalShape}:     codexIdleProbeRequiredSamples,
	{harness.Codex, screenstate.TierQuiescent}:         codexIdleProbeRequiredSamples,
}

// requiredIdleSamples is the table lookup, plus whatever extra samples the pane
// title bought on top of the tier's own count.
//
// A MISS returns idleMinimumSamples and drops the bump. This is miss-only and
// never a general floor: every wired row is free to charge MORE than the floor
// (Claude's quiescent tier charges 3, and the marker bump can take a row to 4),
// so clamping here would be both redundant for the rows that sit on the floor
// and wrong for the ones above it.
//
// The explicit miss return is load-bearing rather than defensive.
// applyAuthoritativeIdle clamps a RequiredSamples below 1 up to 1, so a bare map
// lookup on a miss would yield the zero value and degrade to a single sample —
// today's Codex value, but BELOW Claude's floor of 2, which would re-open the
// exact bug this plan fixes. A tier nobody wired must hold ⚡, not settle.
func requiredIdleSamples(harnessName string, tier screenstate.Tier, titleBump int) int {
	required, ok := idleRequiredSamples[idlePolicyKey{harness: harnessName, tier: tier}]
	if !ok {
		return idleMinimumSamples
	}
	return required + titleBump
}

// claudeIdleProbeSamples is the Claude-shaped view of the table above: the
// tier's own count plus the pane-title marker bump. It is one lookup into
// idleRequiredSamples, never a second policy.
func claudeIdleProbeSamples(tier screenstate.Tier, titleHasMarker bool) int {
	bump := 0
	if titleHasMarker {
		bump = claudeIdleProbeMarkerSampleBump
	}
	return requiredIdleSamples(harness.Claude, tier, bump)
}
