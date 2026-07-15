package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// copilotSpanBeforeTailTranscript models a subagent.started scrolled off the top
// of the bounded 256 KiB tail: more than a tail's worth of subagent output
// separates it from the nested assistant.turn_end that follows. Without span
// reconstruction the probe would read that nested turn_end as a main terminal
// and false-stop the still-running parent bolt. If openOnly is true the tail
// ends inside the open (reconstructed) span so the probe must preserve the bolt;
// otherwise a genuine main assistant.turn_end follows the span's close and the
// probe must settle stop.
func copilotSpanBeforeTailTranscript(openOnly bool) string {
	// A single record larger than the tail window guarantees the leading
	// subagent.started falls outside the last 256 KiB the reader inspects.
	huge := strings.Repeat("x", 256<<10)
	records := []string{
		`{"type":"subagent.started","data":{"agentName":"task"}}`,
		`{"type":"tool.execution_complete","data":{"result":"` + huge + `"}}`,
		`{"type":"assistant.turn_end","data":{"turnId":"6"}}`,
		`{"type":"subagent.completed","data":{"agentName":"task"}}`,
	}
	if !openOnly {
		records = append(records,
			`{"type":"assistant.turn_start","data":{"turnId":"0"}}`,
			`{"type":"assistant.turn_end","data":{"turnId":"0"}}`,
		)
	}
	return strings.Join(records, "\n") + "\n"
}

// copilotOpenSpanBeyondWindowTranscript models the finding's worst case at the
// daemon boundary: a subagent.started whose opening record scrolled off the top
// of the reader's whole bounded window (a huge in-span record separates it from
// the nested assistant.turn_end), with NO subagent.completed and no main-session
// anchor visible anywhere in the window. The span remains open, so the reader
// fails closed and the probe must preserve the parent bolt (record no
// TranscriptStopProbe). The huge record is comfortably larger than the reader's
// 512 KiB tail+anchor window so both the anchor and the subagent.started fall off
// the top.
func copilotOpenSpanBeyondWindowTranscript() string {
	huge := strings.Repeat("x", 768<<10)
	records := []string{
		`{"type":"user.message","data":{"content":"delegate"}}`,   // anchor scrolled off the window
		`{"type":"subagent.started","data":{"agentName":"task"}}`, // scrolled off the window
		`{"type":"tool.execution_complete","data":{"result":"` + huge + `"}}`,
		`{"type":"assistant.turn_end","data":{"turnId":"6"}}`, // nested subagent turn, span still open
	}
	return strings.Join(records, "\n") + "\n"
}

const (
	copilotTurnEndTranscript   = `{"type":"session.start","data":{}}` + "\n" + `{"type":"assistant.turn_start","data":{}}` + "\n" + `{"type":"assistant.turn_end","data":{}}` + "\n"
	copilotAbortTranscript     = `{"type":"assistant.turn_start","data":{}}` + "\n" + `{"type":"abort","data":{"reason":"user_initiated"}}` + "\n"
	copilotActiveTranscript    = `{"type":"assistant.turn_end","data":{}}` + "\n" + `{"type":"user.message","data":{}}` + "\n" + `{"type":"tool.execution_start","data":{}}` + "\n"
	copilotSubagentTranscript  = `{"type":"session.start","data":{}}` + "\n" + `{"type":"session.task_complete","data":{}}` + "\n"
	copilotMalformedTranscript = `{"type":"assistant.turn_end","data":{}}` + "\n" + `{"type":"assistant.turn_` + "\n"
	// copilotOpenSubagentTranscript ends with an assistant.turn_end while a
	// subagent.started span is still open (no subagent.completed). That turn_end
	// belongs to the nested subagent, so the probe must NOT settle the parent
	// interactive bolt to a false stop.
	copilotOpenSubagentTranscript = `{"type":"assistant.turn_start","data":{"turnId":"5"}}` + "\n" +
		`{"type":"subagent.started","data":{"agentName":"task"}}` + "\n" +
		`{"type":"assistant.turn_end","data":{"turnId":"5"}}` + "\n" +
		`{"type":"assistant.turn_end","data":{"turnId":"6"}}` + "\n"
)

// writeCopilotTranscript writes an events.jsonl fixture at the Copilot
// transcript path (<home>/session-state/<session>/events.jsonl) the resolver
// builds, so the probe exercises ResolveCopilotTranscript + ReadCopilotLifecycle
// end to end.
func writeCopilotTranscript(t *testing.T, home, session, content string) {
	t.Helper()
	dir := filepath.Join(home, "session-state", session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// appendCopilotTranscript appends raw content to an existing Copilot events.jsonl
// fixture, modeling a live write that grows the transcript (a new turn's
// assistant.turn_start) after an earlier read.
func appendCopilotTranscript(t *testing.T, home, session, content string) {
	t.Helper()
	path := filepath.Join(home, "session-state", session, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript for append: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
}

// copilotTranscriptSize returns the on-disk byte size of a Copilot events.jsonl
// fixture — the generation fingerprint the probe captures at read time.
func copilotTranscriptSize(t *testing.T, home, session string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(home, "session-state", session, "events.jsonl"))
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	return info.Size()
}

// copilotSeedActiveBolt folds a real session-scoped Copilot interactive
// UserPromptSubmit fact into a window so its stuck ⚡ is backed by a live
// interactive reducer fact — the reducer precedence a synthetic transcript stop
// must defeat. It mirrors codexSeedActiveBolt/claudeSeedActiveBolt and clears
// the seed's dirty mark so the probe's own render is the only title write.
func copilotSeedActiveBolt(t *testing.T, h *Handler, windowID int, session string, at int64) uint64 {
	t.Helper()
	return copilotSeedActiveBoltPiped(t, h, windowID, session, at, false)
}

func copilotSeedActiveBoltPiped(t *testing.T, h *Handler, windowID int, session string, at int64, pipeMode bool) uint64 {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "copilot:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "userPromptSubmitted",
		PipeMode:  pipeMode,
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "userPromptSubmitted", PipeMode: pipeMode, normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed copilot active %q rejected: %+v", session, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}

// settleCopilotTurnEndProbe drives the transcript stop probe the two stable
// observations a normal assistant.turn_end completion now requires: the first
// observation only credits a stabilization sample, and the second — reading the
// same terminal at the unchanged transcript generation — settles. A genuinely
// final turn_end stops growing, so both reads agree; an intermediate turn_end
// grows the transcript with a new assistant.turn_start before the second read,
// which restarts stabilization. Whole-session terminals (abort/shutdown) settle
// in one observation and do not use this helper.
func settleCopilotTurnEndProbe(h *Handler, now time.Time) {
	h.ProbeCopilotTranscriptStop(now)
	h.ProbeCopilotTranscriptStop(now)
}

// copilotProbeHandlerNotifier mirrors copilotProbeHandler but wires a recording
// notifier with stop notifications enabled, so a test can assert whether the
// transcript stop probe actually dispatched a "Session stopped" notification.
func copilotProbeHandlerNotifier(t *testing.T, windowID int, iconState, home string) (*Handler, *fakeKittyClient, *callRecorder) {
	t.Helper()
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(guard, notifier)
	h.SetCopilotTranscriptHomes([]string{home}, "")
	fake := &fakeKittyClient{
		windows: codexProbeWindows(windowID, palette.BuildTitle("W:api", iconState, false), kitty.UserVars{
			Profile: "work",
			Harness: "copilot",
		}),
	}
	h.SetKittyClient(fake)
	return h, fake, rec
}

func copilotProbeHandler(t *testing.T, windowID int, iconState string, home string) (*Handler, *fakeKittyClient) {
	t.Helper()
	h := newTestHandler()
	h.SetCopilotTranscriptHomes([]string{home}, "")
	fake := &fakeKittyClient{
		windows: codexProbeWindows(windowID, palette.BuildTitle("W:api", iconState, false), kitty.UserVars{
			Profile: "work",
			Harness: "copilot",
		}),
	}
	h.SetKittyClient(fake)
	return h, fake
}

// TestProbeCopilotTranscriptStop_CompletedTurnStopsStuckBolt drives the
// native-hook-loss recovery through the authoritative-idle path: a terminal
// (assistant.turn_end) transcript settles the stale interactive bolt, converges
// the tab to 🛑, and records a TranscriptStopProbe row carrying the ACTUAL
// reduced state under the copilot_transcript probe source.
func TestProbeCopilotTranscriptStop_CompletedTurnStopsStuckBolt(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	settleCopilotTurnEndProbe(h, base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}

	// The stale interactive bolt fact was retired, so it can no longer mask 🛑.
	h.stateMu.Lock()
	old := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-1"}]
	h.stateMu.Unlock()
	if old == nil || !old.acked {
		t.Fatalf("stale interactive bolt was not retired: %+v", old)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", copilotTranscriptStopProbeEvent, resp.Events)
	}
	if ev.Action != "stop" {
		t.Fatalf("TranscriptStopProbe action = %q, want stop", ev.Action)
	}
	if ev.Source != string(SourceProbe)+":"+copilotTranscriptStopProbeSource {
		t.Fatalf("TranscriptStopProbe source = %q, want %q", ev.Source, string(SourceProbe)+":"+copilotTranscriptStopProbeSource)
	}
}

// TestProbeCopilotTranscriptStop_UserAbortStopsStuckBolt is the interruption
// twin: a user-initiated abort record is terminal evidence and settles the same
// way as a clean completion.
func TestProbeCopilotTranscriptStop_UserAbortStopsStuckBolt(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotAbortTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCopilotTranscriptStop(base)

	if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "stop", false) {
		t.Fatalf("SetTabTitle calls = %+v, want one stop title", calls)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after user abort", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
		t.Fatalf("TranscriptStopProbe row = %+v ok=%v, want stop", ev, ok)
	}
}

// TestProbeCopilotTranscriptStop_NonTerminalEvidenceKeepsBolt walks the
// fail-closed evidence shapes the probe must never settle: ongoing activity, a
// non-state-bearing tail (subagent task_complete only), a missing transcript,
// and a malformed tail. Each leaves the stuck ⚡ untouched and records no
// TranscriptStopProbe row.
func TestProbeCopilotTranscriptStop_NonTerminalEvidenceKeepsBolt(t *testing.T) {
	cases := []struct {
		name    string
		content string
		write   bool
	}{
		{name: "active later turn", content: copilotActiveTranscript, write: true},
		{name: "open subagent span turn_end", content: copilotOpenSubagentTranscript, write: true},
		{name: "no state-bearing event", content: copilotSubagentTranscript, write: true},
		{name: "malformed tail", content: copilotMalformedTranscript, write: true},
		{name: "missing transcript", write: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.write {
				writeCopilotTranscript(t, home, "sess-1", tc.content)
			}
			h, fake := copilotProbeHandler(t, 42, "bolt", home)
			base := time.Now()
			copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

			h.ProbeCopilotTranscriptStop(base)

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("effective = %+v, want bolt preserved", eff)
			}
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
				t.Fatalf("TranscriptStopProbe row recorded for %s: %+v", tc.name, ev)
			}
		})
	}
}

// TestProbeCopilotTranscriptStop_SubagentSpanBeganOutsideTail proves the
// tail-boundary repair end to end. When the opening subagent.started scrolled
// off the top of the 256 KiB tail, the probe must NOT settle the parent bolt on
// the nested assistant.turn_end (the span is reconstructed from the unmatched
// subagent.completed). Once a genuine main assistant.turn_end follows the
// reconstructed span's close, the probe settles stop as usual.
func TestProbeCopilotTranscriptStop_SubagentSpanBeganOutsideTail(t *testing.T) {
	t.Run("open span keeps bolt", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotSpanBeforeTailTranscript(true))
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		h.ProbeCopilotTranscriptStop(base)

		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0 (nested turn_end inside off-tail span)", got)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt preserved", eff)
		}
		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
			t.Fatalf("TranscriptStopProbe row recorded for an open off-tail span: %+v", ev)
		}
	})

	t.Run("still-open span beyond window keeps bolt", func(t *testing.T) {
		// The span never closes and its opening subagent.started scrolled off the
		// top of the reader's whole window with no visible anchor, so the reader
		// fails closed. The probe must NOT settle the parent bolt on the nested
		// assistant.turn_end and must record no TranscriptStopProbe.
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotOpenSpanBeyondWindowTranscript())
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		h.ProbeCopilotTranscriptStop(base)

		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0 (still-open span beyond the window)", got)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt preserved", eff)
		}
		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
			t.Fatalf("TranscriptStopProbe row recorded for a still-open off-window span: %+v", ev)
		}
	})

	t.Run("main terminal after span closes settles stop", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotSpanBeforeTailTranscript(false))
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		settleCopilotTurnEndProbe(h, base)

		if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "stop", false) {
			t.Fatalf("SetTabTitle calls = %+v, want one stop title", calls)
		}
		if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
			t.Fatalf("effective = %+v visible %q, want stop/stop after span closes", eff, visible)
		}
		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
			t.Fatalf("TranscriptStopProbe row = %+v ok=%v, want stop", ev, ok)
		}
	})
}

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

// TestProbeCopilotTranscriptStop_ConcurrentPromptInvalidatesStopProbe proves the
// captured-sequence guard: a fresh prompt landing during the (slow) transcript
// read advances the reducer receipt past the value the probe captured, so the
// stale terminal read is discarded and the newer interactive bolt survives. The
// injected lifecycle reader models the concurrent prompt landing mid-read.
func TestProbeCopilotTranscriptStop_ConcurrentPromptInvalidatesStopProbe(t *testing.T) {
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
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	interactive := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "copilot:sess-1"}]
	probe := h.store[42].Reducer.facts[SourceID{Kind: SourceProbe, Key: copilotTranscriptStopProbeSource}]
	h.stateMu.Unlock()
	if interactive == nil || interactive.acked {
		t.Fatalf("interactive fact not reinstated after recovery: %+v", interactive)
	}
	// The withdrawn probe stop is an acked tombstone; Compact may already have
	// pruned it. Either way it must not survive as a live terminal.
	if probe != nil && !probe.acked {
		t.Fatalf("probe stop fact not withdrawn after recovery: %+v", probe)
	}

	// A provenance row records the ACTUAL reduced result (bolt), never a stop.
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	home := t.TempDir()
	// An active transcript: recovery WOULD fire if the stop were probe-owned.
	writeCopilotTranscript(t, home, "sess-1", copilotActiveTranscript)
	h, fake := copilotProbeHandler(t, 42, "stop", home)
	base := time.Now()

	// A native agentStop terminal fact owns the 🛑 — not the transcript probe.
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "copilot:sess-1"},
		Action:    "stop",
		SessionID: "sess-1",
		At:        base.Unix(),
		Event:     "agentStop",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "agentStop", normalized: &ev})
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
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, _ := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// A live ASL turn lease: it contributes bolt + a loop lease that masks stop.
	aslEv := NormalizedEvent{
		Source: SourceID{Kind: SourceASL, Key: "run-1"},
		Phase:  ASLPhaseTurn,
		RunID:  "run-1",
		At:     base.Unix(),
		Event:  agentSessionLoopTurnStarted,
	}
	h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "loop_marker", Event: agentSessionLoopTurnStarted, normalized: &aslEv})

	settleCopilotTurnEndProbe(h, base)

	eff, visible := windowEffective(t, h, 42)
	if visible == "stop" || eff.Icon == "stop" {
		t.Fatalf("effective = %+v visible %q, want a masked (non-stop) state under a live ASL lease", eff, visible)
	}
	if !eff.Looping {
		t.Fatalf("effective = %+v, want the ASL loop lease preserved", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

// TestProbeCopilotTranscriptStop_StableCompletionNotifiesOnce is the positive
// twin: a genuinely final assistant.turn_end (the transcript stops growing) passes
// the stabilization policy AND the post-settle ConfirmNotify re-read still reads
// terminal, so exactly one "Session stopped" notification dispatches. This guards
// the suppression above from silently swallowing genuine completions.
func TestProbeCopilotTranscriptStop_StableCompletionNotifiesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, _, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		settleCopilotTurnEndProbe(h, base)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective = %+v, want stop for a genuinely stable completion", eff)
		}
		calls := rec.snapshot()
		if len(calls) != 1 {
			t.Fatalf("stop notifications = %d, want exactly 1 for a stable completion", len(calls))
		}
		if calls[0].subtitle != "Session stopped" {
			t.Fatalf("notification subtitle = %q, want %q", calls[0].subtitle, "Session stopped")
		}
	})
}
