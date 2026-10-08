package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

const codexFixtureRoot = "testdata/codex"
const currentCodexFixtureRoot = "testdata/codex-current"

func TestCodexReader_Discover(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover(codexFixtureRoot, DiscoverOptions{})
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
	if m.SessionClass != SessionClassMain {
		t.Errorf("SessionClass = %q, want main", m.SessionClass)
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
	metas, err := r.Discover(filepath.Join(codexFixtureRoot, "does-not-exist"), DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestCodexReader_DiscoverEmptyHome(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover("", DiscoverOptions{})
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

func TestCodexReader_CurrentUserMessageEvents(t *testing.T) {
	r := NewCodexReader()
	metas, err := r.Discover(currentCodexFixtureRoot, DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d sessions, want 1", len(metas))
	}

	meta := metas[0]
	const wantUser = "Review which previous features should be added back\nand restore the best ones."
	if meta.ID != "01a03a5b-ea47-7ae3-b7f3-68f944d707b5" {
		t.Errorf("ID = %q", meta.ID)
	}
	if meta.Snippet != "Review which previous features should be added back and restore the best ones." {
		t.Errorf("Snippet = %q", meta.Snippet)
	}
	if meta.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want one user plus one assistant", meta.MessageCount)
	}
	if !strings.Contains(meta.SearchText, strings.ToLower(wantUser)) {
		t.Errorf("SearchText does not contain clean user text: %q", meta.SearchText)
	}
	for _, excluded := range []string{"agents injected", "environment must never", "response-only duplicate", "plan-ideal", "mockup.png", "completed item is not a user"} {
		if strings.Contains(meta.SearchText, excluded) {
			t.Errorf("SearchText contains excluded %q: %q", excluded, meta.SearchText)
		}
	}

	path := filepath.Join(currentCodexFixtureRoot, "sessions", "2026", "08", "25",
		"rollout-2026-08-25T13-18-17-01a03a5b-ea47-7ae3-b7f3-68f944d707b5.jsonl")
	msgs, err := r.ReadMessages(path)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != wantUser {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	wantUserTime, _ := time.Parse(time.RFC3339Nano, "2026-08-25T19:18:41.000Z")
	if !msgs[0].Timestamp.Equal(wantUserTime) {
		t.Errorf("msg[0].Timestamp = %v, want %v", msgs[0].Timestamp, wantUserTime)
	}
	if msgs[1].Role != "assistant" || msgs[1].Text != "I will compare the retired features with the current design." {
		t.Errorf("msg[1] = %+v", msgs[1])
	}
}

func TestCleanCodexUserMessageRecognizesEmptyTurns(t *testing.T) {
	text, ok := cleanCodexUserMessage(codexEventMsgPayload{
		Type: "item_completed",
		Item: &codexCompletedItem{Type: "UserMessage"},
	})
	if !ok || text != "" {
		t.Fatalf("cleanCodexUserMessage empty turn = %q, %t; want empty, true", text, ok)
	}
	if text, ok := cleanCodexUserMessage(codexEventMsgPayload{
		Type: "item_completed",
		Item: &codexCompletedItem{Type: "AgentMessage", Content: []codexContentBlock{{Type: "text", Text: "ignored"}}},
	}); ok || text != "" {
		t.Fatalf("cleanCodexUserMessage unrelated item = %q, %t; want empty, false", text, ok)
	}
}

func TestCodexDiscover_CacheHitSkipsParse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "05", "24")
	path := filepath.Join(dir, "rollout-2026-05-24T09-00-00-cccccccc-cccc-cccc-cccc-cccccccccccc.jsonl")
	body := `{"timestamp":"2026-05-24T13:00:01.000Z","type":"session_meta","payload":{"id":"on-disk-id","timestamp":"2026-05-24T13:00:00.000Z","cwd":"/on-disk/cwd"}}` + "\n"
	info := writeTranscript(t, path, body)

	cachedMeta := SessionMeta{
		ID:           "cached-id",
		Harness:      "codex",
		Path:         path,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 9,
		Snippet:      "cached snippet",
		CWD:          "/cached/cwd",
	}

	called := 0
	r := NewCodexReader()
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

func TestCodexDiscover_MinTimeSkipsOldFiles(t *testing.T) {
	root := t.TempDir()
	body := `{"timestamp":"2026-05-24T13:00:01.000Z","type":"session_meta","payload":{"id":"sess-id","timestamp":"2026-05-24T13:00:00.000Z","cwd":"/cwd"}}` + "\n"
	newPath := filepath.Join(root, "sessions", "2026", "05", "24",
		"rollout-2026-05-24T09-00-00-bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.jsonl")
	oldPath := filepath.Join(root, "sessions", "2026", "04", "01",
		"rollout-2026-04-01T09-00-00-eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee.jsonl")
	writeTranscript(t, newPath, body)
	writeTranscript(t, oldPath, body)

	stale := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	r := NewCodexReader()
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

func TestCodexDiscover_CacheMissReparses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "05", "24")
	path := filepath.Join(dir, "rollout-2026-05-24T09-00-00-dddddddd-dddd-dddd-dddd-dddddddddddd.jsonl")
	body := `{"timestamp":"2026-05-24T13:00:01.000Z","type":"session_meta","payload":{"id":"fresh-id","timestamp":"2026-05-24T13:00:00.000Z","cwd":"/fresh/cwd"}}` + "\n" +
		`{"timestamp":"2026-05-24T13:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"fresh body"}}` + "\n"
	info := writeTranscript(t, path, body)

	staleCached := SessionMeta{
		ID:          "stale-id",
		Harness:     "codex",
		Path:        path,
		FileSize:    info.Size() - 1, // deliberate mismatch
		FileModTime: info.ModTime(),
		Snippet:     "stale snippet",
	}

	r := NewCodexReader()
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

// copyCodexFixture copies the Codex fixture home into a temp directory so a
// test can add a session index beside it.
func copyCodexFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(codexFixtureRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codexFixtureRoot, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCodexDiscover_TitleComesFromTheLatestThreadName(t *testing.T) {
	root := copyCodexFixture(t)
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	index := `{"id":"` + id + `","thread_name":"Magic links","updated_at":"2026-05-24T13:00:00Z"}
not json
{"id":"other","thread_name":"Unrelated","updated_at":"2026-05-24T13:00:01Z"}
{"id":"` + id + `","thread_name":"Magic link auth","updated_at":"2026-05-24T13:00:02Z"}
`
	if err := os.WriteFile(filepath.Join(root, codexSessionIndex), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	metas, err := NewCodexReader().Discover(root, DiscoverOptions{})
	if err != nil || len(metas) != 1 {
		t.Fatalf("Discover = %d sessions, %v; want 1", len(metas), err)
	}
	if metas[0].Title != "Magic link auth" || metas[0].Snippet != "Refactor the auth flow to support magic links." {
		t.Fatalf("Title, Snippet = %q, %q; want the latest thread name beside the opening prompt", metas[0].Title, metas[0].Snippet)
	}

	// A rename touches only the index, so a cached entry still takes the
	// current name rather than the one it was cached with.
	cached := metas[0]
	cached.Title = "Magic links"
	metas, err = NewCodexReader().Discover(root, DiscoverOptions{Cached: func(string, int64, time.Time) (SessionMeta, bool) { return cached, true }})
	if err != nil || len(metas) != 1 || metas[0].Title != "Magic link auth" {
		t.Fatalf("cached Discover = %+v, %v; want the current thread name", metas, err)
	}
}

func TestCodexDiscover_NoSessionIndexLeavesTitlesEmpty(t *testing.T) {
	metas, err := NewCodexReader().Discover(codexFixtureRoot, DiscoverOptions{})
	if err != nil || len(metas) != 1 || metas[0].Title != "" {
		t.Fatalf("Discover = %+v, %v; want one untitled session", metas, err)
	}
}

// Codex's paginated history continues a conversation in a second rollout,
// rollout-<ts>-<id>_<segment>.jsonl, whose session_meta repeats the original
// ID. Discovery keeps the later segment only, and repeated refreshes leave the
// entry and its search sidecar where they are instead of trading them between
// the two files.
func TestCodexContinuationSegmentIsOneConversation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "10", "04")
	const id = "01a108ac-75f4-7dd0-bf85-6f583dfa0983"
	meta := func(ts string) string {
		return `{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"` + id + `","timestamp":"` + ts + `","cwd":"/cwd"}}` + "\n"
	}
	user := func(ts, text string) string {
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"user_message","message":"` + text + `","images":[]}}` + "\n"
	}
	original := filepath.Join(dir, "rollout-2026-10-04T14-48-02-"+id+".jsonl")
	continued := filepath.Join(dir, "rollout-2026-10-04T14-48-08-"+id+"_01a108ac-8e06-7361-92ac-87b005c3254c.jsonl")
	writeTranscript(t, original, meta("2026-10-04T20:48:02.000Z")+user("2026-10-04T20:48:03.000Z", "aborted start"))
	writeTranscript(t, continued, meta("2026-10-04T20:48:08.000Z")+user("2026-10-04T20:48:09.000Z", "the real conversation")+user("2026-10-04T20:55:00.000Z", "and its follow-up"))

	metas, err := NewCodexReader().Discover(root, DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != id || metas[0].Path != continued {
		t.Fatalf("Discover = %+v, want only the continuation segment for %s", metas, id)
	}

	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: root}}}
	readers := map[string]Reader{harness.Codex: NewCodexReader()}
	for refresh := 1; refresh <= 3; refresh++ {
		if err := idx.Refresh(cfg, readers); err != nil {
			t.Fatalf("Refresh %d: %v", refresh, err)
		}
		status, err := Status(idx.Path())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if status.VisibleEntries != 1 || status.MissingSidecars != 0 || status.SidecarCount != 1 {
			t.Fatalf("after refresh %d: visible %d sidecars %d missing %d, want 1/1/0",
				refresh, status.VisibleEntries, status.SidecarCount, status.MissingSidecars)
		}
	}
}

func TestCodexDiscover_EndingIsTheLastAssistantReply(t *testing.T) {
	metas, err := NewCodexReader().Discover(codexFixtureRoot, DiscoverOptions{})
	if err != nil || len(metas) != 1 {
		t.Fatalf("Discover = %d sessions, %v; want 1", len(metas), err)
	}
	if got := metas[0]; got.Ending != "Done. See the diff above." || got.ReaderVersion != codexReaderVersion {
		t.Fatalf("Ending, ReaderVersion = %q, %d; want the closing reply, %d", got.Ending, got.ReaderVersion, codexReaderVersion)
	}
}
