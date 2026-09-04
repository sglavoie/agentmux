package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
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

// copilotScreenCapture reads one of the committed byte-for-byte
// `kitten @ get-text --extent=screen` captures of a live GitHub Copilot CLI
// pane out of internal/screenstate/testdata.
//
// Reaching across a package boundary for testdata is deliberate and is the
// point of these tests rather than a shortcut. The Copilot transcript probe now
// refuses a settle on screen evidence, and the whole argument for that refusal
// is what a REAL Copilot pane looks like — a permission prompt Copilot's own
// transcript structurally cannot report. A screen hand-written here to look
// like Copilot would assert the daemon against this file's idea of the
// harness, which is the exact failure CLAUDE.md's capture-first rule exists to
// prevent ("all three provisional clauses were wrong in kind"). Sharing the one
// committed capture also means a re-capture for a new Copilot release moves the
// adapter's fixtures and the daemon's evidence together instead of leaving this
// package asserting bytes Copilot no longer draws.
//
// It fails rather than skips: a missing capture means the shared fixture moved,
// and silently degrading to "no screen" would turn every refusal assertion
// below vacuous.
func copilotScreenCapture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "screenstate", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared Copilot screen capture %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("shared Copilot screen capture %s is empty", path)
	}
	return string(data)
}

// copilotVetoFreeScreen is the capture of a Copilot pane whose turn has
// finished: output, the ordinary idle footer, and an empty composer box. The
// adapter raises NO veto over it (pinned in
// internal/screenstate/copilot_adapter_test.go), so it is the screen that lets
// the transcript settle a pane — which makes it the right default for every
// probe test whose subject is the transcript evidence rather than the pane.
const copilotVetoFreeScreen = "copilot_idle_completed.txt"

// copilotPermissionPromptScreen is the capture of a Copilot pane blocked on a
// tool-permission modal. It is the whole reason the screen is consulted at all:
// Copilot has written its records and stopped, so this pane's transcript tail
// reads terminal while a human is being waited on.
const copilotPermissionPromptScreen = "copilot_permission_prompt.txt"

// copilotSetScreen points a fake pane at one of the committed captures.
func copilotSetScreen(t *testing.T, fake *fakeKittyClient, sourceWindowID int, capture string) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.getText == nil {
		fake.getText = map[string]string{}
	}
	fake.getText[kitty.MatchWindowByID(sourceWindowID)] = copilotScreenCapture(t, capture)
}

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
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "copilot:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "userPromptSubmitted",
		PipeMode:  pipeMode,
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: "active", Event: "userPromptSubmitted", PipeMode: pipeMode, Normalized: &ev})
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
	guard := activity.NewStateGuard(time.Hour)
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
	copilotSetScreen(t, fake, windowID, copilotVetoFreeScreen)
	return h, fake, rec
}

// copilotProbeHandler builds a one-window Copilot probe fixture. The pane is
// seeded with the veto-free finished-turn capture, because the probe now
// consults the screen before it settles: a fake with no screen at all reads as
// an empty capture, which raises VetoComposerBusy and would silently turn every
// transcript-evidence test in this package into a screen-refusal test. A test
// whose subject IS the screen overrides it with copilotSetScreen.
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
	copilotSetScreen(t, fake, windowID, copilotVetoFreeScreen)
	return h, fake
}

// armCopilotTerminalHold drives a real Copilot hook pair — a prompt then a
// native agentStop — and asserts the stop entered the terminal hold class
// instead of painting immediately. Both hooks carry the session ID, so the held
// stop and the transcript the probe reads describe the same conversation.
func armCopilotTerminalHold(t *testing.T, h *Handler, windowID int, session string) {
	t.Helper()
	promptPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: session})
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "userPromptSubmitted", PayloadJSON: promptPayload}); !resp.OK {
		t.Fatalf("userPromptSubmitted response = %+v", resp)
	}
	stopPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: session, StopReasonCamel: "end_turn"})
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "agentStop", PayloadJSON: stopPayload}); !resp.OK {
		t.Fatalf("agentStop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, windowID)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	if eff, _ := windowEffective(t, h, windowID); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}
}
