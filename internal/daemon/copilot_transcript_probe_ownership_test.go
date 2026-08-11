package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestProbeCopilotTranscriptStop_MissingProfileHomeKeepsBolt confirms an
// unresolvable session (no configured Copilot home and no default home) is a
// no-op: the resolver returns an error and the bolt is preserved.
func TestProbeCopilotTranscriptStop_MissingProfileHomeKeepsBolt(t *testing.T) {
	h := newTestHandler()
	h.SetCopilotTranscriptHomes(nil, "") // no profile homes, no default home
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "copilot",
		}),
	}
	h.SetKittyClient(fake)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCopilotTranscriptStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt preserved with no resolvable home", eff)
	}
}

// TestProbeCopilotTranscriptStop_PrefersInteractiveOverPipeChild proves the
// probe resolves the interactive session's transcript, never a newer pipe-mode
// `copilot -p` child's. The interactive session's transcript is terminal while
// the pipe child's is still active, so only selecting the interactive owner can
// settle: the interactive bolt fact must be retired while the protected pipe
// child fact is preserved. The pipe child then legitimately masks the reduced
// icon (a pipe-mode child is never falsely stopped), so the recorded row reports
// the actual reduced state rather than stop.
func TestProbeCopilotTranscriptStop_PrefersInteractiveOverPipeChild(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-interactive", copilotTurnEndTranscript)
	writeCopilotTranscript(t, home, "sess-pipe", copilotActiveTranscript)
	h, _ := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-interactive", base.Unix())
	// A newer pipe-mode child fact must NOT win the current conversation.
	copilotSeedActiveBoltPiped(t, h, 42, "sess-pipe", base.Unix()+5, true)

	settleCopilotTurnEndProbe(h, base)

	h.stateMu.Lock()
	interactive := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-interactive"}]
	pipe := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-pipe"}]
	h.stateMu.Unlock()
	// The interactive session's terminal transcript was the one resolved, so its
	// stale bolt was retired.
	if interactive == nil || !interactive.acked {
		t.Fatalf("interactive fact not retired (wrong session resolved?): %+v", interactive)
	}
	// The protected pipe-mode child fact is never masked by interactive
	// supersession and keeps the tab non-stopped.
	if pipe == nil || pipe.acked {
		t.Fatalf("pipe-mode child fact was retired, want preserved: %+v", pipe)
	}
	if eff, visible := windowEffective(t, h, 42); visible == "stop" || eff.Icon == "stop" {
		t.Fatalf("effective = %+v visible %q, want a non-stop state masked by the pipe child", eff, visible)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok && ev.Action == "stop" {
		t.Fatalf("TranscriptStopProbe recorded a false stop while a pipe child is active: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_PipeOnlyChildNeverProbed proves the bolt path
// resolves an INTERACTIVE owner only and never a sole pipe-mode `copilot -p`
// child. A window whose only live harness fact is a pipe child shows ⚡ (the pipe
// bolt), but CurrentConversation's descriptive pipe fallback must not select that
// child's transcript: the probe bails before ANY transcript resolution or read,
// so a background pipe invocation can never create or refresh a synthetic
// transcript-probe stop. The injected reader is a tripwire that fails the test if
// the probe reaches transcript I/O at all.
func TestProbeCopilotTranscriptStop_PipeOnlyChildNeverProbed(t *testing.T) {
	home := t.TempDir()
	// A terminal pipe-child transcript that WOULD settle a stop if wrongly read.
	writeCopilotTranscript(t, home, "sess-pipe", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	// The window's ONLY live harness fact is a pipe-mode child.
	copilotSeedActiveBoltPiped(t, h, 42, "sess-pipe", base.Unix(), true)

	readReached := false
	h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
		readReached = true
		return transcript.CopilotLifecycleEvidence{
			State:     transcript.CopilotLifecycleTerminal,
			EventType: "assistant.turn_end",
		}, nil
	}

	h.ProbeCopilotTranscriptStop(base)

	if readReached {
		t.Fatalf("probe read a pipe-only child's transcript; want rejection before transcript resolution/read")
	}
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (pipe-only child must not settle)", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the pipe bolt preserved", eff)
	}
	// No synthetic transcript-probe stop fact was created or refreshed.
	h.stateMu.Lock()
	probe := h.store[42].Reducer.facts[SourceID{Kind: SourceProbe, Key: copilotTranscriptStopProbeSource}]
	h.stateMu.Unlock()
	if probe != nil {
		t.Fatalf("transcript-probe stop fact created for a pipe-only child: %+v", probe)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
		t.Fatalf("TranscriptStopProbe row recorded for a pipe-only child: %+v", ev)
	}
}

// TestProbeCopilotTranscriptStop_MaskingPipeChildPreservesParentMemoAndRecovers
// proves the parent-session memo survives a pipe child that masks a probe-owned
// stop, and that recovery reinstates the ORIGINAL interactive parent once the
// pipe contribution disappears. First an interactive turn settles a synthetic 🛑
// (memo = the interactive session). Then a pipe-mode `copilot -p` child appears:
// its live bolt masks the stop, so the tab shows ⚡ again. A normal pipe transcript
// sample must NOT re-enter the bolt path against the pipe child and overwrite the
// probe stop's parent-session memo — otherwise a later Active read could not
// identify and reinstate the retired interactive parent. Once the pipe child's
// contribution is gone and the interactive transcript reads Active, recovery
// restores the interactive parent's ⚡ with no hook or prompt.
func TestProbeCopilotTranscriptStop_MaskingPipeChildPreservesParentMemoAndRecovers(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-interactive", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-interactive", base.Unix())

	// Settle a synthetic stop for the interactive session: memo = sess-interactive,
	// interactive bolt retired, tab converges to 🛑.
	settleCopilotTurnEndProbe(h, base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop settled for the interactive session", eff)
	}
	assertProbeMemo := func(want string) {
		t.Helper()
		h.stateMu.Lock()
		probe := h.store[42].Reducer.facts[SourceID{Kind: SourceProbe, Key: copilotTranscriptStopProbeSource}]
		h.stateMu.Unlock()
		if probe == nil || probe.sessionID != want {
			t.Fatalf("probe stop memo = %+v, want sessionID %q", probe, want)
		}
	}
	assertProbeMemo("sess-interactive")

	// A pipe-mode `copilot -p` child appears with a TERMINAL transcript of its own —
	// the exact poison that, if read on the bolt path, would overwrite the memo to
	// the pipe session. Its live bolt masks the probe stop, so the tab shows ⚡.
	writeCopilotTranscript(t, home, "sess-pipe", copilotTurnEndTranscript)
	copilotSeedActiveBoltPiped(t, h, 42, "sess-pipe", base.Unix()+5, true)
	h.renderSnapshot(fake.windows) // converge the drifted title 🛑→⚡
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the pipe child to mask the stop as bolt", eff, visible)
	}

	// The bolt path must resolve the INTERACTIVE owner only. The interactive fact is
	// retired and only the pipe child is live, so the probe bails without reading —
	// the parent-session memo is preserved intact.
	titlesBefore := len(fake.titleCalls())
	h.ProbeCopilotTranscriptStop(base)
	if got := len(fake.titleCalls()); got != titlesBefore {
		t.Fatalf("SetTabTitle calls = %d, want %d (pipe child must not re-settle)", got, titlesBefore)
	}
	assertProbeMemo("sess-interactive")

	// The pipe child's contribution disappears (its `copilot -p` invocation ended and
	// its live fact is retired), and the window re-reduces. The probe stop becomes
	// the sole visible contribution again, so the tab reverts to 🛑.
	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-pipe"}].acknowledge(base.Unix())
	st.ShadowSeq++
	st.Reducer.Compact(base.Unix())
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(base.Unix()))
	h.markWindowDirtyLocked(42)
	h.stateMu.Unlock()
	h.renderSnapshot(fake.windows) // converge the drifted title ⚡→🛑
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after the pipe child disappears", eff)
	}

	// A post-stat active append lands on the ORIGINAL interactive session's
	// transcript. Because the memo was never overwritten, the next probe observation
	// reads that Active evidence for sess-interactive and recovers — reinstating the
	// interactive parent's ⚡ with no hook or prompt.
	appendCopilotTranscript(t, home, "sess-interactive", `{"type":"assistant.turn_start","data":{"turnId":"7"}}`+"\n")
	h.ProbeCopilotTranscriptStop(base)

	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the interactive parent's bolt reinstated", eff, visible)
	}
	h.stateMu.Lock()
	interactive := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-interactive"}]
	h.stateMu.Unlock()
	if interactive == nil || interactive.acked {
		t.Fatalf("interactive parent not reinstated by recovery: %+v", interactive)
	}
}

// TestProbeCopilotTranscriptStop_IgnoresNonCopilotOwner confirms the probe only
// settles Copilot-owned tabs: a Codex-managed owner with the same stuck bolt is
// left alone even if a matching transcript exists.
func TestProbeCopilotTranscriptStop_IgnoresNonCopilotOwner(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h := newTestHandler()
	h.SetCopilotTranscriptHomes([]string{home}, "")
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
	}
	h.SetKittyClient(fake)
	base := time.Now()
	// Seed a codex interactive bolt so the tab has live interactive work.
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCopilotTranscriptStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 for a non-copilot owner", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt preserved for non-copilot owner", eff)
	}
}
