package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

func resetSessionCommandsTestState(t *testing.T) *tmuxtest.Fixture {
	t.Helper()
	fixture := tmuxtest.New(t)
	oldTmuxBackend := cli.NewTmuxBackend
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return fixture.Backend, nil }
	oldLoadConfig := sessionCommandsLoadConfig
	oldLoadIndex := sessionCommandsLoadSessionIndex
	oldIndexPath := sessionCommandsSessionIndexPath
	oldReaders := sessionCommandsSessionReaders
	oldDaemon := sessionCommandsDaemon
	oldLive := sessionCommandsLiveConversations
	oldNewHandoff := sessionCommandsNewHandoff
	oldNewWorktree := sessionCommandsNewWorktree
	oldIsTerminal := sessionListIsTerminal
	oldNow := sessionCommandsNow
	oldListOut := sessionListCmd.OutOrStdout()
	oldContinueOut := sessionContinueCmd.OutOrStdout()
	oldForkOut := sessionForkCmd.OutOrStdout()
	oldForkWorktreeOut := sessionForkWorktreeCmd.OutOrStdout()
	oldHandoffOut := sessionHandoffCmd.OutOrStdout()
	t.Cleanup(func() {
		cli.NewTmuxBackend = oldTmuxBackend
		sessionCommandsLoadConfig = oldLoadConfig
		sessionCommandsLoadSessionIndex = oldLoadIndex
		sessionCommandsSessionIndexPath = oldIndexPath
		sessionCommandsSessionReaders = oldReaders
		sessionCommandsDaemon = oldDaemon
		sessionCommandsLiveConversations = oldLive
		sessionCommandsNewHandoff = oldNewHandoff
		sessionCommandsNewWorktree = oldNewWorktree
		sessionListIsTerminal = oldIsTerminal
		sessionCommandsNow = oldNow
		sessionListCmd.SetOut(oldListOut)
		sessionContinueCmd.SetOut(oldContinueOut)
		sessionForkCmd.SetOut(oldForkOut)
		sessionForkWorktreeCmd.SetOut(oldForkWorktreeOut)
		sessionHandoffCmd.SetOut(oldHandoffOut)
		_ = sessionListCmd.Flags().Set("json", "false")
		_ = sessionListCmd.Flags().Set("all", "false")
		_ = sessionSearchCmd.Flags().Set("all", "false")
		_ = sessionContinueCmd.Flags().Set("json", "false")
		_ = sessionForkCmd.Flags().Set("json", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("allow-dirty", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("yes", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("json", "false")
		_ = sessionHandoffCmd.Flags().Set("profile", "")
		_ = sessionHandoffCmd.Flags().Set("edit", "false")
		_ = sessionHandoffCmd.Flags().Set("no-launch", "false")
		_ = sessionHandoffCmd.Flags().Set("preview", "false")
		_ = sessionHandoffCmd.Flags().Set("content-file", "")
		_ = sessionHandoffCmd.Flags().Set("instruction", "")
		_ = sessionHandoffCmd.Flags().Set("json", "false")
		for _, cmd := range []*cobra.Command{sessionListCmd, sessionSearchCmd} {
			if value, ok := cmd.Flags().Lookup("project").Value.(interface{ Replace([]string) error }); ok {
				_ = value.Replace(nil)
			}
		}
	})
	// Tests replace the daemon's live conversation query.
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return nil, false }
	return fixture
}

type recordingTmux struct {
	backend.TargetLauncher
	launches []backend.LaunchSpec
}

func (r *recordingTmux) LaunchTarget(spec backend.LaunchSpec) (backend.Launched, error) {
	r.launches = append(r.launches, spec)
	return r.TargetLauncher.LaunchTarget(spec)
}

type sessionCommandReader struct {
	harness     string
	discovered  []transcript.SessionMeta
	discoverErr error
	calls       *int
}

func (r sessionCommandReader) Harness() string { return r.harness }
func (r sessionCommandReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	if r.calls != nil {
		(*r.calls)++
	}
	// A real reader classifies every session it discovers; an unclassified
	// fixture would read as automated-or-unknown and leave list and search.
	discovered := make([]transcript.SessionMeta, len(r.discovered))
	for i, meta := range r.discovered {
		discovered[i] = withMainClass(meta)
	}
	return discovered, r.discoverErr
}

func automated(meta transcript.SessionMeta) transcript.SessionMeta {
	meta.SessionClass = transcript.SessionClassAutomated
	return meta
}

func withMainClass(meta transcript.SessionMeta) transcript.SessionMeta {
	if meta.SessionClass == "" {
		meta.SessionClass = transcript.SessionClassMain
	}
	return meta
}
func (sessionCommandReader) ReadMessages(string) ([]transcript.Message, error) { return nil, nil }

type sessionCommandHandoff struct {
	requests []handoff.Request
	result   handoff.Result
	err      error
}

type sessionCommandWorktree struct {
	requests  []worktree.Request
	result    worktree.Result
	removeErr error
}

func (w *sessionCommandWorktree) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	w.requests = append(w.requests, request)
	return w.result, nil
}

func (w *sessionCommandWorktree) RemoveUnused(context.Context, worktree.Result) error {
	return w.removeErr
}

func (h *sessionCommandHandoff) Run(_ context.Context, request handoff.Request) (handoff.Result, error) {
	h.requests = append(h.requests, request)
	return h.result, h.err
}

func TestSessionListRefreshesFilesystemIndexAndEmitsTextAndJSON(t *testing.T) {
	resetSessionCommandsTestState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	source := filepath.Join(dir, "fresh.jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{
		ID: "fresh", Harness: harness.Codex, CWD: dir, Path: source,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
	}
	indexPath := filepath.Join(dir, "transcripts.json")
	discoverCalls := 0
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{LaunchBackend: "tmux"},
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: dir}},
			Projects: []config.Session{
				{Title: "Primary", Directory: dir, Profile: "work"},
				{Title: "Alias", Directory: dir, Profile: "work"},
			},
		}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}

	if err := sessionListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionListCmd.SetOut(&out)
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("JSON list: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionListData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || len(payload.Data.Sessions) != 1 || payload.Data.Sessions[0].ID != meta.ID {
		t.Fatalf("payload = %+v", payload)
	}
	view := payload.Data.Sessions[0]
	if view.Project != "Primary" || strings.Join(view.Projects, ",") != "Primary,Alias" {
		t.Fatalf("session JSON projects = %q %#v, want Primary [Primary Alias]", view.Project, view.Projects)
	}
	persisted, err := transcript.LoadIndexAt(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := persisted.Get(transcript.IndexKey(meta.Harness, meta.ID)); !found || discoverCalls != 1 {
		t.Fatalf("filesystem refresh found=%t calls=%d", found, discoverCalls)
	}

	if err := sessionListCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("text list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "codex\twork\tPrimary\t") || !strings.Contains(got, "\tfresh\t\t\t1\t?\t\n") || strings.Contains(got, "Alias") {
		t.Fatalf("text list = %q", got)
	}
	assertNoRetiredDatabaseState(t, home, "session list")
}

func TestSessionContinueCLIUsesExactNativeInvocation(t *testing.T) {
	fixture := resetSessionCommandsTestState(t)
	recorder := &recordingTmux{TargetLauncher: fixture.Backend}
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return recorder, nil }
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{LaunchBackend: "tmux", Args: []string{"--model", "test"}},
			Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work", Args: []string{"--project-flag"}}},
		}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	info, err := os.Stat(meta.Path)
	if err != nil {
		t.Fatal(err)
	}
	meta.FileSize, meta.FileModTime = info.Size(), info.ModTime()
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	discoverCalls := 0
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)

	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("session continue: %v", err)
	}
	if len(recorder.launches) != 1 {
		t.Fatalf("launches = %+v, want one", recorder.launches)
	}
	opts := recorder.launches[0]
	if opts.Directory != meta.CWD || opts.Vars["amx_harness"] != harness.Codex || opts.Vars["amx_profile"] != "work" {
		t.Fatalf("launch context = %+v, want indexed source context", opts)
	}
	if got := strings.Join(opts.Argv, " "); !strings.Contains(got, "codex --project-flag -c log_dir=") || !strings.Contains(got, " resume session-1") {
		t.Fatalf("command = %q, want Codex resume argv", got)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionActionData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || payload.Data.Action != "session.continue" || payload.Data.Data["continuation"] != "resumed" {
		t.Fatalf("payload = %+v", payload)
	}
	if discoverCalls != 1 {
		t.Fatalf("conversation resolution refresh calls = %d, want 1", discoverCalls)
	}
}
