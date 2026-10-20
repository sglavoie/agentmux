package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestProbeCopilotTranscriptStop_ConcurrentPromptInvalidatesStopProbe proves the
// captured-sequence guard: a fresh prompt landing during the (slow) transcript
// read advances the reducer receipt past the value the probe captured, so the
// stale terminal read is discarded and the newer interactive bolt survives. The
// injected lifecycle reader models the concurrent prompt landing mid-read.
func TestProbeCopilotTranscriptStop_ConcurrentPromptInvalidatesStopProbe(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// The probe captures the receipt sequence under stateMu before its transcript
	// read; seeding a fresh prompt inside the reader models a concurrent
	// userPromptSubmitted landing after the capture.
	h.copilotLifecycleReader = func(path string) (transcript.CopilotLifecycleEvidence, error) {
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix()+1)
		return transcript.CopilotLifecycleEvidence{State: transcript.CopilotLifecycleTerminal, EventType: "assistant.turn_end"}, nil
	}

	h.ProbeCopilotTranscriptStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the newer bolt preserved against a stale stop probe", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
		t.Fatalf("TranscriptStopProbe row recorded despite a concurrent prompt: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_IntermediateTurnEndDoesNotFalseStop proves the
// stabilization gate. Installed Copilot emits an intermediate assistant.turn_end
// mid-interaction and appends another assistant.turn_start immediately
// afterward; a probe read landing in that sub-second gap sees a momentary
// terminal. One terminal sample must NOT settle: by the next observation the
// appended assistant.turn_start has advanced the transcript generation and reads
// active, so the streak restarts, the stuck bolt is preserved, and no
// TranscriptStopProbe is recorded.
func TestProbeCopilotTranscriptStop_IntermediateTurnEndDoesNotFalseStop(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// First observation: a lone terminal turn_end credits one stabilization sample
	// but must not settle on its own.
	h.ProbeCopilotTranscriptStop(base)
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls after one intermediate sample = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt after a single intermediate turn_end", eff)
	}

	// The interaction was not over: Copilot appends the next turn's start, growing
	// the transcript generation.
	appendCopilotTranscript(t, home, "sess-1", `{"type":"assistant.turn_start","data":{"turnId":"7"}}`+"\n")

	// Second observation now reads active, so the streak resets and the bolt stays.
	h.ProbeCopilotTranscriptStop(base)
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (intermediate turn_end must not settle)", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt preserved across an intermediate turn_end", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
		t.Fatalf("TranscriptStopProbe row recorded for an intermediate turn_end: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_TurnEndAppendedBeforeApplyKeepsBolt proves the
// apply-time freshness guard for the finding's tight race: the settling
// observation reads a terminal assistant.turn_end, then a new
// assistant.turn_start is appended to the transcript after that read but before
// the synthetic stop is applied — with no reducer event to advance the captured
// sequence. The freshness check must observe the grown transcript and discard the
// probe, preserving the bolt. The injected reader returns the terminal at the
// PRE-append size and performs the append, modeling the write that lands between
// the terminal read and the settle.
func TestProbeCopilotTranscriptStop_TurnEndAppendedBeforeApplyKeepsBolt(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// First observation credits a stabilization sample at the current generation.
	h.ProbeCopilotTranscriptStop(base)
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls after first sample = %d, want 0", got)
	}

	// Second observation reaches the required-samples threshold. The injected
	// reader returns the terminal it read at the pre-append size, then appends a
	// new turn_start to the real transcript — the write that races the settle.
	preAppend := copilotTranscriptSize(t, home, "sess-1")
	h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
		appendCopilotTranscript(t, home, "sess-1", `{"type":"assistant.turn_start","data":{"turnId":"7"}}`+"\n")
		return transcript.CopilotLifecycleEvidence{
			State:     transcript.CopilotLifecycleTerminal,
			EventType: "assistant.turn_end",
			Size:      preAppend,
		}, nil
	}

	h.ProbeCopilotTranscriptStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (append before apply must discard)", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt preserved against a transcript append before apply", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
		t.Fatalf("TranscriptStopProbe row recorded despite a transcript append before apply: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_PostStatAppendSelfHealsSyntheticStop proves the
// post-stat append recovery. The apply-time freshness check (ConfirmStable) bounds
// but cannot make atomic the read-then-settle ordering: Copilot's external writer
// can append an active record AFTER that freshness check and before the reducer
// mutation, so a synthetic 🛑 gets applied over a session that is actually still
// active. That stranded stop is modeled here by settling a stop and THEN growing
// the transcript with a new assistant.turn_start (the post-stat active append).
// The next bounded probe observation must read that appended active record, take
// its own stop back, and reinstate the interactive session — self-healing the
// stale synthetic stop with NO hook or prompt. A genuinely final stop (no append)
// is preserved: the sibling cases prove recovery never fires on still-terminal or
// non-probe-owned stops.
func TestProbeCopilotTranscriptStop_PostStatAppendSelfHealsSyntheticStop(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// Settle a synthetic stop from a stable terminal transcript: the tab converges
	// to 🛑 and the interactive bolt fact is retired.
	settleCopilotTurnEndProbe(h, base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop settled before the post-stat append", eff)
	}
	titlesAfterSettle := len(fake.titleCalls())

	// The post-stat active append: Copilot writes the next turn's start after the
	// freshness check that let the stop settle. The transcript now classifies
	// Active while the tab is stranded on the synthetic 🛑.
	appendCopilotTranscript(t, home, "sess-1", `{"type":"assistant.turn_start","data":{"turnId":"7"}}`+"\n")

	// The next bounded probe observation reads the appended active record on the
	// probe-owned stop tab and self-heals: it supersedes its own stop and
	// reinstates the interactive session, restoring ⚡ with no hook or prompt.
	h.ProbeCopilotTranscriptStop(base)

	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want bolt after post-stat append recovery", eff, visible)
	}
	calls := fake.titleCalls()
	if len(calls) != titlesAfterSettle+1 || calls[len(calls)-1].title != palette.BuildTitle("W:api", "bolt", false) {
		t.Fatalf("SetTabTitle calls = %+v, want one trailing bolt title from recovery", calls)
	}

	// The reducer facts reflect the recovery: the probe's synthetic stop is
	// withdrawn (acked) and the interactive session is reinstated (live).
	h.stateMu.Lock()
	interactive := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "copilot:sess-1"})
	probe := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource})
	h.stateMu.Unlock()
	if interactive == nil || interactive.Acked {
		t.Fatalf("interactive fact not reinstated after recovery: %+v", interactive)
	}
	// The withdrawn probe stop is an acked tombstone; Compact may already have
	// pruned it. Either way it must not survive as a live terminal.
	if probe != nil && !probe.Acked {
		t.Fatalf("probe stop fact not withdrawn after recovery: %+v", probe)
	}

	// A provenance row records the ACTUAL reduced result (bolt), never a stop.
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent)
	if !ok || ev.Action == "stop" {
		t.Fatalf("recovery provenance row = %+v ok=%v, want a non-stop reduced action", ev, ok)
	}

	// A later genuine completion still settles: append a final terminal and let it
	// stabilize; the ordinary bolt→stop cycle resumes with no external hook.
	appendCopilotTranscript(t, home, "sess-1", `{"type":"assistant.turn_end","data":{"turnId":"7"}}`+"\n")
	settleCopilotTurnEndProbe(h, base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop re-settled after recovery when the turn genuinely ends", eff)
	}
}

// TestProbeCopilotTranscriptStop_RecoveryNeverReopensNativeStop confirms recovery
// is scoped to this probe's own synthetic stop: a native harness terminal (an
// agentStop fact) that leaves the tab at 🛑 is never taken back, even if the
// transcript reads Active, because the stop is not the probe's contribution.
func TestProbeCopilotTranscriptStop_RecoveryNeverReopensNativeStop(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	// An active transcript: recovery WOULD fire if the stop were probe-owned.
	writeCopilotTranscript(t, home, "sess-1", copilotActiveTranscript)
	h, fake := copilotProbeHandler(t, 42, "stop", home)
	base := time.Now()

	// A native agentStop terminal fact owns the 🛑 — not the transcript probe.
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "copilot:sess-1"},
		Action:    "stop",
		SessionID: "sess-1",
		At:        base.Unix(),
		Event:     "agentStop",
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Event: "agentStop", Normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed native stop rejected: %+v", resp)
	}
	h.stateMu.Lock()
	h.store[42].Dirty = false
	h.store[42].DirtySince = time.Time{}
	h.stateMu.Unlock()

	h.ProbeCopilotTranscriptStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (native stop must not be reopened)", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want native stop preserved", eff)
	}
}

// TestProbeCopilotTranscriptStop_ActiveASLLeaseMasksStop confirms a live ASL
// loop lease masks the synthetic stop: terminal transcript evidence retires the
// interactive bolt, but the ASL turn fact keeps the tab looping/active, so the
// recorded row reports the ACTUAL reduced icon rather than a false stop.
func TestProbeCopilotTranscriptStop_ActiveASLLeaseMasksStop(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, _ := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// A live ASL turn lease: it contributes bolt + a loop lease that masks stop.
	aslEv := protocol.NormalizedEvent{
		Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"},
		Phase:  state.ASLPhaseTurn,
		RunID:  "run-1",
		At:     base.Unix(),
		Event:  events.AgentSessionLoopTurnStarted,
	}
	h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "loop_marker", Event: events.AgentSessionLoopTurnStarted, Normalized: &aslEv})

	settleCopilotTurnEndProbe(h, base)

	eff, visible := windowEffective(t, h, 42)
	if visible == "stop" || eff.Icon == "stop" {
		t.Fatalf("effective = %+v visible %q, want a masked (non-stop) state under a live ASL lease", eff, visible)
	}
	if !eff.Looping {
		t.Fatalf("effective = %+v, want the ASL loop lease preserved", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok && ev.Action == "stop" {
		t.Fatalf("TranscriptStopProbe recorded a false stop under a live ASL lease: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_PostStatAppendNotificationSuppressed proves the
// notification twin of the post-stat recovery. The apply-time ConfirmStable check
// bounds but cannot make atomic the read-then-settle ordering: Copilot's external
// writer can append an active record AFTER that freshness check and before the
// reducer mutation, so a synthetic 🛑 settles over a session that is actually still
// active. Recovery restores ⚡ on the next probe cycle, but a stop notification is
// dispatched asynchronously with no cancellable pending entry, so recovery cannot
// revoke it. The fix refuses to dispatch it: before the (unrevocable) notification,
// the probe re-reads the transcript and, seeing the same active append the recovery
// path will act on, SUPPRESSES the false "Session stopped" notification. The visible
// stop still settles and self-heals through recovery; only the notification is
// withheld. Here the ConfirmNotify re-read (and every read thereafter) reports Active,
// modeling the post-stat append landing between the settle-time freshness check and
// the reducer mutation.
func TestProbeCopilotTranscriptStop_PostStatAppendNotificationSuppressed(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	realSize := copilotTranscriptSize(t, home, "sess-1")
	h, _, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// The two stabilization-sample evidence reads see a terminal assistant.turn_end
	// at the unchanged transcript generation, so the stop settles. The ConfirmNotify
	// re-read after the settle — and every read after it — reports Active, modeling
	// the record Copilot appended between the ConfirmStable check and the settle.
	var reads int
	h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
		reads++
		if reads <= 2 {
			return transcript.CopilotLifecycleEvidence{
				State:     transcript.CopilotLifecycleTerminal,
				EventType: "assistant.turn_end",
				Size:      realSize,
			}, nil
		}
		return transcript.CopilotLifecycleEvidence{State: transcript.CopilotLifecycleActive}, nil
	}

	settleCopilotTurnEndProbe(h, base)

	// The synthetic stop settled the visible state (recovery restores it next cycle).
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop settled before recovery", eff)
	}
	// But the unrevocable false stop notification was suppressed: the post-stat
	// re-read showed the session is actually still active. No dispatch goroutine
	// was ever spawned, so this count is deterministic.
	if got := rec.len(); got != 0 {
		t.Fatalf("stop notifications dispatched = %d, want 0 (post-stat active append must suppress)", got)
	}

	// The next bounded probe observation self-heals ⚡ with no hook or prompt, and
	// still dispatches no notification.
	h.ProbeCopilotTranscriptStop(base)
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want bolt after post-stat recovery", eff, visible)
	}
	if got := rec.len(); got != 0 {
		t.Fatalf("notifications after recovery = %d, want 0", got)
	}
}
