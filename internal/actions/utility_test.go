package actions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// TestOpenActionsNeedNoKitty: with no Kitty to open a tab in, the four open
// actions hand the shell or editor to the surface's host, and when there is
// none (or it refuses) leave it to the caller's own terminal. None of them
// reports a missing Kitty client.
func TestOpenActionsNeedNoKitty(t *testing.T) {
	directory := t.TempDir()
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude}},
		Projects: []config.Session{{Title: "api", Directory: directory, Profile: "work"}},
	}
	project := cfg.Projects[0]
	entry := worktree.Entry{ID: "wt-1", ProjectLabel: "api", Path: directory, Exists: true}
	for _, tc := range []struct {
		id      ID
		request Request
		editor  bool
	}{
		{OpenDirectory, Request{Project: &project}, false},
		{OpenEditor, Request{Project: &project}, true},
		{WorktreeShell, Request{Worktree: &entry}, false},
		{WorktreeEditor, Request{Worktree: &entry}, true},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			base := Dependencies{Config: cfg, Backend: runtimesession.BackendTmux, WorktreeManager: &actionWorktreeManager{entries: []worktree.Entry{entry}}}

			var asked []UtilityView
			hosted := base
			hosted.Utility = func(view UtilityView) error { asked = append(asked, view); return nil }
			result, err := DefaultRegistry().Execute(context.Background(), hosted, tc.id, tc.request)
			if err != nil || !result.Changed || result.Data["continuation"] != DataContinuationWorkspace {
				t.Fatalf("hosted: %+v, %v; want the host's view", result, err)
			}
			if want := (UtilityView{Editor: tc.editor, Path: directory}); len(asked) != 1 || asked[0] != want {
				t.Fatalf("host was asked for %+v; want %+v", asked, want)
			}
			if _, here := UtilityHere(result); here {
				t.Fatal("a view the host took was also left to the caller")
			}

			refused := base
			refused.Utility = func(UtilityView) error { return errors.New("workspace_unavailable") }
			for name, deps := range map[string]Dependencies{"no host": base, "host refused": refused} {
				result, err := DefaultRegistry().Execute(context.Background(), deps, tc.id, tc.request)
				if err != nil || result.Changed {
					t.Fatalf("%s: %+v, %v; want nothing opened and no error", name, result, err)
				}
				view, here := UtilityHere(result)
				if want := (UtilityView{Editor: tc.editor, Path: directory}); !here || view != want {
					t.Fatalf("%s: left %+v (%v); want %+v for the caller's terminal", name, view, here, want)
				}
			}
		})
	}
}

// A session on tmux with no tmux server injected is missing a tmux server,
// not a Kitty client; a Kitty session is still missing Kitty.
func TestSessionActionsNameTheBackendTheyLack(t *testing.T) {
	cfg := continuityConfig(harness.Claude)
	for kind, want := range map[runtimesession.BackendKind]string{
		runtimesession.BackendTmux:  "tmux server",
		runtimesession.BackendKitty: "Kitty client",
	} {
		running := session.RunningSession{Project: "api", Profile: "work", Backend: kind, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef"}
		for _, id := range []ID{Focus, ToggleLock, ToggleMute, Close} {
			_, err := DefaultRegistry().Execute(context.Background(), Dependencies{Config: cfg}, id, Request{Session: &running, Confirmed: true})
			var actionErr *Error
			if !errors.As(err, &actionErr) || actionErr.Code != "missing_dependency" || !strings.Contains(actionErr.Message, want) {
				t.Errorf("%s on %s: %v; want a missing %s", id, kind, err, want)
			}
			if kind == runtimesession.BackendTmux && err != nil && strings.Contains(err.Error(), "Kitty") {
				t.Errorf("%s on tmux blamed Kitty: %v", id, err)
			}
		}
	}
}
