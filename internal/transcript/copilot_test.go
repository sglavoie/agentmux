package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const copilotFixtureRoot = "testdata/copilot"

func TestCopilotReader_Discover(t *testing.T) {
	r := NewCopilotReader()
	metas, err := r.Discover(copilotFixtureRoot, DiscoverOptions{})
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
	if m.SessionClass != SessionClassMain {
		t.Errorf("SessionClass = %q, want main", m.SessionClass)
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
	metas, err := r.Discover(filepath.Join(copilotFixtureRoot, "does-not-exist"), DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestCopilotReader_DiscoverEmptyHome(t *testing.T) {
	r := NewCopilotReader()
	metas, err := r.Discover("", DiscoverOptions{})
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

func TestCopilotDiscover_CacheHitSkipsParse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "session-state", "cccccccc-cccc-cccc-cccc-cccccccccccc")
	path := filepath.Join(dir, "events.jsonl")
	body := `{"type":"session.start","timestamp":"2026-05-24T13:00:01.000Z","data":{"sessionId":"on-disk-id","startTime":"2026-05-24T13:00:00.000Z","context":{"cwd":"/on-disk/cwd"}}}` + "\n"
	info := writeTranscript(t, path, body)

	cachedMeta := SessionMeta{
		ID:           "cached-id",
		Harness:      "copilot",
		Path:         path,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 11,
		Snippet:      "cached snippet",
		CWD:          "/cached/cwd",
	}

	called := 0
	r := NewCopilotReader()
	metas, err := r.Discover(root, DiscoverOptions{
		Cached: func(p string, size int64, mtime time.Time) (SessionMeta, bool) {
			called++
			if p != path {
				t.Errorf("Cached called with path %q, want %q", p, path)
			}
			if size != info.Size() {
				t.Errorf("Cached called with size %d, want %d", size, info.Size())
			}
			if !mtime.Equal(info.ModTime()) {
				t.Errorf("Cached called with mtime %v, want %v", mtime, info.ModTime())
			}
			return cachedMeta, true
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if called != 1 {
		t.Fatalf("Cached called %d times, want 1", called)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d metas, want 1", len(metas))
	}
	got := metas[0]
	if got.ID != cachedMeta.ID {
		t.Errorf("ID = %q, want %q (cache should not be overwritten)", got.ID, cachedMeta.ID)
	}
	if got.Snippet != cachedMeta.Snippet {
		t.Errorf("Snippet = %q, want %q (cache should not be overwritten)", got.Snippet, cachedMeta.Snippet)
	}
	if got.MessageCount != cachedMeta.MessageCount {
		t.Errorf("MessageCount = %d, want %d (cache should not be overwritten)", got.MessageCount, cachedMeta.MessageCount)
	}
	if got.CWD != cachedMeta.CWD {
		t.Errorf("CWD = %q, want %q (cache should not be overwritten)", got.CWD, cachedMeta.CWD)
	}
}

func TestCopilotDiscover_MinTimeSkipsOldFiles(t *testing.T) {
	root := t.TempDir()
	body := `{"type":"session.start","timestamp":"2026-05-24T13:00:01.000Z","data":{"sessionId":"sess-id","startTime":"2026-05-24T13:00:00.000Z","context":{"cwd":"/cwd"}}}` + "\n"
	newPath := filepath.Join(root, "session-state",
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "events.jsonl")
	oldPath := filepath.Join(root, "session-state",
		"eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", "events.jsonl")
	writeTranscript(t, newPath, body)
	writeTranscript(t, oldPath, body)

	stale := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	r := NewCopilotReader()
	metas, err := r.Discover(root, DiscoverOptions{
		MinTime: time.Now().Add(-7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d metas, want 1: %+v", len(metas), metas)
	}
	if metas[0].Path != newPath {
		t.Errorf("returned path = %q, want %q", metas[0].Path, newPath)
	}
}

func TestCopilotDiscover_CacheMissReparses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "session-state", "dddddddd-dddd-dddd-dddd-dddddddddddd")
	path := filepath.Join(dir, "events.jsonl")
	body := `{"type":"session.start","timestamp":"2026-05-24T13:00:01.000Z","data":{"sessionId":"fresh-id","startTime":"2026-05-24T13:00:00.000Z","context":{"cwd":"/fresh/cwd"}}}` + "\n" +
		`{"type":"user.message","timestamp":"2026-05-24T13:00:02.000Z","data":{"content":"fresh body"}}` + "\n"
	info := writeTranscript(t, path, body)

	staleCached := SessionMeta{
		ID:          "stale-id",
		Harness:     "copilot",
		Path:        path,
		FileSize:    info.Size() - 1, // deliberate mismatch
		FileModTime: info.ModTime(),
		Snippet:     "stale snippet",
	}

	r := NewCopilotReader()
	metas, err := r.Discover(root, DiscoverOptions{
		Cached: func(p string, size int64, mtime time.Time) (SessionMeta, bool) {
			if size != staleCached.FileSize {
				return SessionMeta{}, false
			}
			return staleCached, true
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d metas, want 1", len(metas))
	}
	got := metas[0]
	if got.ID == staleCached.ID {
		t.Errorf("ID should be re-parsed, got stale value %q", got.ID)
	}
	if got.ID != "fresh-id" {
		t.Errorf("ID = %q, want %q", got.ID, "fresh-id")
	}
	if got.Snippet != "fresh body" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "fresh body")
	}
	if got.FileSize != info.Size() {
		t.Errorf("FileSize = %d, want %d (re-parse should reflect live file)", got.FileSize, info.Size())
	}
}

// The tool-only assistant event carries no content and is not an ending.
func TestCopilotDiscover_EndingIsTheLastAssistantReply(t *testing.T) {
	metas, err := NewCopilotReader().Discover(copilotFixtureRoot, DiscoverOptions{})
	if err != nil || len(metas) != 1 {
		t.Fatalf("Discover = %d sessions, %v; want 1", len(metas), err)
	}
	if got := metas[0]; got.Ending != "Done. See the diff above." || got.ReaderVersion != copilotReaderVersion {
		t.Fatalf("Ending, ReaderVersion = %q, %d; want the closing reply, %d", got.Ending, got.ReaderVersion, copilotReaderVersion)
	}
}
