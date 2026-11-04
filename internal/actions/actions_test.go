package actions

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

func TestDefaultRegistryHasStableDefinitions(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	got := registry.Definitions()
	want := []ID{PlanRun, OpenDirectory, OpenEditor, ClearStatus, Close, Continue, CopyTitle, Fork, ForkWorktree, Handoff, Launch, MarkActive, Restart, ToggleLock, ToggleMute, TogglePin, WorktreeDiff, WorktreeEditor, WorktreeForget, WorktreeLand, WorktreeList, WorktreeRemove, WorktreeReopen, WorktreeShell, WorktreeShow}
	if len(got) != len(want) {
		t.Fatalf("definition count = %d, want %d", len(got), len(want))
	}
	for i, definition := range got {
		if definition.ID != want[i] {
			t.Fatalf("definition %d = %q, want %q", i, definition.ID, want[i])
		}
	}
}

func TestCloseReportsConfirmationAndLockAvailability(t *testing.T) {
	t.Parallel()

	target := session.RunningSession{TabID: 42}
	registry := DefaultRegistry()

	_, err := registry.Execute(context.Background(), Dependencies{}, Close, Request{Session: &target})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "confirmation_required" {
		t.Fatalf("error = %v, want confirmation_required", err)
	}

	target.Locked = true
	_, err = registry.Execute(context.Background(), Dependencies{}, Close, Request{Session: &target, Confirmed: true})
	if !errors.As(err, &actionErr) || actionErr.Code != "unavailable" {
		t.Fatalf("locked error = %v, want unavailable", err)
	}
}

func TestLaunchDirectoryParameterIsCheckedInPlaceOfTheConfiguredOne(t *testing.T) {
	f := tmuxtest.New(t)

	missing := filepath.Join(t.TempDir(), "gone")
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: "claude"}}}
	project := config.Session{Title: "api", Directory: t.TempDir(), Profile: "work"}

	_, err := DefaultRegistry().Execute(context.Background(), tmuxDeps(f, cfg), Launch,
		Request{Project: &project, Parameters: map[string]string{"directory": missing}})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "directory_unusable" || !strings.Contains(actionErr.Message, missing) {
		t.Fatalf("error = %v, want directory_unusable naming %q", err, missing)
	}
}

func TestCopyTitleUsesInjectedClipboard(t *testing.T) {
	t.Parallel()

	var copied string
	target := session.RunningSession{Project: "api", Base: "W:api"}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Clipboard: func(text string) error {
			copied = text
			return nil
		},
	}, CopyTitle, Request{Session: &target})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if copied != "api" || result.Data["title"] != "api" {
		t.Fatalf("copy result = %+v, copied = %q", result, copied)
	}
}

// A launch must reject a missing project directory.
func TestLaunchRefusesAProjectWhoseDirectoryIsUnusable(t *testing.T) {
	f := tmuxtest.New(t)

	missing := filepath.Join(t.TempDir(), "gone")
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: "claude"}}}
	project := config.Session{Title: "api", Directory: missing, Profile: "work"}

	_, err := DefaultRegistry().Execute(context.Background(), tmuxDeps(f, cfg), Launch, Request{Project: &project})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "directory_unusable" {
		t.Fatalf("error = %v, want directory_unusable", err)
	}
	want := fmt.Sprintf("project %q: directory %q does not exist", "work/api", missing)
	if actionErr.Message != want {
		t.Fatalf("message = %q, want %q", actionErr.Message, want)
	}
}
