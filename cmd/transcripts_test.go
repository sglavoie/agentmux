package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/searchstore"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func resetTranscriptsCommandTestState(t *testing.T, indexPath string) *datastore.Store {
	t.Helper()
	oldIndexPath := transcriptsIndexPath
	oldLoadConfig := transcriptsLoadConfig
	oldReaders := transcriptsReaders
	oldOpenStore := transcriptsOpenStore
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
		transcriptsOpenStore = oldOpenStore
		transcriptsLoadIndex = oldLoadIndex
		transcriptsRebuildAll = oldRebuildAll
		transcriptsStatusCmd.SetOut(oldStatusOut)
		transcriptsCompactCmd.SetOut(oldCompactOut)
		transcriptsRebuildCmd.SetOut(oldRebuildOut)
	})
	store := datastore.OpenTest(t)
	transcriptsOpenStore = func(context.Context) (*datastore.Store, error) { return store, nil }
	transcriptsLoadIndex = func(ctx context.Context, path string, borrowed *datastore.Store, readers map[string]transcript.Reader, mode compatimport.SyncMode) (*transcript.Index, error) {
		return transcript.LoadSQLiteIndexWithStore(ctx, path, borrowed, readers, mode)
	}
	return store
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
	store := resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "s1")
	repo, err := transcript.NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(context.Background(), transcript.SessionMeta{
		ID: "sqlite-only", Harness: harness.Claude, Profile: "work", Profiles: []string{"work", "personal"}, MessageCount: 1,
	}, "durable body"); err != nil {
		t.Fatal(err)
	}
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","profile":"rollback","message_count":1}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// A directory at the sidecar-body path is a filesystem read spy: any
	// compatibility sync attempt would reject it before status could succeed.
	if err := os.MkdirAll(searchstore.New(path).Path(key), 0o700); err != nil {
		t.Fatal(err)
	}
	before := transcriptStatusMutationSnapshot(t, store)
	dbPath := store.Path()
	openCalls := 0
	transcriptsOpenStore = func(ctx context.Context) (*datastore.Store, error) {
		openCalls++
		return datastore.OpenPath(ctx, dbPath)
	}

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("second RunE: %v", err)
	}
	if openCalls != 2 {
		t.Fatalf("status datastore opens = %d, want exactly one per invocation", openCalls)
	}
	got := out.String()
	for _, want := range []string{
		"metadata_path: " + path,
		"visible_entries: 1",
		"hidden_entries: 0",
		"visible_profile_memberships: 2",
		"shared_profile_entries: 1",
		"search_document_count: 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
	afterStore, err := datastore.OpenPath(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = afterStore.Close() }()
	after := transcriptStatusMutationSnapshot(t, afterStore)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("status mutated imports/entities:\n before=%v\n after=%v", before, after)
	}
}

type statusMutationSnapshot struct {
	EntityCounts []int64
	Imports      []string
}

func transcriptStatusMutationSnapshot(t *testing.T, store *datastore.Store) statusMutationSnapshot {
	t.Helper()
	queries := []string{
		`SELECT count(*) FROM import_records`,
		`SELECT count(*) FROM legacy_import_entries`,
		`SELECT count(*) FROM conversations`,
		`SELECT count(*) FROM profile_memberships`,
		`SELECT count(*) FROM search_documents`,
	}
	values := make([]int64, len(queries))
	for i, query := range queries {
		if err := store.DB().QueryRow(query).Scan(&values[i]); err != nil {
			t.Fatalf("snapshot query %q: %v", query, err)
		}
	}
	rows, err := store.DB().Query(`
		SELECT source_name, source_path, fingerprint, completed_at_ns, record_count
		FROM import_records ORDER BY source_name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	imports := make([]string, 0)
	for rows.Next() {
		var sourceName, sourcePath, fingerprint string
		var completedAt, recordCount int64
		if err := rows.Scan(&sourceName, &sourcePath, &fingerprint, &completedAt, &recordCount); err != nil {
			t.Fatal(err)
		}
		imports = append(imports, fmt.Sprintf("%s|%s|%s|%d|%d", sourceName, sourcePath, fingerprint, completedAt, recordCount))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return statusMutationSnapshot{EntityCounts: values, Imports: imports}
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
