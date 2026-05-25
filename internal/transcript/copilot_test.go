package transcript

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const copilotFixtureRoot = "testdata/copilot"

func TestCopilotReader_Discover(t *testing.T) {
	r := NewCopilotReader()
	metas, err := r.Discover(copilotFixtureRoot)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// The empty-session directory (no events.jsonl) must be skipped.
	if len(metas) != 1 {
		t.Fatalf("got %d sessions, want 1", len(metas))
	}

	m := metas[0]
	if m.ID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("ID = %q", m.ID)
	}
	if m.Harness != "copilot" {
		t.Errorf("Harness = %q", m.Harness)
	}
	if m.CWD != "/Users/dev/projects/foo" {
		t.Errorf("CWD = %q", m.CWD)
	}
	// 2 user.message + 2 assistant.message with non-empty content.
	// The tool-only assistant.message and tool events are excluded.
	if m.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4", m.MessageCount)
	}
	if m.Snippet != "Refactor the auth flow to support magic links." {
		t.Errorf("Snippet = %q", m.Snippet)
	}
	// session.start data.startTime overrides envelope timestamp.
	wantStart, _ := time.Parse(time.RFC3339Nano, "2026-05-24T13:00:00.000Z")
	if !m.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", m.StartedAt, wantStart)
	}
	// session.task_complete envelope timestamp is the last successfully
	// parsed envelope (the truncated shutdown tail line fails parsing).
	wantLast, _ := time.Parse(time.RFC3339Nano, "2026-05-24T13:00:08.000Z")
	if !m.LastAt.Equal(wantLast) {
		t.Errorf("LastAt = %v, want %v", m.LastAt, wantLast)
	}
	if m.FileSize == 0 {
		t.Errorf("FileSize must be > 0")
	}
	wantPath := filepath.Join(copilotFixtureRoot, "session-state",
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "events.jsonl")
	if m.Path != wantPath {
		t.Errorf("Path = %q, want %q", m.Path, wantPath)
	}
}

func TestCopilotReader_DiscoverMissingDir(t *testing.T) {
	r := NewCopilotReader()
	metas, err := r.Discover(filepath.Join(copilotFixtureRoot, "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestCopilotReader_DiscoverEmptyHome(t *testing.T) {
	r := NewCopilotReader()
	metas, err := r.Discover("")
	if err != nil {
		t.Fatalf("Discover empty homeDir: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for empty homeDir, got %#v", metas)
	}
}

func TestCopilotReader_ReadMessages(t *testing.T) {
	r := NewCopilotReader()
	path := filepath.Join(copilotFixtureRoot, "session-state",
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "events.jsonl")
	msgs, err := r.ReadMessages(path)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	// Expected order: user, tool(start), tool(complete), assistant, user, assistant.
	// The empty-content assistant.message (tool-only turn) is omitted.
	if len(msgs) != 6 {
		t.Fatalf("got %d messages, want 6: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "Refactor the auth flow to support magic links." {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].Text != "[tool_use: read_file]" {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || !strings.Contains(msgs[2].Text, "package auth") {
		t.Errorf("msg[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "assistant" || msgs[3].Text != "Here is the plan." {
		t.Errorf("msg[3] = %+v", msgs[3])
	}
	if msgs[4].Role != "user" || msgs[4].Text != "Continue please." {
		t.Errorf("msg[4] = %+v", msgs[4])
	}
	if msgs[5].Role != "assistant" || msgs[5].Text != "Done. See the diff above." {
		t.Errorf("msg[5] = %+v", msgs[5])
	}
}
