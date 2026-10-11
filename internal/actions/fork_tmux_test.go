package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// forkReader is a transcript with something in it, so the portable fork's
// handoff pack is built by the real workflow.
type forkReader struct{ harness string }

func (r forkReader) Harness() string { return r.harness }
func (forkReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, nil
}
func (forkReader) ReadMessages(string) ([]transcript.Message, error) {
	return []transcript.Message{{Role: "user", Text: "Original goal"}, {Role: "assistant", Text: "Working on it"}}, nil
}

// A fork on tmux, native (the harness forks the conversation) or portable (a
// handoff pack starts a fresh one), starts a second agent and leaves the
// source alone: its runtime session, its pane and process, and its transcript
// are what they were, and it still holds its conversation.
func TestForksOnTmuxLeaveTheSourceSessionAndConversationUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, harness, continuation string
		portable                    bool
	}{
		{name: "native", harness: harness.Claude, continuation: "forked"},
		{name: "portable", harness: harness.Copilot, continuation: "portable_fork", portable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tmuxtest.New(t)
			// The portable fork writes its pack under the home directory.
			home := t.TempDir()
			t.Setenv("HOME", home)
			cfg := continuityConfig(tc.harness)
			directory := t.TempDir()
			cfg.Projects[0].Directory = directory
			cfg.Accounts[0].HomeDir = t.TempDir()
			meta := continuityMeta(t, tc.harness, "source-1", "work")
			meta.CWD = directory
			transcriptBefore, err := os.ReadFile(meta.Path)
			if err != nil {
				t.Fatal(err)
			}

			// The source: the conversation, live, in a tmux session of its own.
			rt := session.Runtime{Tmux: f.Backend, Registry: f.Registry}
			source, err := session.LaunchContinuityOn(rt, cfg.Projects[0], cfg.Accounts[0], cfg, harness.InvocationResume, meta.ID, "")
			if err != nil {
				t.Fatalf("resume the source: %v", err)
			}
			sourceBefore := recordJSON(t, f, source.RuntimeSessionID)
			panesBefore := tmuxSessions(t, f, cfg)
			if len(panesBefore) != 1 {
				t.Fatalf("running before the fork = %+v; want the source", panesBefore)
			}

			deps := tmuxDeps(f, cfg)
			deps.Readers = map[string]transcript.Reader{tc.harness: forkReader{harness: tc.harness}}
			result, err := DefaultRegistry().Execute(context.Background(), deps, Fork, Request{Conversation: &meta})
			if err != nil {
				t.Fatalf("fork: %v", err)
			}
			if result.Data["continuation"] != tc.continuation {
				t.Fatalf("continuation = %v; want %s (%+v)", result.Data["continuation"], tc.continuation, result.Data)
			}
			if tc.portable {
				path, _ := result.Data["path"].(string)
				if result.Data["fork_kind"] != "portable" || result.Data["launched"] != true || path == "" {
					t.Fatalf("portable fork data = %+v; want a launched portable fork with its pack", result.Data)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("handoff pack: %v", err)
				}
			}

			// One more agent, and it is not the source.
			running := tmuxSessions(t, f, cfg)
			if len(running) != 2 {
				t.Fatalf("running after the fork = %d; want the source and the fork", len(running))
			}
			var fork session.RunningSession
			for _, candidate := range running {
				if candidate.RuntimeSessionID != source.RuntimeSessionID {
					fork = candidate
				}
			}
			if fork.RuntimeSessionID == "" {
				t.Fatalf("the fork has no runtime session: %+v", fork)
			}
			forkRecord, err := f.Load(t).Get(fork.RuntimeSessionID)
			if err != nil || forkRecord.State != runtimesession.StateLive {
				t.Fatalf("fork record = %+v, %v; want live", forkRecord, err)
			}
			// The fork never takes the source's conversation.
			if forkRecord.Conversation != nil && forkRecord.Conversation.SessionID == meta.ID {
				t.Fatalf("the fork claimed the source conversation: %+v", forkRecord.Conversation)
			}
			if fork.Directory != directory {
				t.Fatalf("fork directory = %q; want the source's %q", fork.Directory, directory)
			}

			// The source is exactly what it was.
			if after := recordJSON(t, f, source.RuntimeSessionID); !bytes.Equal(after, sourceBefore) {
				t.Fatalf("source runtime session changed\nbefore: %s\nafter:  %s", sourceBefore, after)
			}
			var sourceAfter session.RunningSession
			for _, candidate := range running {
				if candidate.RuntimeSessionID == source.RuntimeSessionID {
					sourceAfter = candidate
				}
			}
			if sourceAfter.PaneRef() != panesBefore[0].PaneRef() || sourceAfter.GroupRef() != panesBefore[0].GroupRef() {
				t.Fatalf("source pane moved: %v -> %v", panesBefore[0].PaneRef(), sourceAfter.PaneRef())
			}
			transcriptAfter, err := os.ReadFile(meta.Path)
			if err != nil || !bytes.Equal(transcriptAfter, transcriptBefore) {
				t.Fatalf("source transcript changed (%v)", err)
			}
			// It still holds its conversation: a second resume is refused.
			if _, err := session.LaunchContinuityOn(rt, cfg.Projects[0], cfg.Accounts[0], cfg, harness.InvocationResume, meta.ID, ""); err == nil {
				t.Fatal("the source conversation could be resumed a second time after the fork")
			}
		})
	}
}

// recordJSON is one registry record as stored, for an exact before-and-after
// comparison.
func recordJSON(t *testing.T, f *tmuxtest.Fixture, id runtimesession.ID) []byte {
	t.Helper()
	record, err := f.Load(t).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
