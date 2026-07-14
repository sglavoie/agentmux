package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type hermeticUITestSnapshot struct {
	transcriptIndex   *transcript.Index
	transcriptReaders map[string]transcript.Reader
	transcriptResults []transcript.Result
	planIndex         *plans.Index
}

// newHermeticUITestSnapshot creates filesystem-backed transcript and plan
// fixtures beneath a private temporary directory. Nothing consults HOME or
// opens the operator's caches.
func newHermeticUITestSnapshot(parent string) (hermeticUITestSnapshot, string, error) {
	root, err := os.MkdirTemp(parent, "agentmux-uitest-")
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating fixture root: %w", err)
	}
	cleanupRoot := true
	defer func() {
		if cleanupRoot {
			_ = os.RemoveAll(root)
		}
	}()

	planRoot := filepath.Join(root, "plans")
	planDir := filepath.Join(planRoot, "fixture plan-aabb112233cc")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating fixture plan: %w", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# Fixture plan\n\nDeterministic UI harness content.\n"), 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: writing fixture plan: %w", err)
	}

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	transcriptMeta := transcript.SessionMeta{
		ID: "fixture-conversation", Harness: "claude", Profile: "work", Profiles: []string{"work"},
		Project: "api", CWD: filepath.Join(root, "projects", "api"),
		StartedAt: now.Add(-time.Hour), LastAt: now,
		Path:         filepath.Join(root, "transcripts", "fixture.jsonl"),
		MessageCount: 2, Snippet: "deterministic transcript fixture",
	}
	if err := os.MkdirAll(filepath.Dir(transcriptMeta.Path), 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating transcript source: %w", err)
	}
	if err := os.WriteFile(transcriptMeta.Path, []byte(`{"type":"user","timestamp":"2026-01-02T14:04:05Z","message":{"role":"user","content":"deterministic transcript fixture"}}`+"\n"), 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: writing transcript source: %w", err)
	}
	transcriptPath := filepath.Join(root, "data", "transcripts.json")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating transcript cache: %w", err)
	}
	cacheData, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: map[string]transcript.SessionMeta{
		transcript.IndexKey(transcriptMeta.Harness, transcriptMeta.ID): transcriptMeta,
	}})
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: encoding transcript cache: %w", err)
	}
	if err := os.WriteFile(transcriptPath, cacheData, 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: seeding transcript cache: %w", err)
	}

	readers := buildTranscriptReaders()
	transcriptIndex, err := transcript.LoadIndexAt(transcriptPath)
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching transcript fixture: %w", err)
	}
	if transcriptIndex == nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching transcript fixture: nil index")
	}
	planIndex, err := plans.LoadIndexAt(filepath.Join(root, "data", "plans.json"))
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching plan fixture: %w", err)
	}
	if planIndex == nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching plan fixture: nil index")
	}
	if err := planIndex.Refresh(planRoot); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: indexing plan fixture: %w", err)
	}

	results, searchErr := transcript.Search(transcriptIndex, readers, transcript.Query{})
	if searchErr != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: loading transcript fixture: %w", searchErr)
	}

	snapshot := hermeticUITestSnapshot{
		transcriptIndex: transcriptIndex, transcriptReaders: readers,
		planIndex: planIndex, transcriptResults: results,
	}
	cleanupRoot = false
	return snapshot, root, nil
}
