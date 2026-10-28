package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestJSONLScannerReportsOversizedRecord(t *testing.T) {
	scanner := newJSONLScanner(strings.NewReader("12345678\n123456789\nlater\n"))
	scanner.maxBytes = 8
	if !scanner.Scan() || string(scanner.Bytes()) != "12345678" {
		t.Fatalf("first record = %q, %v", scanner.Bytes(), scanner.Err())
	}
	if scanner.Scan() || scanner.Err() == nil {
		t.Fatalf("oversized record was accepted: %q, %v", scanner.Bytes(), scanner.Err())
	}
}

func TestRefreshRebuildsOldIncompleteSearchCorpus(t *testing.T) {
	home := filepath.Join(t.TempDir(), "copilot-home")
	path := filepath.Join(home, "session-state", "session-id", "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session.start","data":{"sessionId":"session-id","context":{"cwd":"/tmp"}}}` + "\n" +
		`{"type":"ignored","padding":"` + strings.Repeat("x", 16<<20) + `"}` + "\n" +
		`{"type":"user.message","data":{"content":"after oversized record"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := newTestIndex(t)
	key := IndexKey(harness.Copilot, "session-id")
	idx.entries[key] = SessionMeta{
		ID: "session-id", Harness: harness.Copilot, Path: path,
		FileSize: info.Size(), FileModTime: info.ModTime(),
		SessionClass: SessionClassMain, ReaderVersion: copilotReaderVersion - 1,
	}
	idx.filesystemDirty = true
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Copilot, HomeDir: home}}}
	if err := idx.Refresh(cfg, map[string]Reader{harness.Copilot: NewCopilotReader()}); err != nil {
		t.Fatal(err)
	}
	meta, ok := idx.Get(key)
	if !ok || meta.MessageCount != 1 || meta.ReaderVersion != copilotReaderVersion {
		t.Fatalf("refreshed metadata = %+v, found=%v", meta, ok)
	}
	corpus, err := idx.ReadSearchText(key)
	if err != nil || !strings.Contains(corpus, "after oversized record") {
		t.Fatalf("refreshed search corpus missing later message: %q, %v", corpus, err)
	}
}

func TestReadersContinueAfterLargeJSONLRecord(t *testing.T) {
	largeRecord := `{"type":"ignored","padding":"` + strings.Repeat("x", 16<<20) + `"}` + "\n"
	tests := []struct {
		name   string
		prefix string
		later  string
		read   func(string) ([]Message, error)
		parse  func(string, os.FileInfo) (SessionMeta, error)
	}{
		{
			name:   "claude",
			prefix: `{"type":"user","message":{"role":"user","content":"before"}}` + "\n",
			later:  `{"type":"user","message":{"role":"user","content":"after oversized record"}}` + "\n",
			read:   NewClaudeReader().ReadMessages,
			parse: func(path string, info os.FileInfo) (SessionMeta, error) {
				return NewClaudeReader().parseFile(path, info, "")
			},
		},
		{
			name:   "codex",
			prefix: `{"type":"session_meta","payload":{"id":"session-id","cwd":"/tmp"}}` + "\n",
			later:  `{"type":"event_msg","payload":{"type":"user_message","message":"after oversized record"}}` + "\n",
			read:   NewCodexReader().ReadMessages,
			parse: func(path string, info os.FileInfo) (SessionMeta, error) {
				return NewCodexReader().parseFile(path, info)
			},
		},
		{
			name:   "copilot",
			prefix: `{"type":"session.start","data":{"sessionId":"session-id","context":{"cwd":"/tmp"}}}` + "\n",
			later:  `{"type":"user.message","data":{"content":"after oversized record"}}` + "\n",
			read:   NewCopilotReader().ReadMessages,
			parse: func(path string, info os.FileInfo) (SessionMeta, error) {
				return NewCopilotReader().parseFile(path, info, "session-id")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte(tc.prefix+largeRecord+tc.later), 0o600); err != nil {
				t.Fatal(err)
			}
			msgs, err := tc.read(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) == 0 || msgs[len(msgs)-1].Text != "after oversized record" {
				t.Fatalf("later message missing: %+v", msgs)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := tc.parse(path, info)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(meta.SearchText, "after oversized record") {
				t.Fatal("later message missing from cached search text")
			}
		})
	}
}
