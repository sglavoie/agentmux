package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// writeCompletionRegistry registers one live and one ended runtime session in
// a registry under a temporary directory and points completion at it.
func writeCompletionRegistry(t *testing.T) (live, older runtimesession.ID) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime-sessions.json")
	previous := completionRegistryPath
	completionRegistryPath = func() string { return path }
	t.Cleanup(func() { completionRegistryPath = previous })
	store, err := runtimesession.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	identity, _ := conversation.New("claude", "session-1")
	err = store.Update(func(registry *runtimesession.Registry) error {
		register := func(project, launch, ref string, at time.Time, conv *conversation.Identity) (runtimesession.ID, error) {
			record, err := registry.Begin(runtimesession.BeginRequest{Project: project, LaunchID: launch, Relation: runtimesession.RelationFresh, Conversation: conv}, at)
			if err != nil {
				return "", err
			}
			target := runtimesession.Target{Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: "/tmp/amx-tmux.sock", Generation: "gen-1"}, Ref: ref}
			if _, err := registry.Commit(record.ID, target, runtimesession.ProcessFingerprint{PID: 100, StartToken: launch}, at); err != nil {
				return "", err
			}
			return record.ID, nil
		}
		var err error
		if older, err = register("api", "launch-1", "%1", epoch, nil); err != nil {
			return err
		}
		if live, err = register("web", "launch-2", "%2", epoch.Add(time.Minute), &identity); err != nil {
			return err
		}
		ended, err := register("gone", "launch-3", "%3", epoch.Add(2*time.Minute), nil)
		if err != nil {
			return err
		}
		_, err = registry.End(ended, epoch.Add(3*time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return live, older
}

func TestCompleteRuntimeSessionsOffersTheLiveOnesNewestFirst(t *testing.T) {
	live, older := writeCompletionRegistry(t)
	got, directive := CompleteRuntimeSessions(nil, nil, "rs-")
	want := []string{string(live) + "\tweb (claude)", string(older) + "\tapi"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("completions = %q, want %q", got, want)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || directive&cobra.ShellCompDirectiveKeepOrder == 0 {
		t.Fatalf("directive = %d", directive)
	}
	if got, _ := CompleteRuntimeSessionFirstArg(nil, []string{string(live)}, ""); got != nil {
		t.Fatalf("second argument completed %q", got)
	}
}

func TestParseRuntimeSessionIDSaysWhatOneLooksLike(t *testing.T) {
	_, err := ParseRuntimeSessionID("rs-nope")
	if err == nil || strings.Contains(err.Error(), "runtimesession:") || strings.Contains(err.Error(), "malformed") ||
		!strings.Contains(err.Error(), `"rs-nope" is not a runtime session ID`) || !strings.Contains(err.Error(), "amx session list") {
		t.Fatalf("error = %v", err)
	}
	id := "rs-0123456789abcdef0123456789abcdef"
	if got, err := ParseRuntimeSessionID(" " + id + " "); err != nil || string(got) != id {
		t.Fatalf("ParseRuntimeSessionID(%q) = %q, %v", id, got, err)
	}
}

func TestCompleteRuntimeSessionsOffersNothingForAMissingRegistry(t *testing.T) {
	previous := completionRegistryPath
	completionRegistryPath = func() string { return filepath.Join(t.TempDir(), "absent.json") }
	t.Cleanup(func() { completionRegistryPath = previous })
	if got, _ := CompleteRuntimeSessions(nil, nil, ""); len(got) != 0 {
		t.Fatalf("completions = %q", got)
	}
}
