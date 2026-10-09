package session

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// These tests launch on a real private tmux server whose "agents" are a
// stand-in shell (see tmuxtest), so every assertion about panes, processes,
// and the registry is about real tmux state.

func tmuxTestConfig(t *testing.T) (*config.Config, config.Session) {
	t.Helper()
	project := config.Session{Title: "api", Directory: t.TempDir(), Profile: "work"}
	return &config.Config{
		Accounts: []config.Account{
			{Name: "work", Harness: harness.Claude, Color: "blue"},
			{Name: "home", Harness: harness.Codex, Color: "yellow"},
		},
		Sessions: []config.Session{project},
	}, project
}

func tmuxRuntime(f *tmuxtest.Fixture) Runtime {
	return Runtime{Tmux: f.Backend, Registry: f.Registry}
}

func runningOnTmux(t *testing.T, f *tmuxtest.Fixture, cfg *config.Config) []RunningSession {
	t.Helper()
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	return RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg)
}

func liveRecords(registry *runtimesession.Registry) []runtimesession.Record {
	var live []runtimesession.Record
	for _, record := range registry.List() {
		if record.State == runtimesession.StateLive || record.State == runtimesession.StatePending {
			live = append(live, record)
		}
	}
	return live
}

func TestLaunchOnTmuxRegistersOneLiveRuntimeSession(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	work := cfg.Accounts[0]

	first, err := LaunchOn(tmuxRuntime(f), project, work, cfg)
	if err != nil {
		t.Fatalf("LaunchOn: %v", err)
	}
	if first.Backend != runtimesession.BackendTmux || first.Deduplicated || first.RuntimeSessionID == "" {
		t.Fatalf("first launch = %+v, want a registered tmux launch", first)
	}
	record, err := f.Load(t).Get(first.RuntimeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != runtimesession.StateLive || record.Target.Ref != first.Pane.ID || record.Relation != runtimesession.RelationFresh || record.Project != "api" {
		t.Fatalf("record = %+v, want live fresh record at pane %s", record, first.Pane.ID)
	}
	running := runningOnTmux(t, f, cfg)
	if len(running) != 1 {
		t.Fatalf("running = %+v, want one tmux session", running)
	}
	rs := running[0]
	if rs.Backend != runtimesession.BackendTmux || rs.RuntimeSessionID != first.RuntimeSessionID || rs.Group != first.Group || rs.Pane != first.Pane || rs.EffectiveProfile() != "work" || rs.EffectiveProject() != "api" {
		t.Fatalf("running session = %+v, want the launch's identity", rs)
	}
	if got := f.LaunchedFor(record.Process.PID); got != string(first.RuntimeSessionID) {
		t.Fatalf("agent saw %s=%q, want %q", runtimesession.EnvVar, got, first.RuntimeSessionID)
	}

	again, err := LaunchOn(tmuxRuntime(f), project, work, cfg)
	if err != nil {
		t.Fatalf("dedup LaunchOn: %v", err)
	}
	if !again.Deduplicated || again.RuntimeSessionID != first.RuntimeSessionID {
		t.Fatalf("dedup launch = %+v, want the existing runtime session", again)
	}
	if n := len(runningOnTmux(t, f, cfg)); n != 1 {
		t.Fatalf("dedup launch left %d sessions, want 1", n)
	}

	another, err := LaunchOn(tmuxRuntime(f), project, work, cfg, LaunchPolicyAlwaysNewInstance)
	if err != nil {
		t.Fatalf("new-instance LaunchOn: %v", err)
	}
	if another.Deduplicated || another.RuntimeSessionID == first.RuntimeSessionID {
		t.Fatalf("new-instance launch = %+v, want a second runtime session", another)
	}
	if n := len(liveRecords(f.Load(t))); n != 2 {
		t.Fatalf("live records = %d, want 2", n)
	}
}

// TestLaunchOnTmuxConcurrentDedupLaunchesOnce races dedup launches of one key:
// the launch lock means exactly one agent starts.
func TestLaunchOnTmuxConcurrentDedupLaunchesOnce(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	var wg sync.WaitGroup
	results := make([]LaunchResult, 4)
	errs := make([]error, len(results))
	for i := range results {
		wg.Go(func() { results[i], errs[i] = LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg) })
	}
	wg.Wait()
	launched := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("launch %d: %v", i, err)
		}
		if !results[i].Deduplicated {
			launched++
		}
	}
	if launched != 1 || len(runningOnTmux(t, f, cfg)) != 1 || len(liveRecords(f.Load(t))) != 1 {
		t.Fatalf("launched %d, want exactly one agent and one live record", launched)
	}
}

func TestLaunchOnTmuxFailureLeavesNothingRunning(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	project.Directory = project.Directory + "/missing"
	_, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err == nil {
		t.Fatal("launch into a missing directory succeeded")
	}
	if n := len(runningOnTmux(t, f, cfg)); n != 0 {
		t.Fatalf("failed launch left %d sessions", n)
	}
	records := f.Load(t).List()
	if len(records) != 1 || records[0].State != runtimesession.StateEnded {
		t.Fatalf("records = %+v, want one ended registration", records)
	}
}

func TestLaunchOnTmuxUncommittableLaunchIsUndone(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	previous := processStartToken
	processStartToken = func(int) (string, error) { return "", errors.New("ps unavailable") }
	t.Cleanup(func() { processStartToken = previous })

	_, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err == nil || !strings.Contains(err.Error(), "committing runtime session") {
		t.Fatalf("err = %v, want a commit failure", err)
	}
	if n := len(runningOnTmux(t, f, cfg)); n != 0 {
		t.Fatalf("uncommitted launch left %d agents running", n)
	}
	if live := liveRecords(f.Load(t)); len(live) != 0 {
		t.Fatalf("uncommitted launch left registrations %+v", live)
	}
}

func TestLaunchContinuityOnTmuxRefusesSecondResume(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	work := cfg.Accounts[0]
	resumed, err := LaunchContinuityOn(tmuxRuntime(f), project, work, cfg, harness.InvocationResume, "conv-1", "")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	record, err := f.Load(t).Get(resumed.RuntimeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Conversation == nil || record.Conversation.SessionID != "conv-1" || record.Conversation.Harness != harness.Claude {
		t.Fatalf("resumed record = %+v, want conversation claude:conv-1", record)
	}
	if _, err := LaunchContinuityOn(tmuxRuntime(f), project, work, cfg, harness.InvocationResume, "conv-1", ""); !errors.Is(err, runtimesession.ErrConversationInUse) {
		t.Fatalf("second resume err = %v, want ErrConversationInUse", err)
	}
	forked, err := LaunchContinuityOn(tmuxRuntime(f), project, work, cfg, harness.InvocationFork, "conv-1", "")
	if err != nil {
		t.Fatalf("fork of a live conversation: %v", err)
	}
	if forked.RuntimeSessionID == resumed.RuntimeSessionID {
		t.Fatal("fork reused the source runtime session")
	}
	if n := len(runningOnTmux(t, f, cfg)); n != 2 {
		t.Fatalf("running = %d, want the resume and the fork", n)
	}
}

func TestRestartOnTmuxLaunchesBeforeClosingAndEndsTheOriginal(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	original, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	running := runningOnTmux(t, f, cfg)[0]

	restarted, err := RestartOn(tmuxRuntime(f), running, cfg)
	if err != nil {
		t.Fatalf("RestartOn: %v", err)
	}
	registry := f.Load(t)
	old, _ := registry.Get(original.RuntimeSessionID)
	replacement, _ := registry.Get(restarted.RuntimeSessionID)
	if old.State != runtimesession.StateEnded {
		t.Fatalf("original = %+v, want ended", old)
	}
	if replacement.State != runtimesession.StateLive || replacement.Relation != runtimesession.RelationRestart || replacement.Parent != original.RuntimeSessionID {
		t.Fatalf("replacement = %+v, want live restart of %s", replacement, original.RuntimeSessionID)
	}
	after := runningOnTmux(t, f, cfg)
	if len(after) != 1 || after[0].RuntimeSessionID != restarted.RuntimeSessionID {
		t.Fatalf("running after restart = %+v, want only the replacement", after)
	}
}

func TestRestartWithAccountOnTmuxReplacesProfile(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	if _, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg); err != nil {
		t.Fatal(err)
	}
	running := runningOnTmux(t, f, cfg)[0]
	home := cfg.Accounts[1]

	same, err := RestartWithAccountTemplateOn(tmuxRuntime(f), running, project, cfg.Accounts[0], cfg)
	if err != nil || same.RuntimeSessionID != "" {
		t.Fatalf("same-profile replacement = %+v, %v; want a no-op", same, err)
	}
	replaced, err := RestartWithAccountTemplateOn(tmuxRuntime(f), running, project, home, cfg)
	if err != nil {
		t.Fatalf("profile replacement: %v", err)
	}
	after := runningOnTmux(t, f, cfg)
	if len(after) != 1 || after[0].EffectiveProfile() != "home" || after[0].Harness != harness.Codex || after[0].RuntimeSessionID != replaced.RuntimeSessionID {
		t.Fatalf("running after replacement = %+v, want one home/codex session", after)
	}
}

// TestRestartFollowsRecordedBackend refuses to act on a session through a
// backend that does not host it, before launching anything.
func TestRestartFollowsRecordedBackend(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, _ := tmuxTestConfig(t)
	kittySession := RunningSession{TabID: 4, WindowID: 5, Profile: "work", Project: "api", Directory: t.TempDir()}
	if _, err := RestartOn(tmuxRuntime(f), kittySession, cfg); err == nil || !strings.Contains(err.Error(), "runs on kitty") {
		t.Fatalf("err = %v, want a backend mismatch", err)
	}
	tmuxSession := RunningSession{Backend: runtimesession.BackendTmux, Group: backend.GroupID("$0"), Profile: "work", Project: "api"}
	if _, err := RestartOn(KittyRuntime(nil), tmuxSession, cfg); err == nil || !strings.Contains(err.Error(), "runs on tmux") {
		t.Fatalf("err = %v, want a backend mismatch", err)
	}
	if n := len(runningOnTmux(t, f, cfg)); n != 0 {
		t.Fatalf("refused restart launched %d sessions", n)
	}
}

// TestLaunchOnTmuxAbandonsInterruptedRegistrations recovers a pending
// registration left by a launcher that died between registering and
// committing, so it can never block or impersonate a later launch.
func TestLaunchOnTmuxAbandonsInterruptedRegistrations(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	var stuck runtimesession.Record
	if err := f.Registry.Update(func(registry *runtimesession.Registry) error {
		var err error
		stuck, err = registry.Begin(runtimesession.BeginRequest{Project: "api", LaunchID: "crashed", Relation: runtimesession.RelationFresh}, time.Now().Add(-time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg); err != nil {
		t.Fatal(err)
	}
	if record, _ := f.Load(t).Get(stuck.ID); record.State != runtimesession.StateAbandoned {
		t.Fatalf("interrupted registration = %+v, want abandoned", record)
	}
}
