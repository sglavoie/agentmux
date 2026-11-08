package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// `session continue --here` acts on the newest interactive conversation
// started in the current directory or below it, and says which one before
// acting. Newer conversations elsewhere, and automated ones here, are not it.
func TestSessionContinueHerePicksTheNewestConversationHere(t *testing.T) {
	fixture := resetSessionCommandsTestState(t)
	recorder := &recordingTmux{TargetLauncher: fixture.Backend}
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return recorder, nil }
	oldGetwd := sessionListGetwd
	t.Cleanup(func() {
		sessionListGetwd = oldGetwd
		_ = sessionContinueCmd.Flags().Set("here", "false")
		sessionContinueCmd.SetErr(nil)
	})
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	entry := func(id, cwd string, age time.Duration, title string) transcript.SessionMeta {
		source := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		return transcript.SessionMeta{
			ID: id, Harness: harness.Codex, CWD: cwd, Path: source, Title: title, Snippet: "prompt " + id,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: now.Add(-age), LastAt: now.Add(-age),
		}
	}
	discovered := []transcript.SessionMeta{
		entry("elsewhere", filepath.Join(dir, "other"), time.Minute, "Other work"),
		automated(entry("asl-run", repo, 2*time.Minute, "")),
		entry("wanted", filepath.Join(repo, "sub"), 5*time.Minute, "Parser fix"),
		entry("older", repo, time.Hour, "Older"),
	}
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "transcripts.json") }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{LaunchBackend: "tmux"},
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: filepath.Join(dir, "codex-home")}},
			Projects: []config.Session{{Title: "repo", Directory: repo, Profile: "work"}},
		}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: discovered}}
	}
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: true}, nil }
	sessionListGetwd = func() (string, error) { return repo, nil }
	if err := sessionContinueCmd.Flags().Set("here", "true"); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	sessionContinueCmd.SetOut(&out)
	sessionContinueCmd.SetErr(&errOut)

	if err := sessionContinueCmd.RunE(sessionContinueCmd, nil); err != nil {
		t.Fatalf("continue --here: %v\n%s", err, out.String())
	}
	if len(recorder.launches) != 1 || !strings.Contains(strings.Join(recorder.launches[0].Argv, " "), " resume wanted") {
		t.Fatalf("launches = %+v, want a resume of the conversation named wanted", recorder.launches)
	}
	if got, want := errOut.String(), "Picked codex:wanted, active 5m ago: Parser fix\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	t.Run("a selector beside --here is refused", func(t *testing.T) {
		err := sessionContinueCmd.RunE(sessionContinueCmd, []string{"older"})
		if err == nil || !strings.Contains(err.Error(), "pass either --here or a selector") {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})

	t.Run("nothing here is a miss that says where it looked", func(t *testing.T) {
		sessionListGetwd = func() (string, error) { return filepath.Join(dir, "empty"), nil }
		err := sessionContinueCmd.RunE(sessionContinueCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--here: no conversation was started in") {
			t.Fatalf("err = %v, want a miss", err)
		}
	})
}

func TestAmbiguousPrefixCandidatesListTheNewestFirst(t *testing.T) {
	resetSessionCommandsTestState(t)
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	meta := func(id string, age time.Duration) transcript.SessionMeta {
		m := base
		m.ID, m.LastAt = id, now.Add(-age)
		return m
	}
	metas := []transcript.SessionMeta{meta("beef-old", 48*time.Hour), meta("beef-new", time.Hour), meta("beef-mid", 24*time.Hour), meta("beef-tie", time.Hour)}
	writeSessionCommandIndex(t, indexPath, metas...)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: metas}}

	_, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "beef"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) {
		t.Fatalf("error = %v, want an ambiguity", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got, want := strings.Join(ids, ","), "codex:beef-new,codex:beef-tie,codex:beef-mid,codex:beef-old"; got != want {
		t.Fatalf("candidates = %s, want %s", got, want)
	}
}

// fork shares continue's --here path, so it offers the same flag.
func TestSessionForkOffersHereLikeContinue(t *testing.T) {
	t.Parallel()

	for _, cmd := range []*cobra.Command{sessionContinueCmd, sessionForkCmd} {
		if cmd.Flags().Lookup("here") == nil {
			t.Errorf("session %s does not declare --here", cmd.Name())
		}
	}
}
