package session

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/activityfeed"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestActivityCommandReadsDurableJournalWithoutDaemon(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(filepath.Join(root, "agent-mux")), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e := diagnostics.NewEnvelope("daemon.decision")
	e.ObservedAt = time.Now()
	e.Outcome = "applied"
	e.Harness = "claude"
	e.SessionID = "conversation"
	e.Evidence = map[string]string{"event": "Stop", "replay.before_icon": "bolt", "replay.after_icon": "stop"}
	if err := journal.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	cmd := newActivityCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Version int                 `json:"schema_version"`
		Data    activityfeed.Report `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Version != 1 || len(reply.Data.Entries) != 1 || reply.Data.Entries[0].SessionID != "conversation" {
		t.Fatalf("reply: %+v", reply)
	}
}

func TestActivityCommandRejectsUnboundedReads(t *testing.T) {
	for _, args := range [][]string{{"--days", "0"}, {"--days", "91"}, {"--limit", "0"}, {"--limit", "1001"}} {
		cmd := newActivityCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
