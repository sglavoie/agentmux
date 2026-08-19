package transcript

import (
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
