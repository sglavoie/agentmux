package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copilotEvents joins JSONL records into a newline-terminated body.
func copilotEvents(records ...string) string {
	return strings.Join(records, "\n") + "\n"
}

func TestCopilotLifecycle_FinalCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"session.start","timestamp":"2026-05-24T13:00:00.000Z","data":{"sessionId":"s","startTime":"2026-05-24T13:00:00.000Z","context":{"cwd":"/w"}}}`,
		`{"type":"user.message","timestamp":"2026-05-24T13:00:01.000Z","data":{"content":"do the thing"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:02.000Z","data":{}}`,
		`{"type":"assistant.message","timestamp":"2026-05-24T13:00:03.000Z","data":{"content":"done"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal {
		t.Errorf("State = %v, want terminal", ev.State)
	}
	if ev.EventType != "assistant.turn_end" {
		t.Errorf("EventType = %q, want assistant.turn_end", ev.EventType)
	}
}

func TestCopilotLifecycle_SessionShutdownIsTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{}}`,
		`{"type":"session.shutdown","timestamp":"2026-05-24T13:00:05.000Z","data":{}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "session.shutdown" {
		t.Errorf("got %v/%q, want terminal/session.shutdown", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_InternalTurnEndThenNewTurn(t *testing.T) {
	// A settled turn followed by a new turn's activity must classify as active:
	// the latest state-bearing event wins.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{}}`,
		`{"type":"user.message","timestamp":"2026-05-24T13:00:05.000Z","data":{"content":"and now this"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:06.000Z","data":{}}`,
		`{"type":"tool.execution_start","timestamp":"2026-05-24T13:00:07.000Z","data":{"toolName":"read_file"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleActive {
		t.Errorf("State = %v, want active", ev.State)
	}
	if ev.EventType != "tool.execution_start" {
		t.Errorf("EventType = %q, want tool.execution_start", ev.EventType)
	}
}

func TestCopilotLifecycle_ReportsTranscriptSizeGeneration(t *testing.T) {
	// The evidence carries the transcript's byte size as a monotonic generation
	// fingerprint the probe captures to detect a later append. It equals the
	// on-disk file size, and it advances when a record is appended — exactly the
	// signal the probe uses to reject an intermediate assistant.turn_end that is
	// followed by a fresh assistant.turn_start.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := copilotEvents(
		`{"type":"user.message","timestamp":"2026-05-24T13:00:01.000Z","data":{"content":"go"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:02.000Z","data":{}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:03.000Z","data":{}}`,
	)
	writeTranscript(t, path, body)

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal {
		t.Fatalf("State = %v, want terminal", ev.State)
	}
	if ev.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d (on-disk file size)", ev.Size, len(body))
	}

	// Appending the next turn's start advances the generation and flips the verdict
	// back to active: the intermediate turn_end no longer settles.
	writeTranscript(t, path, body+`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:04.000Z","data":{}}`+"\n")
	ev2, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle after append: %v", err)
	}
	if ev2.Size <= ev.Size {
		t.Errorf("Size after append = %d, want > %d", ev2.Size, ev.Size)
	}
	if ev2.State != CopilotLifecycleActive {
		t.Errorf("State after append = %v, want active", ev2.State)
	}
}

func TestCopilotLifecycle_UserAbortThenBookkeepingNoise(t *testing.T) {
	// A user-initiated abort is terminal; trailing hook and subagent
	// (session.task_complete) noise must not flip it back to active.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:02.000Z","data":{}}`,
		`{"type":"tool.execution_start","timestamp":"2026-05-24T13:00:03.000Z","data":{"toolName":"bash"}}`,
		`{"type":"abort","timestamp":"2026-05-24T13:00:04.000Z","data":{"reason":"user_initiated"}}`,
		`{"type":"session.task_complete","timestamp":"2026-05-24T13:00:05.000Z","data":{}}`,
		`{"type":"hook.execution_complete","timestamp":"2026-05-24T13:00:06.000Z","data":{"hook":"agentStop"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal {
		t.Errorf("State = %v, want terminal", ev.State)
	}
	if ev.EventType != "abort" {
		t.Errorf("EventType = %q, want abort", ev.EventType)
	}
}

func TestCopilotLifecycle_NonUserAbortIsNotTerminal(t *testing.T) {
	// An error abort is not the user-initiated terminal signal; the last
	// trustworthy state-bearing event before it wins.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"tool.execution_start","timestamp":"2026-05-24T13:00:03.000Z","data":{"toolName":"bash"}}`,
		`{"type":"abort","timestamp":"2026-05-24T13:00:04.000Z","data":{"reason":"error"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleActive || ev.EventType != "tool.execution_start" {
		t.Errorf("got %v/%q, want active/tool.execution_start", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_TaskCompleteAloneIsUnknown(t *testing.T) {
	// session.task_complete on its own (subagent completion) is not a
	// main-turn terminal signal — nothing state-bearing precedes it here.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"session.start","timestamp":"2026-05-24T13:00:00.000Z","data":{"sessionId":"s"}}`,
		`{"type":"session.task_complete","timestamp":"2026-05-24T13:00:05.000Z","data":{}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleUnknown {
		t.Errorf("State = %v, want unknown", ev.State)
	}
}

func TestCopilotLifecycle_LaterActivity(t *testing.T) {
	// A terminal turn_end followed later by a fresh user prompt is active.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{}}`,
		`{"type":"session.task_complete","timestamp":"2026-05-24T13:00:05.000Z","data":{}}`,
		`{"type":"user.message","timestamp":"2026-05-24T13:00:30.000Z","data":{"content":"one more thing"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleActive || ev.EventType != "user.message" {
		t.Errorf("got %v/%q, want active/user.message", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_SubagentTurnEndIsNotMainTerminal(t *testing.T) {
	// A subagent runs its own nested turns; each emits an assistant.turn_end
	// that is indistinguishable, per-record, from the main turn's terminal.
	// While the subagent.started span is open the tail must NOT classify those
	// nested turn_ends as a main-session terminal — otherwise the stuck bolt
	// probe would settle a still-running parent turn to a false stop.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"user.message","timestamp":"2026-05-24T13:00:00.000Z","data":{"content":"delegate"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:01.000Z","data":{"turnId":"5"}}`,
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:03.000Z","data":{"turnId":"5"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:04.000Z","data":{"turnId":"6"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:05.000Z","data":{"turnId":"6"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State == CopilotLifecycleTerminal {
		t.Errorf("State = %v, want non-terminal (subagent turn_end inside open span)", ev.State)
	}
}

func TestCopilotLifecycle_MainTerminalAfterSubagentCompletes(t *testing.T) {
	// Once the subagent.completed closes the span, a genuine main-session
	// assistant.turn_end must still settle the session to terminal.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:01.000Z","data":{"turnId":"5"}}`,
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:03.000Z","data":{"turnId":"5"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{"turnId":"6"}}`,
		`{"type":"subagent.completed","timestamp":"2026-05-24T13:00:05.000Z","data":{"agentName":"task"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:06.000Z","data":{"turnId":"0"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:07.000Z","data":{"turnId":"0"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "assistant.turn_end" {
		t.Errorf("got %v/%q, want terminal/assistant.turn_end after span closes", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_UserAbortInsideSubagentSpanIsTerminal(t *testing.T) {
	// A user-initiated abort cancels the whole session, subagents included, so
	// it is terminal even while a subagent.started span is still open.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:01.000Z","data":{"turnId":"5"}}`,
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"abort","timestamp":"2026-05-24T13:00:03.000Z","data":{"reason":"user_initiated"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "abort" {
		t.Errorf("got %v/%q, want terminal/abort inside open subagent span", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_SubagentStartedBeforeTailIsNotMainTerminal(t *testing.T) {
	// A subagent span whose in-span tool output is larger than the classification
	// tail but still fits the enlarged window: the opening subagent.started is
	// visible from the file-start boundary, so span depth is counted directly and
	// the nested assistant.turn_end inside the open span must NEVER be classified
	// as the main terminal — a huge record between them must not break the count.
	huge := strings.Repeat("x", copilotLifecycleMaxTailBytes) // > the classification tail, within the window
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-05-24T13:00:03.000Z","data":{"result":"`+huge+`"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{"turnId":"6"}}`,
		`{"type":"subagent.completed","timestamp":"2026-05-24T13:00:05.000Z","data":{"agentName":"task"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State == CopilotLifecycleTerminal {
		t.Errorf("State = %v, want non-terminal (subagent.started scrolled off the tail)", ev.State)
	}
}

func TestCopilotLifecycle_MainTerminalAfterSpanBeganOutsideTail(t *testing.T) {
	// Same large-in-window span, but a genuine main assistant.turn_end follows the
	// span's close: once the span (opened from the file-start boundary) is closed
	// by its subagent.completed, the trailing main-session turn_end must still
	// settle the session to terminal.
	huge := strings.Repeat("x", copilotLifecycleMaxTailBytes) // > the classification tail, within the window
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-05-24T13:00:03.000Z","data":{"result":"`+huge+`"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{"turnId":"6"}}`,
		`{"type":"subagent.completed","timestamp":"2026-05-24T13:00:05.000Z","data":{"agentName":"task"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:06.000Z","data":{"turnId":"0"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:07.000Z","data":{"turnId":"0"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "assistant.turn_end" {
		t.Errorf("got %v/%q, want terminal/assistant.turn_end after the reconstructed span closes", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_OpenSubagentSpanOutsideTailRemainsOpenIsNotMainTerminal(t *testing.T) {
	// The worst boundary case: a subagent.started whose opening record falls off
	// the top of the whole bounded window (more than a window's worth of in-span
	// output separates it from the nested assistant.turn_end) and whose span
	// REMAINS OPEN — there is no subagent.completed and no main-session anchor
	// (user.message / session.start) visible anywhere in the window. This is an
	// open subagent span with no subagent.completed to reconstruct depth from, so
	// the reader cannot establish a trusted boundary and must fail closed: the
	// unanchored nested assistant.turn_end must NEVER be classified as the main
	// terminal.
	huge := strings.Repeat("x", copilotLifecycleMaxWindowBytes) // pushes the started off the whole window
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"user.message","timestamp":"2026-05-24T13:00:00.000Z","data":{"content":"delegate"}}`,
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:02.000Z","data":{"agentName":"task"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-05-24T13:00:03.000Z","data":{"result":"`+huge+`"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:04.000Z","data":{"turnId":"6"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State == CopilotLifecycleTerminal {
		t.Errorf("State = %v, want non-terminal (subagent.started off the tail, span still remains open)", ev.State)
	}
}

func TestCopilotLifecycle_MidFileWindowWithVisibleAnchorSettlesTerminal(t *testing.T) {
	// The window begins mid-file (a huge in-span record pushed all earlier history
	// off the top), but a user.message anchor is visible inside the window. That
	// anchor re-establishes a trustworthy zero-depth main boundary, so a following
	// main assistant.turn_end — after every visible span has closed — must still
	// settle the session to terminal even though the window is not anchored to the
	// start of the file.
	huge := strings.Repeat("x", copilotLifecycleMaxWindowBytes) // pushes earlier history off the whole window
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"subagent.started","timestamp":"2026-05-24T13:00:01.000Z","data":{"agentName":"task"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-05-24T13:00:02.000Z","data":{"result":"`+huge+`"}}`,
		`{"type":"user.message","timestamp":"2026-05-24T13:00:30.000Z","data":{"content":"one more thing"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-05-24T13:00:31.000Z","data":{"turnId":"7"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:32.000Z","data":{"turnId":"7"}}`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "assistant.turn_end" {
		t.Errorf("got %v/%q, want terminal/assistant.turn_end after a visible anchor re-establishes the boundary", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_HugeAndPartialLines(t *testing.T) {
	// A huge tool-output record larger than the classification tail (but within
	// the window) must not break parsing, and the trailing incomplete write must
	// be ignored, leaving the real terminal event as the verdict. The window still
	// reaches the file-start boundary, so the main assistant.turn_end is anchored.
	huge := strings.Repeat("x", copilotLifecycleMaxTailBytes) // > the classification tail, within the window
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"user.message","timestamp":"2026-05-24T13:00:01.000Z","data":{"content":"start"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-05-24T13:00:02.000Z","data":{"result":"`+huge+`"}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:03.000Z","data":{}}`,
	)+`{"type":"assistant.turn_st`) // incomplete in-flight final line, no newline

	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleTerminal || ev.EventType != "assistant.turn_end" {
		t.Errorf("got %v/%q, want terminal/assistant.turn_end", ev.State, ev.EventType)
	}
}

func TestCopilotLifecycle_MalformedJSONFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, copilotEvents(
		`{"type":"assistant.turn_end","timestamp":"2026-05-24T13:00:03.000Z","data":{}}`,
		`{this is not json`,
	))

	ev, err := ReadCopilotLifecycle(path)
	if !errors.Is(err, ErrCopilotLifecycleMalformed) {
		t.Fatalf("err = %v, want ErrCopilotLifecycleMalformed", err)
	}
	if ev.State != CopilotLifecycleUnknown {
		t.Errorf("State = %v, want unknown on malformed", ev.State)
	}
}

func TestCopilotLifecycle_MissingFileFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "events.jsonl")
	ev, err := ReadCopilotLifecycle(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
	if ev.State != CopilotLifecycleUnknown {
		t.Errorf("State = %v, want unknown for missing file", ev.State)
	}
}

func TestCopilotLifecycle_EmptyFileIsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writeTranscript(t, path, "")
	ev, err := ReadCopilotLifecycle(path)
	if err != nil {
		t.Fatalf("ReadCopilotLifecycle: %v", err)
	}
	if ev.State != CopilotLifecycleUnknown {
		t.Errorf("State = %v, want unknown for empty file", ev.State)
	}
}

func TestCopilotResolveTranscript_ProfileHomeMatch(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "profile-a")
	homeB := filepath.Join(root, "profile-b")
	defaultHome := filepath.Join(root, "default")

	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	// Only profile B holds the transcript.
	want := filepath.Join(homeB, "session-state", id, "events.jsonl")
	writeTranscript(t, want, copilotEvents(`{"type":"session.start","data":{"sessionId":"s"}}`))

	got, err := ResolveCopilotTranscript(id, []string{homeA, homeB}, defaultHome)
	if err != nil {
		t.Fatalf("ResolveCopilotTranscript: %v", err)
	}
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestCopilotResolveTranscript_ProfileHomeFallbackDefault(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "profile-a")
	defaultHome := filepath.Join(root, "default")

	id := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	// No profile home holds the transcript — fall back to the default home
	// path (which need not exist; the reader fails closed downstream).
	got, err := ResolveCopilotTranscript(id, []string{homeA}, defaultHome)
	if err != nil {
		t.Fatalf("ResolveCopilotTranscript: %v", err)
	}
	want := filepath.Join(defaultHome, "session-state", id, "events.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestCopilotResolveTranscript_ProfileHomePrefersFirstMatch(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "profile-a")
	homeB := filepath.Join(root, "profile-b")
	defaultHome := filepath.Join(root, "default")

	id := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	// Both homes hold it; the first configured match wins.
	wantA := filepath.Join(homeA, "session-state", id, "events.jsonl")
	writeTranscript(t, wantA, copilotEvents(`{"type":"session.start","data":{"sessionId":"s"}}`))
	writeTranscript(t, filepath.Join(homeB, "session-state", id, "events.jsonl"),
		copilotEvents(`{"type":"session.start","data":{"sessionId":"s"}}`))

	got, err := ResolveCopilotTranscript(id, []string{homeA, homeB}, defaultHome)
	if err != nil {
		t.Fatalf("ResolveCopilotTranscript: %v", err)
	}
	if got != wantA {
		t.Errorf("path = %q, want %q (first match)", got, wantA)
	}
}

func TestCopilotResolveTranscript_InvalidSessionID(t *testing.T) {
	defaultHome := t.TempDir()
	cases := []string{
		"",
		"..",
		".",
		"../escape",
		"a/b",
		`a\b`,
		"foo/../bar",
		"nested..traversal",
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			got, err := ResolveCopilotTranscript(id, nil, defaultHome)
			if !errors.Is(err, errInvalidCopilotSessionID) {
				t.Fatalf("err = %v, want errInvalidCopilotSessionID", err)
			}
			if got != "" {
				t.Errorf("path = %q, want empty on rejection", got)
			}
		})
	}
}

func TestCopilotResolveTranscript_SkipsEmptyProfileHomes(t *testing.T) {
	root := t.TempDir()
	defaultHome := filepath.Join(root, "default")
	id := "dddddddd-dddd-dddd-dddd-dddddddddddd"

	got, err := ResolveCopilotTranscript(id, []string{"", ""}, defaultHome)
	if err != nil {
		t.Fatalf("ResolveCopilotTranscript: %v", err)
	}
	want := filepath.Join(defaultHome, "session-state", id, "events.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
