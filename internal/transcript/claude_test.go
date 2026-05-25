package transcript

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const claudeFixtureRoot = "testdata/claude"

func TestClaudeReader_Discover(t *testing.T) {
	r := NewClaudeReader()
	metas, err := r.Discover(claudeFixtureRoot)
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
	metas, err := r.Discover(filepath.Join(claudeFixtureRoot, "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if metas != nil {
		t.Errorf("expected nil metas for missing root, got %#v", metas)
	}
}

func TestClaudeReader_DiscoverEmptyHome(t *testing.T) {
	r := NewClaudeReader()
	metas, err := r.Discover("")
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
	if msgs[2].Role != "user" || msgs[2].Text != "Continue please." {
		t.Errorf("msg[2] = %+v", msgs[2])
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

