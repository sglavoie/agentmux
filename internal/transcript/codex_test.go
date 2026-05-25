package transcript

import (
	"path/filepath"
	"testing"
	"time"
)

const codexFixtureRoot = "testdata/codex"

func TestCodexReader_Discover(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover(codexFixtureRoot)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d sessions, want 1", len(metas))
	}

	m := metas[0]
	if m.ID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("ID = %q (expected payload.id, not filename stem)", m.ID)
	}
	if m.Harness != "codex" {
		t.Errorf("Harness = %q", m.Harness)
	}
	if m.CWD != "/Users/dev/projects/foo" {
		t.Errorf("CWD = %q", m.CWD)
	}
	// 2 event_msg/user_message + 2 response_item/message/assistant.
	// Developer + env_context user records and tool calls are excluded.
	if m.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4", m.MessageCount)
	}
	if m.Snippet != "Refactor the auth flow to support magic links." {
		t.Errorf("Snippet = %q", m.Snippet)
	}
	// session_meta.payload.timestamp overrides the envelope timestamp.
	wantStart, _ := time.Parse(time.RFC3339Nano, "2026-05-24T13:00:00.000Z")
	if !m.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", m.StartedAt, wantStart)
	}
	// Final task_completed envelope timestamp is the last successfully
	// parsed envelope (the truncated tail line fails json.Unmarshal).
	wantLast, _ := time.Parse(time.RFC3339Nano, "2026-05-24T13:00:08.000Z")
	if !m.LastAt.Equal(wantLast) {
		t.Errorf("LastAt = %v, want %v", m.LastAt, wantLast)
	}
	if m.FileSize == 0 {
		t.Errorf("FileSize must be > 0")
	}
	wantPath := filepath.Join(codexFixtureRoot, "sessions", "2026", "05", "24",
		"rollout-2026-05-24T09-00-00-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl")
	if m.Path != wantPath {
		t.Errorf("Path = %q, want %q", m.Path, wantPath)
	}
}

func TestCodexReader_DiscoverMissingDir(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover(filepath.Join(codexFixtureRoot, "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestCodexReader_DiscoverEmptyHome(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover("")
	if err != nil {
		t.Fatalf("Discover empty homeDir: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for empty homeDir, got %#v", metas)
	}
}

func TestCodexReader_ReadMessages(t *testing.T) {
	r := NewCodexReader()
	path := filepath.Join(codexFixtureRoot, "sessions", "2026", "05", "24",
		"rollout-2026-05-24T09-00-00-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl")
	msgs, err := r.ReadMessages(path)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	// Expected: user, assistant, tool(call), tool(output), user, assistant.
	if len(msgs) != 6 {
		t.Fatalf("got %d messages, want 6: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "Refactor the auth flow to support magic links." {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Text != "Sure, here is the plan." {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].Text != "[tool_use: read_file]" {
		t.Errorf("msg[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].Text == "" {
		t.Errorf("msg[3] = %+v", msgs[3])
	}
	if msgs[4].Role != "user" || msgs[4].Text != "Continue please." {
		t.Errorf("msg[4] = %+v", msgs[4])
	}
	if msgs[5].Role != "assistant" || msgs[5].Text != "Done. See the diff above." {
		t.Errorf("msg[5] = %+v", msgs[5])
	}
}

func TestCodexIDFromFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{
			"rollout-2026-05-24T09-00-00-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl",
			"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{"rollout-shortname.jsonl", "shortname"},
		{"weird.jsonl", "weird"},
	}
	for _, c := range cases {
		if got := codexIDFromFilename(c.in); got != c.want {
			t.Errorf("codexIDFromFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
