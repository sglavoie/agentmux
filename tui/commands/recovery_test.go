package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestRecoverClosedResumesExactConversationAndNeverFallsBackOnMissingSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{ID: "exact-thread", Harness: "claude", Profile: "work", Project: "api", CWD: dir, Path: path}
	data, err := json.Marshal(map[string]any{"entries": map[string]transcript.SessionMeta{"claude:exact-thread": meta}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(transcript.IndexPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript.IndexPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	f := tmuxtest.New(t)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: "claude", HomeDir: dir}}}
	deps := actions.Dependencies{Tmux: f.Backend, Runtimes: f.Registry, Config: cfg, Daemon: func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }}
	identity := conversation.Identity{Harness: "claude", SessionID: "exact-thread"}
	project := config.Session{Title: "api", Directory: dir, Profile: "work"}
	if err := RecoverClosed(context.Background(), deps, project, identity, "tmux"); err != nil {
		t.Fatal(err)
	}
	panes, err := f.Backend.Discover()
	if err != nil || len(panes) != 1 {
		t.Fatalf("recovery panes = %+v, %v; want one", panes, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := RecoverClosed(context.Background(), deps, project, identity, ""); err == nil {
		t.Fatal("missing source was accepted")
	}
	panes, err = f.Backend.Discover()
	if err != nil || len(panes) != 1 {
		t.Fatal("failed resume fell back to a new conversation")
	}
}
