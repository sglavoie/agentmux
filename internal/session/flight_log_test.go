package session

import (
	"context"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

func TestFlightLogRecordsRealLaunchOutcomesWithDiagnosticsDisabled(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	off := false
	cfg.Defaults.Diagnostics.JournalEnabled = &off
	cfg.Defaults.Diagnostics.NativeLogging = &off
	rt := tmuxRuntime(f)
	first, err := LaunchOn(rt, project, cfg.Accounts[1], cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LaunchOn(rt, project, cfg.Accounts[1], cfg)
	if err != nil || !again.Deduplicated {
		t.Fatalf("dedup: %+v %v", again, err)
	}
	bad := project
	bad.Directory += "/missing"
	if _, err := LaunchOn(rt, bad, cfg.Accounts[1], cfg, LaunchPolicyAlwaysNewInstance); err == nil {
		t.Fatal("missing-directory launch succeeded")
	}
	running := runningOnTmux(t, f, cfg)[0]
	restarted, err := RestartOn(rt, running, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchContinuityOn(rt, project, cfg.Accounts[1], cfg, harness.InvocationResume, "conversation-source", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchContinuityOn(rt, project, cfg.Accounts[1], cfg, harness.InvocationFork, "conversation-source", ""); err != nil {
		t.Fatal(err)
	}
	store, err := flightlog.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := flightlog.Build(snapshot, flightlog.Filters{Period: "all"}, time.Now().Add(time.Second), []string{"codex"})
	if r.Totals.Launches != 4 || r.Totals.Fresh != 1 || r.Totals.Restarts != 1 || r.Totals.Resumes != 1 || r.Totals.Forks != 1 {
		t.Fatalf("failed or deduplicated launch counted: %+v", r.Totals)
	}
	if len(r.Sessions) != 4 {
		t.Fatalf("continuity merged separate launches: %+v", r.Sessions)
	}
	foundOriginal, foundRestart := false, false
	for _, s := range r.Sessions {
		if s.RuntimeSessionID == string(first.RuntimeSessionID) {
			foundOriginal = true
		}
		if s.RuntimeSessionID == string(restarted.RuntimeSessionID) && s.Operation == "restart" {
			foundRestart = true
		}
		if s.Operation == "fork" && s.ConversationID != "" {
			t.Fatal("fork attributed to its source conversation")
		}
	}
	if !foundOriginal || !foundRestart {
		t.Fatalf("launch identities lost: %+v", r.Sessions)
	}
}
