package session

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestPaneTarget(t *testing.T) {
	t.Parallel()

	kittySession := RunningSession{WindowID: 42, TabID: 7}
	if got := kittySession.PaneTarget(); got != KittyPaneTarget(42) || got.KittyWindowID() != 42 ||
		got.Backend != runtimesession.BackendKitty || got.Pane != kitty.PaneRef(42) || got.IsZero() {
		t.Fatalf("Kitty session target = %+v", got)
	}

	tmuxSession := RunningSession{
		Backend: runtimesession.BackendTmux, Group: backend.GroupID("$1"), Pane: backend.PaneID("%42"),
	}
	tmuxTarget := tmuxSession.PaneTarget()
	if tmuxTarget.Backend != runtimesession.BackendTmux || tmuxTarget.Pane != backend.PaneID("%42") ||
		tmuxTarget.KittyWindowID() != 0 || tmuxTarget.IsZero() {
		t.Fatalf("tmux session target = %+v", tmuxTarget)
	}
	if tmuxTarget.String() != "tmux/pane:%42" {
		t.Fatalf("String() = %q", tmuxTarget.String())
	}

	// The same pane ID in two backends is two targets.
	sameID := PaneTarget{Backend: runtimesession.BackendTmux, Pane: kitty.PaneRef(42)}
	if sameID == KittyPaneTarget(42) {
		t.Fatal("targets of two backends compare equal")
	}
	if sameID.KittyWindowID() != 0 {
		t.Fatal("a tmux target reported a Kitty window ID")
	}

	// Nothing addressable yields the zero target.
	for name, rs := range map[string]RunningSession{
		"kitty without a window": {},
		"kitty negative window":  {WindowID: -1},
		"tmux without a pane":    {Backend: runtimesession.BackendTmux},
		"tmux with a group ref":  {Backend: runtimesession.BackendTmux, Pane: backend.GroupID("$1")},
	} {
		if got := rs.PaneTarget(); !got.IsZero() {
			t.Errorf("%s: target = %+v, want zero", name, got)
		}
	}
	for _, target := range []PaneTarget{
		{Backend: runtimesession.BackendKitty, Pane: backend.PaneID("042")},
		{Backend: runtimesession.BackendKitty, Pane: backend.PaneID("1 or title:x")},
		{Backend: runtimesession.BackendKitty, Pane: backend.GroupID("42")},
	} {
		if target.KittyWindowID() != 0 {
			t.Errorf("%+v reported window %d", target, target.KittyWindowID())
		}
	}
}
