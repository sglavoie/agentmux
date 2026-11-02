package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

func fixtureThreadStore(t *testing.T) conversation.ThreadStore {
	t.Helper()
	store := conversation.ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
	old := sessionThreadStore
	sessionThreadStore = func() (conversation.ThreadStore, error) { return store, nil }
	t.Cleanup(func() { sessionThreadStore = old })
	a := conversation.Identity{Harness: "claude", SessionID: "same"}
	b := conversation.Identity{Harness: "codex", SessionID: "same"}
	c := conversation.Identity{Harness: "copilot", SessionID: "last"}
	for i, pair := range [][2]conversation.Identity{{a, b}, {b, c}} {
		key := string(rune('a' + i))
		if err := store.Record(conversation.Link{ID: key, Source: pair[0], TargetKey: key, Kind: "handoff", CreatedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
		if err := store.Observe(map[string]conversation.Identity{key: pair[1]}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestThreadCLIReadsEntireChainFromEitherClosedConversationWithoutDaemonOrTranscripts(t *testing.T) {
	store := fixtureThreadStore(t)
	// Open the on-disk state as another CLI process would after the app exits.
	sessionThreadStore = func() (conversation.ThreadStore, error) { return conversation.ThreadStore{Path: store.Path}, nil }
	for _, cmd := range []*cobra.Command{sessionThreadCmd, sessionThreadsCmd} {
		oldOut := cmd.OutOrStdout()
		t.Cleanup(func() { cmd.SetOut(oldOut); _ = cmd.Flags().Set("json", "false") })
		_ = cmd.Flags().Set("json", "true")
	}
	for _, selector := range []string{"claude:same", "codex:same", "copilot:last"} {
		var out bytes.Buffer
		sessionThreadCmd.SetOut(&out)
		if err := sessionThreadCmd.RunE(sessionThreadCmd, []string{selector}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			SchemaVersion int           `json:"schema_version"`
			Data          threadContext `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.SchemaVersion != 1 || len(envelope.Data.Links) != 2 {
			t.Fatalf("lost chain from %s: %s", selector, out.String())
		}
	}
	var out bytes.Buffer
	sessionThreadsCmd.SetOut(&out)
	if err := sessionThreadsCmd.RunE(sessionThreadsCmd, nil); err != nil || !strings.Contains(out.String(), "thread_links") {
		t.Fatalf("all threads: %s %v", out.String(), err)
	}
}

func TestThreadContextBringsOlderLinkedConversationsIntoBriefing(t *testing.T) {
	fixtureThreadStore(t)
	metas := []transcript.SessionMeta{{ID: "same", Harness: "claude"}, {ID: "same", Harness: "codex"}, {ID: "last", Harness: "copilot"}, {ID: "unrelated", Harness: "codex"}}
	context := readThreadContext(sessionViews(metas[1:2]), metas)
	if context.Warning != "" || len(context.Links) != 2 || len(context.Related) != 2 {
		t.Fatalf("incomplete thread context: %+v", context)
	}
	if context.Related[0].Harness != "claude" || context.Related[1].Harness != "copilot" {
		t.Fatal("wrong related conversations")
	}
}

func TestBriefingIncludesThreadsOfLiveConversationsOutsideRecentWindow(t *testing.T) {
	fixtureThreadStore(t)
	metas := []transcript.SessionMeta{{ID: "same", Harness: "claude"}, {ID: "same", Harness: "codex"}, {ID: "last", Harness: "copilot"}, {ID: "newest-unrelated", Harness: "codex"}}
	context := readThreadContext(sessionViews(metas[3:]), metas, conversation.Identity{Harness: "claude", SessionID: "same"})
	if len(context.Links) != 2 || len(context.Related) != 3 {
		t.Fatalf("lost live thread outside recent window: %+v", context)
	}
}

func TestThreadReadFailureReportsWarningWithoutHidingConversations(t *testing.T) {
	store := fixtureThreadStore(t)
	if err := os.WriteFile(store.Path, []byte("unreadable history"), 0o600); err != nil {
		t.Fatal(err)
	}
	context := readThreadContext([]sessionView{{ID: "same", Harness: "claude"}}, nil)
	if context.Warning == "" {
		t.Fatal("read failure presented as no links")
	}
	old := sessionThreadsCmd.OutOrStdout()
	defer sessionThreadsCmd.SetOut(old)
	var out bytes.Buffer
	sessionThreadsCmd.SetOut(&out)
	if err := sessionThreadsCmd.RunE(sessionThreadsCmd, nil); err == nil {
		t.Fatal("CLI hid corrupt history")
	}
	data, _ := os.ReadFile(store.Path)
	if string(data) != "unreadable history" {
		t.Fatal("CLI replaced history")
	}
}
