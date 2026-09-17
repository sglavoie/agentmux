package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestDefaultRegistryHasStableDefinitions(t *testing.T) {
	registry := DefaultRegistry()
	got := registry.Definitions()
	want := []ID{OpenDirectory, OpenEditor, ClearStatus, Close, Continue, CopyTitle, Focus, Fork, ForkWorktree, Handoff, Launch, Restart, ToggleLock, ToggleMute, WorktreeEditor, WorktreeForget, WorktreeList, WorktreeRemove, WorktreeReopen, WorktreeShell, WorktreeShow}
	if len(got) != len(want) {
		t.Fatalf("definition count = %d, want %d", len(got), len(want))
	}
	for i, definition := range got {
		if definition.ID != want[i] {
			t.Fatalf("definition %d = %q, want %q", i, definition.ID, want[i])
		}
	}
}

func TestFocusUsesInjectedKittyClient(t *testing.T) {
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	target := session.RunningSession{TabID: 42}

	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Kitty: client}, Focus, Request{Session: &target})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Changed || result.Action != Focus {
		t.Fatalf("result = %+v, want changed focus result", result)
	}
	if runner.CallCount("kitten", "@", "focus-tab", "--match=id:42") != 1 {
		t.Fatalf("focus did not use injected client: %+v", runner.Calls)
	}
}

func TestCloseReportsConfirmationAndLockAvailability(t *testing.T) {
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

// TestCloseReportsWhetherTheTabWasObservedToClose is the difference a caller
// offering close recovery reads. Both endings changed something, so Changed
// cannot carry it and the message must not claim more than was seen.
func TestCloseReportsWhetherTheTabWasObservedToClose(t *testing.T) {
	target := session.RunningSession{TabID: 42}
	registry := DefaultRegistry()

	// An empty listing no longer contains tab 42, so the close is confirmed.
	gone := kitty.NewClientWithSocket(kitty.NewMockRunner(map[string][]byte{"kitten @ ls": []byte("[]")}), "")
	result, err := registry.Execute(context.Background(), Dependencies{Kitty: gone}, Close, Request{Session: &target, Confirmed: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !CloseConfirmed(result) || result.Message != "closed session" {
		t.Fatalf("observed close result = %+v, want a confirmed close", result)
	}

	// A listing that cannot be read is the branch that assumes the tab closed.
	// It still succeeds, and it still may not say the session was closed.
	blind := kitty.NewClientWithSocket(kitty.NewMockRunner(nil), "")
	result, err = registry.Execute(context.Background(), Dependencies{Kitty: blind}, Close, Request{Session: &target, Confirmed: true})
	if err != nil {
		t.Fatalf("Execute with an unreadable listing: %v", err)
	}
	if CloseConfirmed(result) {
		t.Fatalf("unobserved close result = %+v, want an unconfirmed close", result)
	}
	if !result.Changed {
		t.Fatalf("unobserved close result = %+v, want the accepted request reported as a change", result)
	}
	if result.Message == "closed session" {
		t.Fatalf("unobserved close claimed %q", result.Message)
	}
}

func TestCopyTitleUsesInjectedClipboard(t *testing.T) {
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
