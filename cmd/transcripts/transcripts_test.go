package transcripts

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/searchstore"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func resetTranscriptsCommandTestState(t *testing.T, indexPath string) {
	t.Helper()
	oldIndexPath := transcriptsIndexPath
	oldLoadConfig := transcriptsLoadConfig
	oldReaders := transcriptsReaders
	oldRebuildAll := transcriptsRebuildAll
	oldIsTerminal := transcriptsIsTerminal
	oldStatusOut := transcriptsStatusCmd.OutOrStdout()
	oldCompactOut := transcriptsCompactCmd.OutOrStdout()
	oldRebuildOut := transcriptsRebuildCmd.OutOrStdout()

	transcriptsIndexPath = func() string { return indexPath }
	t.Cleanup(func() {
		transcriptsIndexPath = oldIndexPath
		transcriptsLoadConfig = oldLoadConfig
		transcriptsReaders = oldReaders
		transcriptsRebuildAll = oldRebuildAll
		transcriptsIsTerminal = oldIsTerminal
		transcriptsStatusCmd.SetOut(oldStatusOut)
		transcriptsCompactCmd.SetOut(oldCompactOut)
		transcriptsRebuildCmd.SetOut(oldRebuildOut)
	})
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
	emptyKey := transcript.IndexKey(harness.Claude, "s2")
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","profile":"work","profiles":["work","personal"],"message_count":1},` +
		`"` + emptyKey + `":{"id":"s2","harness":"claude","profile":"work","message_count":0,"snippet":"/clear","empty":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := searchstore.New(path).Write("claude:orphan", "junk"); err != nil {
		t.Fatalf("Write orphan sidecar: %v", err)
	}

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"metadata_path: " + path,
		"visible_entries: 2",
		"hidden_entries: 0",
		"empty_entries: 1",
		"visible_profile_memberships: 3",
		"shared_profile_entries: 1",
		"missing_sidecars: 1",
		"orphan_sidecars: 1",
		"orphan_sidecar_size: 4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestTranscriptsStatusCommandMalformedMetadataPrintsQuarantineWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	writeMalformedTranscriptCommandMetadata(t, path)

	var out bytes.Buffer
	transcriptsStatusCmd.SetOut(&out)
	if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	requireTranscriptCommandQuarantineWarning(t, out.String())
}

func TestTranscriptsCompactCommandPrintsSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "legacy")
	body := `{"entries":{"` + key + `":{"id":"legacy","harness":"claude","message_count":1,"search_text":"body"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := searchstore.New(path).Write("claude:orphan", "unused"); err != nil {
		t.Fatalf("Write orphan sidecar: %v", err)
	}

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "compacted transcript index") ||
		!strings.Contains(got, "legacy_search_text_entries: 1") ||
		!strings.Contains(got, "sidecar_count: 1") ||
		!strings.Contains(got, "orphan_sidecars_removed: 1") ||
		!strings.Contains(got, "orphan_sidecar_size_removed: 6") {
		t.Fatalf("compact output did not include expected summary:\n%s", got)
	}
}

func TestTranscriptsCompactCommandMalformedMetadataPrintsQuarantineWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	writeMalformedTranscriptCommandMetadata(t, path)

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "compacted transcript index") {
		t.Fatalf("compact output missing success summary:\n%s", out.String())
	}
	requireTranscriptCommandQuarantineWarning(t, out.String())
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
		"rebuilt transcript sidecars",
		"metadata_size: ",
		"sidecar_dir: ",
		"sidecar_size: ",
		"visible_entries: 1",
		"visible_profile_memberships: 2",
		"shared_profile_entries: 1",
		"sidecar_count: 1",
		"orphan_sidecars: 0",
		"orphan_sidecar_size: 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rebuild output missing %q:\n%s", want, got)
		}
	}
}

func TestTranscriptsRebuildCommandMalformedMetadataPrintsQuarantineWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	writeMalformedTranscriptCommandMetadata(t, path)
	transcriptsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	transcriptsReaders = func() map[string]transcript.Reader { return map[string]transcript.Reader{} }

	var out bytes.Buffer
	transcriptsRebuildCmd.SetOut(&out)
	if err := transcriptsRebuildCmd.RunE(transcriptsRebuildCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "rebuilt transcript sidecars") {
		t.Fatalf("rebuild output missing success summary:\n%s", out.String())
	}
	requireTranscriptCommandQuarantineWarning(t, out.String())
}

func writeMalformedTranscriptCommandMetadata(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatalf("WriteFile malformed metadata: %v", err)
	}
}

func requireTranscriptCommandQuarantineWarning(t *testing.T, output string) {
	t.Helper()
	if !strings.Contains(output, "warning: transcript cache quarantined as") {
		t.Fatalf("command output missing quarantine warning:\n%s", output)
	}
}

func TestTranscriptsStatusReadsAsSizesOnATerminalAndBytesInAPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	body := `{"entries":{}}` + strings.Repeat(" ", 2048)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := func(terminal bool) string {
		t.Helper()
		transcriptsIsTerminal = func(io.Writer) bool { return terminal }
		var out bytes.Buffer
		transcriptsStatusCmd.SetOut(&out)
		if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
		return out.String()
	}
	piped := run(false)
	if !strings.Contains(piped, fmt.Sprintf("metadata_size: %d\n", len(body))) || strings.Contains(piped, "Past Sessions") {
		t.Fatalf("piped status must keep raw bytes and no annotations:\n%s", piped)
	}
	shown := run(true)
	for _, want := range []string{
		"index:    " + cli.DisplayPath(path) + " (2.0 KiB)\n",
		"sessions: 0 conversations in Past Sessions\n",
		"profiles: each conversation belongs to one profile\n",
		"orphaned: none\n",
		"missing:  none\n",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("terminal status missing %q:\n%s", want, shown)
		}
	}
	for _, field := range []string{"metadata_path", "visible_entries", "hidden_entries", "sidecar_count"} {
		if strings.Contains(shown, field) {
			t.Fatalf("terminal status still prints the field name %s:\n%s", field, shown)
		}
	}
}

// A nonzero problem count names the command that clears it on a terminal, and
// stays a bare key/value line in a pipe.
func TestTranscriptsStatusNamesTheRepairForEachProblemOnATerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "s1")
	body := `{"entries":{"` + key + `":{"id":"s1","harness":"claude","profile":"work","message_count":1}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := searchstore.New(path).Write("claude:orphan", "junk"); err != nil {
		t.Fatalf("Write orphan sidecar: %v", err)
	}
	run := func(terminal bool) string {
		t.Helper()
		transcriptsIsTerminal = func(io.Writer) bool { return terminal }
		var out bytes.Buffer
		transcriptsStatusCmd.SetOut(&out)
		if err := transcriptsStatusCmd.RunE(transcriptsStatusCmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
		return out.String()
	}
	if piped := run(false); strings.Contains(piped, "amx transcripts") {
		t.Fatalf("piped status must not carry repair hints:\n%s", piped)
	}
	shown := run(true)
	for _, want := range []string{
		"orphaned: 1 sidecar (4 B) belongs to no conversation\n",
		"missing:  1 conversation without search text\n",
		"1 conversation has no search text, so search cannot find it; `amx transcripts rebuild --all` reindexes it.",
		"1 search sidecar no longer belongs to a conversation; `amx transcripts compact` removes it.",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("terminal status missing %q:\n%s", want, shown)
		}
	}
}

func TestTranscriptsCompactSummarizesInWordsOnATerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.json")
	resetTranscriptsCommandTestState(t, path)
	key := transcript.IndexKey(harness.Claude, "legacy")
	body := `{"entries":{"` + key + `":{"id":"legacy","harness":"claude","message_count":1,"search_text":"body"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := searchstore.New(path).Write("claude:orphan", "unused"); err != nil {
		t.Fatalf("Write orphan sidecar: %v", err)
	}
	transcriptsIsTerminal = func(io.Writer) bool { return true }

	var out bytes.Buffer
	transcriptsCompactCmd.SetOut(&out)
	if err := transcriptsCompactCmd.RunE(transcriptsCompactCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Compacted the transcript index.\n",
		"legacy:   moved the legacy search text of 1 conversation into sidecars\n",
		"orphaned: removed 1 orphan sidecar (6 B)\n",
		"search:   1 sidecar remains\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("terminal compact missing %q:\n%s", want, got)
		}
	}
}
