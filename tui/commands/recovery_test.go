package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type recoveryKitty struct {
	kitty.KittyClient
	launched []kitty.LaunchOpts
}

func (c *recoveryKitty) ListWindows() ([]kitty.OSWindow, error) { return nil, nil }
func (c *recoveryKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	return nil
}

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
	client := &recoveryKitty{}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: "claude", HomeDir: dir}}}
	deps := actions.Dependencies{Kitty: client, Config: cfg, Daemon: func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }}
	identity := conversation.Identity{Harness: "claude", SessionID: "exact-thread"}
	project := config.Session{Title: "api", Directory: dir, Profile: "work"}
	if err := RecoverClosed(context.Background(), deps, project, identity, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.launched) != 1 || !strings.Contains(strings.Join(client.launched[0].Cmdline, " "), "--resume exact-thread") {
		t.Fatalf("recovery did not resume: %+v", client.launched)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := RecoverClosed(context.Background(), deps, project, identity, ""); err == nil {
		t.Fatal("missing source was accepted")
	}
	if len(client.launched) != 1 {
		t.Fatal("failed resume fell back to a new conversation")
	}
}
