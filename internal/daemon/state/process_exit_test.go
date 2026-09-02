package state

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

var (
	testInteractiveProcess = protocol.HarnessProcessIdentity{
		Harness: "claude", PID: 4101, StartToken: "Thu Jul 16 10:20:30 2026",
	}
	testPipeProcess = protocol.HarnessProcessIdentity{
		Harness: "claude", PID: 4201, StartToken: "Thu Jul 16 10:21:30 2026",
	}
)

func processReducerEvent(
	source protocol.SourceID,
	action string,
	seq uint64,
	at int64,
	sessionID string,
	pipeMode bool,
	identity protocol.HarnessProcessIdentity,
) protocol.ReducerEvent {
	return protocol.ReducerEvent{
		Source:         source,
		Action:         action,
		Seq:            seq,
		At:             at,
		SessionID:      sessionID,
		PipeMode:       pipeMode,
		HarnessProcess: identity,
	}
}

func TestReducerHarnessProcessIdentityReplacement(t *testing.T) {
	r := NewReducer(zeroCfg)
	source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"}
	canonical := testInteractiveProcess
	raw := canonical
	raw.Harness = " Claude "
	raw.StartToken = "  Thu   Jul 16 10:20:30 2026  "

	r.Apply(processReducerEvent(source, "permission", 1, 100, "session-1", false, raw))
	f := r.facts[source]
	if f == nil || f.harnessProcess != canonical {
		t.Fatalf("stored process = %+v, want canonical %+v", f, canonical)
	}

	// The idle is suppressed to preserve the permission icon, but its accepted
	// sequence still clears ownership because it carries no valid replacement.
	if got := r.Apply(processReducerEvent(source, "idle", 2, 101, "session-1", false, protocol.HarnessProcessIdentity{})); got != protocol.DecisionSuppressed {
		t.Fatalf("idle decision = %q, want suppressed", got)
	}
	if f.harnessProcess != (protocol.HarnessProcessIdentity{}) || f.icon != "key" {
		t.Fatalf("suppressed idle left fact %+v; want key with cleared process", f)
	}

	invalid := testInteractiveProcess
	invalid.Harness = "codex" // does not match the claude source
	r.Apply(processReducerEvent(source, "active", 3, 102, "session-1", false, invalid))
	if f.harnessProcess != (protocol.HarnessProcessIdentity{}) {
		t.Fatalf("producer-mismatched identity was retained: %+v", f.harnessProcess)
	}

	legacy := protocol.SourceID{Kind: protocol.SourceLegacy, Key: "win:42"}
	agent := protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}
	r.Apply(processReducerEvent(legacy, "active", 1, 103, "", false, canonical))
	r.Apply(processReducerEvent(agent, "subagent", 1, 104, "session-1", false, canonical))
	if r.facts[legacy].harnessProcess != canonical || r.facts[agent].harnessProcess != canonical {
		t.Fatalf("ordinary legacy/subagent facts lost process ownership: legacy=%+v agent=%+v", r.facts[legacy], r.facts[agent])
	}

	asl := protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}
	probe := protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}
	r.Apply(protocol.ReducerEvent{Source: asl, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: 105, HarnessProcess: canonical})
	r.Apply(processReducerEvent(probe, "permission", 1, 106, "", false, canonical))
	if r.facts[asl].harnessProcess != (protocol.HarnessProcessIdentity{}) || r.facts[probe].harnessProcess != (protocol.HarnessProcessIdentity{}) {
		t.Fatalf("ASL/probe facts retained forbidden process ownership: asl=%+v probe=%+v", r.facts[asl], r.facts[probe])
	}

	normalized := protocol.NormalizedEvent{
		Source: source, Action: "active", Seq: 4, At: 107,
		SessionID: "session-1", HarnessProcess: canonical,
	}
	if got := normalized.ReducerEvent().HarnessProcess; got != canonical {
		t.Fatalf("normalized projection process = %+v, want %+v", got, canonical)
	}
}

func TestReducerSuppressedModeFlipUsesReplacementProcessExitPolicy(t *testing.T) {
	r := NewReducer(zeroCfg)
	source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"}

	r.Apply(processReducerEvent(source, "permission", 1, 100, "session-1", false, testInteractiveProcess))
	if got := r.Apply(processReducerEvent(source, "idle", 2, 101, "session-1", true, testPipeProcess)); got != protocol.DecisionSuppressed {
		t.Fatalf("mode-flip idle decision = %q, want suppressed", got)
	}

	facts := r.Facts()
	pipeMode, consistent := LifecycleGroupPipeMode(facts)
	if !consistent || !pipeMode {
		t.Fatalf("replacement process lifecycle mode = (%v, %v), want (true, true); facts=%+v", pipeMode, consistent, facts)
	}

	result := r.SettleHarnessProcessExit(testPipeProcess, 200, HarnessProcessExitOptions{PipeMode: pipeMode})
	if result.Changed || result.SynthesizedStop || result.Reason != HarnessProcessExitReasonPipe {
		t.Fatalf("replacement process settlement = %+v, want preserved attention under pipe policy", result)
	}
	if fact := r.facts[source]; fact.harnessProcess != testPipeProcess || !fact.pipeMode || fact.icon != "key" || fact.acked {
		t.Fatalf("suppressed replacement fact after exit = %+v, want unacknowledged pipe-owned permission", fact)
	}
}

func TestReducerPipeProcessExitPolicy(t *testing.T) {
	r := NewReducer(zeroCfg)
	eligible := []protocol.ReducerEvent{
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:active"}, "active", 1, 100, "active", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:compact"}, "compact", 1, 101, "compact", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-pipe"}, "subagent", 1, 102, "active", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceLegacy, Key: "pipe-idle"}, "idle", 1, 103, "", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:clean-stop"}, "stop", 1, 104, "clean-stop", true, testPipeProcess),
	}
	protected := []protocol.ReducerEvent{
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:permission"}, "permission", 1, 105, "permission", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:question"}, "elicitation", 1, 106, "question", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:warning"}, "failure", 1, 107, "warning", true, testPipeProcess),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:error"}, "stop_failure", 1, 108, "error", true, testPipeProcess),
	}
	for _, ev := range append(eligible, protected...) {
		r.Apply(ev)
	}

	reused := testPipeProcess
	reused.StartToken = "Thu Jul 16 11:21:30 2026"
	unrelated := processReducerEvent(
		protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:reused"}, "active", 1, 109, "reused", true, reused,
	)
	interactiveSameFingerprint := processReducerEvent(
		protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:interactive"}, "active", 1, 110, "interactive", false, testPipeProcess,
	)
	r.Apply(unrelated)
	r.Apply(interactiveSameFingerprint)

	result := r.SettleHarnessProcessExit(testPipeProcess, 200, HarnessProcessExitOptions{PipeMode: true})
	if !result.Changed || result.SynthesizedStop || result.Reason != HarnessProcessExitReasonPipe {
		t.Fatalf("result = %+v, want changed pipe withdrawal without stop", result)
	}
	if result.WithdrawnFacts != len(eligible) || len(result.WithdrawnSources) != len(eligible) {
		t.Fatalf("withdrawal counts = %+v, want %d", result, len(eligible))
	}
	wantWithdrawn := []protocol.SourceID{
		{Kind: protocol.SourceHarness, Key: "claude:active"},
		{Kind: protocol.SourceHarness, Key: "claude:clean-stop"},
		{Kind: protocol.SourceHarness, Key: "claude:compact"},
		{Kind: protocol.SourceLegacy, Key: "pipe-idle"},
		{Kind: protocol.SourceSubagent, Key: "agent-pipe"},
	}
	if !reflect.DeepEqual(result.WithdrawnSources, wantWithdrawn) {
		t.Fatalf("withdrawn sources = %+v, want deterministic %+v", result.WithdrawnSources, wantWithdrawn)
	}
	if result.ClearedSubagents != 0 || len(result.ClearedSubagentSources) != 0 {
		t.Fatalf("pipe result reported terminal subagent clearing: %+v", result)
	}
	for _, ev := range eligible {
		f := r.facts[ev.Source]
		if f == nil || !f.acked || f.ackedAt != 200 {
			t.Fatalf("eligible fact %+v not withdrawn at exit: %+v", ev.Source, f)
		}
	}
	for _, ev := range protected {
		if f := r.facts[ev.Source]; f == nil || f.acked {
			t.Fatalf("protected fact %+v was withdrawn: %+v", ev.Source, f)
		}
	}
	if r.facts[unrelated.Source].acked || r.facts[interactiveSameFingerprint.Source].acked {
		t.Fatalf("unrelated/reclassified facts changed: reused=%+v interactive=%+v", r.facts[unrelated.Source], r.facts[interactiveSameFingerprint.Source])
	}
	if result.Before.Icon != "error" || result.After.Icon != "error" {
		t.Fatalf("protected error should remain effective: before=%+v after=%+v", result.Before, result.After)
	}

	again := r.SettleHarnessProcessExit(testPipeProcess, 201, HarnessProcessExitOptions{PipeMode: true})
	if again.Changed || again.SynthesizedStop || again.WithdrawnFacts != 0 || again.Before != again.After {
		t.Fatalf("duplicate pipe settlement = %+v, want no-op", again)
	}
}

func TestReducerPipeProcessExitRevealsUnderlyingState(t *testing.T) {
	tests := []struct {
		name       string
		survivor   *protocol.ReducerEvent
		wantIcon   string
		wantSource protocol.SourceID
	}{
		{
			name: "interactive parent",
			survivor: func() *protocol.ReducerEvent {
				ev := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:parent"}, "active", 1, 100, "parent", false, testInteractiveProcess)
				return &ev
			}(),
			wantIcon: "bolt", wantSource: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:parent"},
		},
		{
			name: "another pipe process",
			survivor: func() *protocol.ReducerEvent {
				other := testPipeProcess
				other.PID++
				ev := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:other-pipe"}, "active", 1, 100, "other-pipe", true, other)
				return &ev
			}(),
			wantIcon: "bolt", wantSource: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:other-pipe"},
		},
		{name: "empty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			if tc.survivor != nil {
				r.Apply(*tc.survivor)
			}
			exiting := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:exiting-pipe"}, "active", 1, 150, "exiting-pipe", true, testPipeProcess)
			r.Apply(exiting)

			result := r.SettleHarnessProcessExit(testPipeProcess, 200, HarnessProcessExitOptions{PipeMode: true})
			if result.Before.Source != exiting.Source || result.After.Icon != tc.wantIcon || result.After.Source != tc.wantSource {
				t.Fatalf("settlement before/after = %+v/%+v, want after icon=%q source=%+v", result.Before, result.After, tc.wantIcon, tc.wantSource)
			}
		})
	}
}

func TestReducerInteractiveProcessExitSynthesizesStop(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
	}{
		{name: "active", action: "active"},
		{name: "compact", action: "compact"},
		{name: "permission", action: "permission"},
		{name: "warning", action: "failure"},
		{name: "question", action: "elicitation"},
		{name: "iconless parent", action: "idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"}
			r.Apply(processReducerEvent(source, tc.action, 7, 100, "session-1", false, testInteractiveProcess))
			beforeOrder := r.facts[source].causalOrder

			result := r.SettleHarnessProcessExit(testInteractiveProcess, 200, HarnessProcessExitOptions{})
			if !result.Changed || !result.SynthesizedStop || result.Reason != HarnessProcessExitReasonInteractive {
				t.Fatalf("result = %+v, want synthesized interactive stop", result)
			}
			f := r.facts[source]
			if f.action != "stop" || f.icon != "stop" || !f.terminated || f.terminalAt != 200 || f.at != 200 || f.seq != 7 {
				t.Fatalf("settled parent = %+v, want clean stop retaining source sequence", f)
			}
			if f.causalOrder <= beforeOrder {
				t.Fatalf("synthetic stop causal order = %d, want newer than %d", f.causalOrder, beforeOrder)
			}
			if result.After.Icon != "stop" || result.After.Source != source {
				t.Fatalf("after = %+v, want parent stop", result.After)
			}
		})
	}
}

func TestReducerInteractiveProcessExitPreservesTerminalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		action   string
		wantIcon string
	}{
		{name: "clean stop", action: "stop", wantIcon: "stop"},
		{name: "failure", action: "stop_failure", wantIcon: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:session-1"}
			r.Apply(processReducerEvent(source, tc.action, 1, 100, "session-1", false, testInteractiveProcess))
			before := *r.facts[source]

			result := r.SettleHarnessProcessExit(testInteractiveProcess, 200, HarnessProcessExitOptions{})
			if result.Changed || result.SynthesizedStop || result.After.Icon != tc.wantIcon {
				t.Fatalf("result = %+v, want existing terminal %q preserved", result, tc.wantIcon)
			}
			if after := *r.facts[source]; !reflect.DeepEqual(after, before) {
				t.Fatalf("existing terminal mutated\nbefore=%+v\nafter =%+v", before, after)
			}
		})
	}
}

func TestReducerInteractiveProcessExitChoosesNewestParentAndClearsItsAgents(t *testing.T) {
	r := NewReducer(zeroCfg)
	old := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}, "active", 50, 100, "old", false, testInteractiveProcess)
	middle := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:middle"}, "compact", 1, 150, "middle", false, testInteractiveProcess)
	current := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:current"}, "permission", 1, 200, "current", false, testInteractiveProcess)
	for _, ev := range []protocol.ReducerEvent{old, middle, current} {
		r.Apply(ev)
	}

	currentAgent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-current"}, "subagent", 1, 210, "current", false, protocol.HarnessProcessIdentity{})
	oldAgent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-old"}, "subagent", 1, 211, "old", false, testInteractiveProcess)
	currentBucket := processReducerEvent(protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey("current")}, "subagent", 1, 212, "current", false, protocol.HarnessProcessIdentity{})
	currentBucket.SubagentDelta = 1
	for _, ev := range []protocol.ReducerEvent{currentAgent, oldAgent, currentBucket} {
		r.Apply(ev)
	}

	result := r.SettleHarnessProcessExit(testInteractiveProcess, 300, HarnessProcessExitOptions{})
	if !result.SynthesizedStop || result.WithdrawnFacts != 2 || result.ClearedSubagents != 2 {
		t.Fatalf("result = %+v, want one stop two retired parents and two cleared current agents", result)
	}
	if f := r.facts[current.Source]; f.action != "stop" || !f.terminated {
		t.Fatalf("newest parent was not settled: %+v", f)
	}
	for _, source := range []protocol.SourceID{old.Source, middle.Source} {
		if f := r.facts[source]; f == nil || !f.acked || f.ackedAt != 300 {
			t.Fatalf("older active parent %+v was not retired: %+v", source, f)
		}
	}
	if f := r.facts[currentAgent.Source]; f == nil || !f.terminated || f.icon != "" {
		t.Fatalf("current identified agent not cleared: %+v", f)
	}
	if f := r.facts[currentBucket.Source]; f == nil || f.agents != 0 || f.icon != "" {
		t.Fatalf("current legacy agent bucket not cleared: %+v", f)
	}
	if f := r.facts[oldAgent.Source]; f == nil || f.terminated || f.acked || f.icon != "subagent" {
		t.Fatalf("older session's agent was altered: %+v", f)
	}

	afterFirst := cloneReducerSourceFacts(r)
	again := r.SettleHarnessProcessExit(testInteractiveProcess, 301, HarnessProcessExitOptions{})
	if again.Changed || again.SynthesizedStop || !reflect.DeepEqual(cloneReducerSourceFacts(r), afterFirst) {
		t.Fatalf("duplicate interactive settlement mutated state: %+v", again)
	}
}

func TestReducerInteractiveProcessExitClearsSubagentsInDeterministicOrder(t *testing.T) {
	parent := processReducerEvent(
		protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:current"},
		"active", 1, 100, "current", false, testInteractiveProcess,
	)
	agents := []protocol.ReducerEvent{
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-zulu"}, "subagent", 1, 110, "current", false, protocol.HarnessProcessIdentity{}),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-alpha"}, "subagent", 1, 111, "current", false, protocol.HarnessProcessIdentity{}),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-mike"}, "subagent", 1, 112, "current", false, protocol.HarnessProcessIdentity{}),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-bravo"}, "subagent", 1, 113, "current", false, protocol.HarnessProcessIdentity{}),
		processReducerEvent(protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey("current")}, "subagent", 1, 114, "current", false, protocol.HarnessProcessIdentity{}),
	}
	agents[len(agents)-1].SubagentDelta = 1

	// Vary insertion order and map allocation repeatedly. The settlement must
	// assign the same source-sorted causal order independently of map iteration.
	for run := 0; run < 32; run++ {
		r := NewReducer(zeroCfg)
		r.Apply(parent)
		for step := range agents {
			index := (step + run) % len(agents)
			if run%2 != 0 {
				index = len(agents) - 1 - index
			}
			r.Apply(agents[index])
		}

		result := r.SettleHarnessProcessExit(testInteractiveProcess, 200, HarnessProcessExitOptions{})
		if !result.SynthesizedStop || result.ClearedSubagents != len(agents) {
			t.Fatalf("run %d result = %+v, want one stop and %d cleared subagents", run, result, len(agents))
		}
		parentOrder := r.facts[parent.Source].causalOrder
		for i, source := range result.ClearedSubagentSources {
			wantOrder := parentOrder + uint64(i) + 1
			if got := r.facts[source].causalOrder; got != wantOrder {
				t.Fatalf("run %d causal order for sorted source %+v = %d, want %d; cleared=%+v", run, source, got, wantOrder, result.ClearedSubagentSources)
			}
		}
	}
}

func TestReducerInteractiveProcessExitWithoutSessionClearsExactProcessAgents(t *testing.T) {
	r := NewReducer(zeroCfg)
	parent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceLegacy, Key: "win:42"}, "active", 1, 100, "", false, testInteractiveProcess)
	exactAgent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-exact"}, "subagent", 1, 101, "unknown", false, testInteractiveProcess)
	exactBucket := processReducerEvent(protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey("win:42")}, "subagent", 1, 102, "", false, testInteractiveProcess)
	exactBucket.SubagentDelta = 1
	otherProcess := testInteractiveProcess
	otherProcess.PID++
	otherAgent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-other"}, "subagent", 1, 103, "unknown", false, otherProcess)
	for _, ev := range []protocol.ReducerEvent{parent, exactAgent, exactBucket, otherAgent} {
		r.Apply(ev)
	}

	result := r.SettleHarnessProcessExit(testInteractiveProcess, 200, HarnessProcessExitOptions{})
	if !result.SynthesizedStop || result.ClearedSubagents != 2 {
		t.Fatalf("result = %+v, want exact-process agent cleanup", result)
	}
	if !r.facts[exactAgent.Source].terminated || r.facts[exactBucket.Source].agents != 0 {
		t.Fatalf("exact-process agents survived: agent=%+v bucket=%+v", r.facts[exactAgent.Source], r.facts[exactBucket.Source])
	}
	if f := r.facts[otherAgent.Source]; f == nil || f.terminated || f.acked {
		t.Fatalf("other process agent changed: %+v", f)
	}
}

func TestReducerHarnessProcessExitExactFingerprintAndResetExclusions(t *testing.T) {
	r := NewReducer(zeroCfg)
	active := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:active"}, "active", 1, 100, "active", false, testInteractiveProcess)
	errorFact := processReducerEvent(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:error"}, "stop_failure", 1, 101, "error", false, testInteractiveProcess)
	agent := processReducerEvent(protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, "subagent", 1, 102, "active", false, testInteractiveProcess)
	// Apply the terminal outcome first so the later distinct-source activity is
	// still live; clear-only settlement must withdraw both classes.
	for _, ev := range []protocol.ReducerEvent{errorFact, active, agent} {
		r.Apply(ev)
	}
	aslSource := protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}
	r.Apply(protocol.ReducerEvent{Source: aslSource, RunID: "run-1", Phase: ASLPhaseBetween, Seq: 1, At: 103})
	probeSource := protocol.SourceID{Kind: protocol.SourceProbe, Key: "screen"}
	r.Apply(protocol.ReducerEvent{Source: probeSource, Action: "permission", Seq: 1, At: 104})
	// Corrupt/test-inject matching metadata to prove settlement excludes ASL and
	// daemon probes regardless of matching process fields.
	r.facts[aslSource].harnessProcess = testInteractiveProcess
	r.facts[probeSource].harnessProcess = testInteractiveProcess
	aslBefore := *r.facts[aslSource]
	probeBefore := *r.facts[probeSource]

	reused := testInteractiveProcess
	reused.StartToken = "Thu Jul 16 12:20:30 2026"
	noMatch := r.SettleHarnessProcessExit(reused, 150, HarnessProcessExitOptions{ClearOnly: true})
	if noMatch.Changed || noMatch.Before != noMatch.After {
		t.Fatalf("same PID with different start token matched: %+v", noMatch)
	}

	result := r.SettleHarnessProcessExit(testInteractiveProcess, 200, HarnessProcessExitOptions{ClearOnly: true})
	if !result.Changed || result.SynthesizedStop || result.Reason != HarnessProcessExitReasonReset || result.WithdrawnFacts != 3 {
		t.Fatalf("reset result = %+v, want three exact ordinary withdrawals", result)
	}
	for _, source := range []protocol.SourceID{active.Source, errorFact.Source, agent.Source} {
		if f := r.facts[source]; f == nil || !f.acked || f.ackedAt != 200 {
			t.Fatalf("reset did not withdraw %+v: %+v", source, f)
		}
	}
	if after := *r.facts[aslSource]; !reflect.DeepEqual(after, aslBefore) {
		t.Fatalf("ASL fact changed during process settlement\nbefore=%+v\nafter =%+v", aslBefore, after)
	}
	if after := *r.facts[probeSource]; !reflect.DeepEqual(after, probeBefore) {
		t.Fatalf("probe fact changed during process settlement\nbefore=%+v\nafter =%+v", probeBefore, after)
	}
	if !result.After.Looping {
		t.Fatalf("ASL lease was released: %+v", result.After)
	}

	invalid := r.SettleHarnessProcessExit(protocol.HarnessProcessIdentity{Harness: "claude", PID: 1, StartToken: "token"}, 201, HarnessProcessExitOptions{})
	if invalid.Changed || invalid.Reason != HarnessProcessExitReasonInvalid || invalid.Before != invalid.After {
		t.Fatalf("invalid identity result = %+v", invalid)
	}
}

func cloneReducerSourceFacts(r *Reducer) map[protocol.SourceID]sourceFact {
	out := make(map[protocol.SourceID]sourceFact, len(r.facts))
	for source, fact := range r.facts {
		out[source] = *fact
	}
	return out
}
