package handoff

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
)

type fixedGitInspector struct{ state GitState }

func (f fixedGitInspector) Inspect(context.Context, string, time.Duration) GitState { return f.state }

func TestBuilderDeterministicIncludesSourcesAndExcludesToolPayloads(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2026, 7, 11, 10, 0, 0, 0, time.FixedZone("offset", -6*60*60))
	source := Source{
		ProjectLabel: "Example", Harness: "codex", SessionID: "session-1",
		CapturedAt: baseTime, CWD: dir, TranscriptPath: transcriptPath,
		Messages: []transcript.Message{
			{Role: "user", Text: "Implement deterministic handoffs.", Timestamp: baseTime},
			{Role: "tool", Text: "secret tool payload", Timestamp: baseTime.Add(time.Minute)},
			{Role: "assistant", Text: "latest answer", Timestamp: baseTime.Add(2 * time.Minute)},
		},
	}
	builder := NewBuilderWithGit(fixedGitInspector{state: GitState{
		Available: true, Kind: "git", Branch: "main", Head: "0123456789abcdef", Status: " M file.go", DiffStat: " file.go | 1 +",
	}})
	first, err := builder.Build(context.Background(), source, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	second, err := builder.Build(context.Background(), source, Options{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if !bytes.Equal(first.Content, second.Content) || first.ContentHash != second.ContentHash {
		t.Fatal("same source snapshot produced different packs")
	}
	text := string(first.Content)
	for _, want := range []string{
		"Project: `Example`", "Implement deterministic handoffs.", "latest answer",
		transcriptPath, "0123456789abcdef", "non-user/assistant or empty message(s) excluded",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pack missing %q", want)
		}
	}
	if strings.Contains(text, "secret tool payload") {
		t.Error("tool payload leaked into pack")
	}
	for _, removed := range []string{"Workspace ID", "Conversation row", "Linked Plans"} {
		if strings.Contains(text, removed) {
			t.Errorf("pack retained removed metadata %q", removed)
		}
	}
	if len(first.Content) > DefaultMaxBytes {
		t.Fatalf("pack has %d bytes, limit %d", len(first.Content), DefaultMaxBytes)
	}
}

func TestBuilderBudgetDropsOldestRecentMessagesFirst(t *testing.T) {
	dir := t.TempDir()
	source := Source{
		ProjectLabel: "bounded", CWD: dir, TranscriptPath: filepath.Join(dir, "source.jsonl"),
		Messages: []transcript.Message{
			{Role: "user", Text: "Keep the newest context."},
			{Role: "assistant", Text: "oldest-marker " + strings.Repeat("a", 1400)},
			{Role: "assistant", Text: "middle-marker " + strings.Repeat("b", 400)},
			{Role: "user", Text: "newest-marker"},
		},
	}
	builder := NewBuilderWithGit(fixedGitInspector{state: GitState{Kind: "non-git", Detail: "working directory is not a Git worktree"}})
	pack, err := builder.Build(context.Background(), source, Options{MaxBytes: 1800, RecentMessages: 4})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pack.Content) > 1800 {
		t.Fatalf("pack has %d bytes, limit 1800", len(pack.Content))
	}
	text := string(pack.Content)
	if strings.Contains(text, "oldest-marker") {
		t.Error("oldest message survived the constrained budget")
	}
	if !strings.Contains(text, "newest-marker") {
		t.Error("newest message was dropped before older context")
	}
	if !strings.Contains(text, "oldest recent message(s) omitted to satisfy max_bytes") {
		t.Error("budget omission was not recorded")
	}
}

func TestBuilderDocumentsMissingTranscriptPathAndNonGitProject(t *testing.T) {
	dir := t.TempDir()
	source := Source{
		ProjectLabel: "Example", CWD: dir,
		Messages: []transcript.Message{{Role: "user", Text: "Continue safely."}},
	}
	builder := NewBuilderWithGit(fixedGitInspector{state: GitState{Kind: "non-git", Detail: "working directory is not a Git worktree"}})
	pack, err := builder.Build(context.Background(), source, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	text := string(pack.Content)
	for _, want := range []string{"State: unavailable", "non-git", "transcript path was not supplied"} {
		if !strings.Contains(text, want) {
			t.Errorf("pack missing %q", want)
		}
	}
}

func TestBuilderDocumentsTimedOutGitInspection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	pack, err := NewBuilder().Build(ctx, Source{
		ProjectLabel: "Example", CWD: t.TempDir(), TranscriptPath: "/tmp/source.jsonl",
		Messages: []transcript.Message{{Role: "user", Text: "Continue."}},
	}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pack.Git.Kind != "timeout" || !strings.Contains(string(pack.Content), "Git inspection timed out") {
		t.Fatalf("Git state = %#v", pack.Git)
	}
}

func TestBuilderRejectsMalformedRequiredSourceAndTinyBudget(t *testing.T) {
	builder := NewBuilderWithGit(fixedGitInspector{})
	if _, err := builder.Build(context.Background(), Source{}, Options{}); err == nil {
		t.Fatal("empty CWD was accepted")
	}
	_, err := builder.Build(context.Background(), Source{ProjectLabel: "Example", CWD: t.TempDir()}, Options{MaxBytes: MinimumMaxBytes - 1})
	if !errors.Is(err, ErrBudgetTooSmall) {
		t.Fatalf("tiny-budget error = %v", err)
	}
}
