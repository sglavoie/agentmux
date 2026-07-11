package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func resetTranscriptsCommandTestState(t *testing.T, indexPath string) {
	t.Helper()
	oldIndexPath := transcriptsIndexPath
	oldLoadConfig := transcriptsLoadConfig
	oldReaders := transcriptsReaders
	oldLoadIndex := transcriptsLoadIndex
	oldRebuildAll := transcriptsRebuildAll
	oldStatusOut := transcriptsStatusCmd.OutOrStdout()
	oldCompactOut := transcriptsCompactCmd.OutOrStdout()
	oldRebuildOut := transcriptsRebuildCmd.OutOrStdout()

	transcriptsIndexPath = func() string { return indexPath }
	t.Cleanup(func() {
		transcriptsIndexPath = oldIndexPath
		transcriptsLoadConfig = oldLoadConfig
		transcriptsReaders = oldReaders
		transcriptsLoadIndex = oldLoadIndex
		transcriptsRebuildAll = oldRebuildAll
		transcriptsStatusCmd.SetOut(oldStatusOut)
		transcriptsCompactCmd.SetOut(oldCompactOut)
		transcriptsRebuildCmd.SetOut(oldRebuildOut)
	})
	store := datastore.OpenTest(t)
	transcriptsLoadIndex = func(path string, readers map[string]transcript.Reader) (*transcript.Index, error) {
		idx, err := transcript.LoadIndexAt(path)
		if err != nil {
			return nil, err
		}
		return transcript.EnableSQLiteIndex(context.Background(), idx, store, readers), nil
	}
}

type fakeTranscriptReader struct {
	harness  string
	discover func(homeDir string, opts transcript.DiscoverOptions) ([]transcript.SessionMeta, error)
}

func (f fakeTranscriptReader) Harness() string { return f.harness }

func (f fakeTranscriptReader) Discover(homeDir string, opts transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return f.discover(homeDir, opts)
}

func (f fakeTranscriptReader) ReadMessages(string) ([]transcript.Message, error) {
	return nil, nil
}

func TestTranscriptsStatusCommandPrintsStoreStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "s1")
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","profile":"work","profiles":["work","personal"],"message_count":1}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"metadata_path: " + path,
		"visible_entries: 1",
		"hidden_entries: 0",
		"visible_profile_memberships: 2",
		"shared_profile_entries: 1",
		"missing_sidecars: 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestTranscriptsCompactCommandPrintsSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "legacy")
	body := `{"entries":{"` + key + `":{"id":"legacy","harness":"claude","message_count":1,"search_text":"body"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "verified SQLite transcript import") ||
		!strings.Contains(got, "legacy_source_found: true") ||
		!strings.Contains(got, "record_count: 1") ||
		!strings.Contains(got, "search_document_count: 1") {
		t.Fatalf("compact output did not include expected summary:\n%s", got)
	}
}

func TestTranscriptsRebuildCommandPrintsProfileMembershipSummary(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "transcripts.json")
	home := filepath.Join(tmp, "home")
	transcriptPath := filepath.Join(home, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile transcript: %v", err)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatalf("Stat transcript: %v", err)
	}
	resetTranscriptsCommandTestState(t, path)
	transcriptsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Profiles: []config.Account{
				{Name: "work", Harness: harness.Claude, HomeDir: home},
				{Name: "personal", Harness: harness.Claude, HomeDir: home},
			},
		}, nil
	}
	transcriptsReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{
			harness.Claude: fakeTranscriptReader{
				harness: harness.Claude,
				discover: func(homeDir string, _ transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
					if homeDir != home {
						t.Fatalf("Discover homeDir = %q, want %q", homeDir, home)
					}
					return []transcript.SessionMeta{{
						ID: "shared", Harness: harness.Claude, Path: transcriptPath,
						FileSize: info.Size(), FileModTime: info.ModTime(),
						LastAt:       time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC),
						MessageCount: 1, SearchText: "body",
					}}, nil
				},
			},
		}
	}

	var out bytes.Buffer
	transcriptsRebuildCmd.SetOut(&out)
	if err := transcriptsRebuildCmd.RunE(transcriptsRebuildCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"rebuilt SQLite transcript search documents",
		"visible_entries: 1",
		"visible_profile_memberships: 2",
		"shared_profile_entries: 1",
		"search_document_count: 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rebuild output missing %q:\n%s", want, got)
		}
	}
}
