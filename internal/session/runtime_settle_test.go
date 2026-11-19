package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// withLaunchSettle gives the launch watch a real window for one test; test
// binaries otherwise run it with none.
func withLaunchSettle(t *testing.T, window time.Duration) {
	t.Helper()
	previous, previousPoll := launchSettle, launchSettlePoll
	launchSettle, launchSettlePoll = window, 20*time.Millisecond
	t.Cleanup(func() { launchSettle, launchSettlePoll = previous, previousPoll })
}

// agentShell replaces the fixture's stand-in login shell with script.
func agentShell(t *testing.T, script string) {
	t.Helper()
	shell := filepath.Join(t.TempDir(), "agent-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
}

// A harness that refuses to start (here: the message Claude Code prints when
// its working directory is gone) exits before the launch settles. The launch
// fails with what it printed, leaves no session behind, ends its runtime
// session, and journals a fixed reason instead of success.
func TestLaunchOnTmuxReportsAnAgentThatExitsAtLaunch(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	journal := journalingConfig(t, cfg)
	withLaunchSettle(t, 3*time.Second)
	agentShell(t, `echo "The current directory no longer exists (it was deleted or moved)."; exit 1`)

	_, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if !errors.Is(err, ErrAgentExitedAtLaunch) {
		t.Fatalf("err = %v, want ErrAgentExitedAtLaunch", err)
	}
	for _, want := range []string{"exit status 1", "The current directory no longer exists", "amx doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "Pane is dead") {
		t.Fatalf("err = %q quotes tmux's own dead-pane line", err)
	}
	if errors.Is(err, ErrAgentMayBeRunning) {
		t.Fatalf("err = %v: an agent that exited is not running", err)
	}
	if panes, err := f.Backend.Discover(); err != nil || len(panes) != 0 {
		t.Fatalf("panes = %+v, %v; want the exited session closed", panes, err)
	}
	records := f.Load(t).List()
	if len(records) != 1 || records[0].State != runtimesession.StateEnded {
		t.Fatalf("records = %+v, want one ended registration", records)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"reason":"exited_at_launch"`) || strings.Contains(string(data), `"reason":"launched"`) {
		t.Fatalf("journal = %s, want exited_at_launch and no success", data)
	}
}

// A program that finishes cleanly inside the window (a print-mode run) did
// what it was launched for, so the launch is not failed.
func TestLaunchOnTmuxCleanExitAtLaunchIsNotAFailure(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	withLaunchSettle(t, time.Second)
	agentShell(t, `echo done; exit 0`)

	result, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RuntimeSessionID == "" {
		t.Fatalf("result = %+v, want a registered runtime session", result)
	}
}

// An agent that is still running when the window closes is launched.
func TestLaunchOnTmuxWaitsOutTheSettleWindowForARunningAgent(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	withLaunchSettle(t, 300*time.Millisecond)

	start := time.Now()
	result, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("launch returned after %s, before the window closed", elapsed)
	}
	if live := liveRecords(f.Load(t)); len(live) != 1 || live[0].ID != result.RuntimeSessionID {
		t.Fatalf("live = %+v, want the launched session", live)
	}
}

func TestLastOutputLinesKeepsTheTailWithoutTmuxLines(t *testing.T) {
	output := "one\n\ntwo   \nthree\nPane is dead (status 1, Wed Nov 18 21:24:13 2026)\n\n"
	if got := lastOutputLines(output, 2); got != "two\nthree" {
		t.Fatalf("lastOutputLines = %q", got)
	}
	if got := lastOutputLines("\n \n", 3); got != "" {
		t.Fatalf("lastOutputLines(blank) = %q", got)
	}
}
