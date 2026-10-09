package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestMute_Argv verifies Mute issues kitten @ set-user-vars with cc_mute=1
// matched on the supplied window id.
func TestMute_Argv(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := session.Mute(kitty.NewBackend(client), kitty.PaneRef(7)); err != nil {
		t.Fatalf("Mute: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	got := runner.Calls[0].Args
	want := []string{"@", "set-user-vars", "--match=id:7", "cc_mute=1"}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestUnmute_Argv verifies Unmute issues kitten @ set-user-vars with cc_mute=
// (empty) matched on the supplied window id.
func TestUnmute_Argv(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := session.Unmute(kitty.NewBackend(client), kitty.PaneRef(7)); err != nil {
		t.Fatalf("Unmute: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	got := runner.Calls[0].Args
	want := []string{"@", "set-user-vars", "--match=id:7", "cc_mute="}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
