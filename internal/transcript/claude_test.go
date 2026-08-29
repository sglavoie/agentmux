package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const claudeFixtureRoot = "testdata/claude"

func TestClaudeReader_Discover(t *testing.T) {
	r := NewClaudeReader()
	metas, err := r.Discover(claudeFixtureRoot, DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("got %d sessions, want 2", len(metas))
	}

	// Sort by ID for deterministic checks.
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID < metas[j].ID })

	foo := metas[0]
	bar := metas[1]

	if foo.ID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("foo.ID = %q", foo.ID)
	}
	if foo.Harness != "claude" {
		t.Errorf("foo.Harness = %q", foo.Harness)
	}
	if foo.SessionClass != SessionClassUnknown {
		t.Errorf("foo.SessionClass = %q, want unknown for legacy marker-free fixture", foo.SessionClass)
	}
	if foo.CWD != "/Users/dev/projects/foo" {
		t.Errorf("foo.CWD = %q", foo.CWD)
	}
	// 3 conversation records: u1 (user), u2 (assistant), u4 (user).
	// u3 is isMeta and must not count. Malformed line and truncated final
	// line are both skipped.
	if foo.MessageCount != 3 {
		t.Errorf("foo.MessageCount = %d, want 3", foo.MessageCount)
	}
	if foo.Snippet != "Refactor the auth flow to support magic links." {
		t.Errorf("foo.Snippet = %q", foo.Snippet)
	}
	wantStart, _ := time.Parse(time.RFC3339Nano, "2026-05-24T17:37:44.531Z")
	if !foo.StartedAt.Equal(wantStart) {
		t.Errorf("foo.StartedAt = %v, want %v", foo.StartedAt, wantStart)
	}
	wantLast, _ := time.Parse(time.RFC3339Nano, "2026-05-24T17:38:00.123Z")
	if !foo.LastAt.Equal(wantLast) {
		t.Errorf("foo.LastAt = %v, want %v", foo.LastAt, wantLast)
	}
	if foo.FileSize == 0 {
		t.Errorf("foo.FileSize must be > 0")
	}
	if foo.Path != filepath.Join(claudeFixtureRoot, "projects",
		"-Users-dev-projects-foo",
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl") {
		t.Errorf("foo.Path = %q", foo.Path)
	}

	// bar has no cwd field on any record -> fallback to decoded dir name.
	if bar.CWD != "/Users/dev/projects/bar" {
		t.Errorf("bar.CWD fallback = %q", bar.CWD)
	}
	if bar.MessageCount != 2 {
		t.Errorf("bar.MessageCount = %d, want 2", bar.MessageCount)
	}
	if bar.Snippet != "only file with no cwd field" {
		t.Errorf("bar.Snippet = %q", bar.Snippet)
	}
}

func TestClaudeReader_DiscoverMissingDir(t *testing.T) {
	r := NewClaudeReader()
	metas, err := r.Discover(filepath.Join(claudeFixtureRoot, "does-not-exist"), DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestClaudeReader_DiscoverEmptyHome(t *testing.T) {
	r := NewClaudeReader()
	metas, err := r.Discover("", DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover empty homeDir: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for empty homeDir, got %#v", metas)
	}
}

func TestClaudeReader_ReadMessages(t *testing.T) {
	r := NewClaudeReader()
	path := filepath.Join(claudeFixtureRoot, "projects",
		"-Users-dev-projects-foo",
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl")
	msgs, err := r.ReadMessages(path)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	// Expected: u1 user, u2 assistant (text + tool_use flattened), u4 user.
	// u3 isMeta dropped; malformed and truncated lines dropped.
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "Refactor the auth flow to support magic links." {
		t.Errorf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msg[1].Role = %q", msgs[1].Role)
	}
	if !strings.Contains(msgs[1].Text, "Sure, here is the plan.") {
		t.Errorf("msg[1].Text missing assistant text: %q", msgs[1].Text)
	}
	if !strings.Contains(msgs[1].Text, "[tool_use: read_file]") {
		t.Errorf("msg[1].Text missing tool_use marker: %q", msgs[1].Text)
	}
	handoffText, ok := msgs[1].TextForHandoff()
	if !ok || handoffText != "Sure, here is the plan." {
		t.Errorf("msg[1] handoff text = %q, %v", handoffText, ok)
	}
	if strings.Contains(handoffText, "plan steps") || strings.Contains(handoffText, "read_file") {
		t.Errorf("msg[1] handoff text leaked private/tool content: %q", handoffText)
	}
	if msgs[2].Role != "user" || msgs[2].Text != "Continue please." {
		t.Errorf("msg[2] = %+v", msgs[2])
	}
}

func TestClaudeMessageToolOnlyIsExcludedFromHandoff(t *testing.T) {
	record := claudeRecord{
		Type: "assistant",
		Message: &claudeMessage{Role: "assistant", Content: []byte(`[
			{"type":"thinking","thinking":"private"},
			{"type":"tool_use","name":"shell"}
		]`)},
	}
	message, ok := messageFromRecord(record)
	if !ok {
		t.Fatal("display message was unexpectedly dropped")
	}
	if _, ok := message.TextForHandoff(); ok {
		t.Fatalf("tool-only message was handoff-safe: %#v", message)
	}
}

func TestFlattenClaudeContent_ToolResultArray(t *testing.T) {
	raw := []byte(`[
		{"type":"text","text":"before tool"},
		{"type":"tool_result","content":[
			{"type":"text","text":"array result line one"},
			{"type":"text","text":"array result line two"}
		]},
		{"type":"tool_result","content":"string result"}
	]`)

	got := flattenClaudeContent(raw)
	want := strings.Join([]string{
		"before tool",
		"array result line one",
		"array result line two",
		"string result",
	}, "\n")
	if got != want {
		t.Fatalf("flattenClaudeContent() = %q, want %q", got, want)
	}
}

func TestClaudeDiscover_CacheHitSkipsParse(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects", "-tmp-cache-hit")
	path := filepath.Join(projDir, "cccccccc-cccc-cccc-cccc-cccccccccccc.jsonl")
	info := writeTranscript(t, path, `{"type":"user","timestamp":"2026-05-24T17:37:44.531Z","message":{"role":"user","content":"on-disk body that should not be read"}}`+"\n")

	cachedMeta := SessionMeta{
		ID:           "cached-id",
		Harness:      "claude",
		Path:         path,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 7,
		Snippet:      "cached snippet",
		CWD:          "/cached/cwd",
	}

	called := 0
	r := NewClaudeReader()
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

func TestClaudeDiscover_MinTimeSkipsOldFiles(t *testing.T) {
	root := t.TempDir()
	newPath := filepath.Join(root, "projects", "-tmp-new", "new.jsonl")
	oldPath := filepath.Join(root, "projects", "-tmp-old", "old.jsonl")
	body := `{"type":"user","timestamp":"2026-05-24T17:37:44.531Z","message":{"role":"user","content":"hi"}}` + "\n"
	writeTranscript(t, newPath, body)
	writeTranscript(t, oldPath, body)

	stale := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	r := NewClaudeReader()
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

func TestClaudeDiscover_CacheMissReparses(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "projects", "-tmp-cache-miss")
	path := filepath.Join(projDir, "dddddddd-dddd-dddd-dddd-dddddddddddd.jsonl")
	body := `{"type":"user","timestamp":"2026-05-24T17:37:44.531Z","message":{"role":"user","content":"fresh body"}}` + "\n"
	info := writeTranscript(t, path, body)

	staleCached := SessionMeta{
		ID:          "stale-id",
		Harness:     "claude",
		Path:        path,
		FileSize:    info.Size() - 1, // deliberate mismatch
		FileModTime: info.ModTime(),
		Snippet:     "stale snippet",
	}

	r := NewClaudeReader()
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
	if got.Snippet == staleCached.Snippet {
		t.Errorf("Snippet should be re-parsed, got stale value %q", got.Snippet)
	}
	if got.Snippet != "fresh body" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "fresh body")
	}
	if got.FileSize != info.Size() {
		t.Errorf("FileSize = %d, want %d (re-parse should reflect live file)", got.FileSize, info.Size())
	}
}

func TestTruncateSnippet(t *testing.T) {
	short := "hello world"
	if got := truncateSnippet(short); got != short {
		t.Errorf("short pass-through = %q", got)
	}
	multi := truncateSnippet("line1\n\n   line2\tline3")
	if multi != "line1 line2 line3" {
		t.Errorf("whitespace collapse = %q", multi)
	}
	exact := strings.Repeat("a", snippetMaxLen)
	if got := truncateSnippet(exact); got != exact {
		t.Errorf("exact length snippet = %q, want no ellipsis", got)
	}
	long := make([]byte, 0, snippetMaxLen+50)
	for i := 0; i < snippetMaxLen+50; i++ {
		long = append(long, 'a')
	}
	got := truncateSnippet(string(long))
	// Truncated form ends with the ellipsis rune.
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("long truncation missing ellipsis: %q", got)
	}
}
