package tmux

import (
	"errors"
	"syscall"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
)

// TestSwitchReadsBackWhereTheClientIs: a target that dies or goes away after
// the survey is skipped by the move's guard, and the read back must then say
// where the terminal really is. The target is the session created last, which
// is what tmux resolves a format in when it is not told which client to read.
func TestSwitchReadsBackWhereTheClientIs(t *testing.T) {
	f := newFixture(t, Options{})
	a := f.launch(t, "a", []string{"/bin/sh", "-c", "exec sleep 600"}, nil)
	attachViewer(t, f, a.Pane)
	client := f.tmux(t, "list-clients", "-F", "#{client_name}")
	target := f.launch(t, "target", []string{"/bin/sh", "-c", "exec sleep 600"}, nil)
	stale, err := f.backend.Survey()
	if err != nil {
		t.Fatal(err)
	}
	stays := func(what string) {
		t.Helper()
		if at := f.tmux(t, "display-message", "-p", "-t", client, "#{pane_id}"); at != a.Pane.ID {
			t.Fatalf("after %s the terminal is on %s, want %s", what, at, a.Pane.ID)
		}
	}

	// The target's program ends; its pane stays, dead.
	if err := syscall.Kill(target.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	f.waitFor(t, "target", func(p backend.Pane) bool { return p.Exit != nil })
	if err := f.backend.switchSurveyed(stale, client, target.Pane); !errors.Is(err, ErrPaneGone) {
		t.Fatalf("switch to a pane that died after the survey = %v, want ErrPaneGone", err)
	}
	stays("a switch to a dead pane")

	// The target's session goes away altogether.
	if err := f.backend.Close(target.Group); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.switchSurveyed(stale, client, target.Pane); !errors.Is(err, ErrPaneGone) {
		t.Fatalf("switch to a pane that went away after the survey = %v, want ErrPaneGone", err)
	}
	stays("a switch to a missing pane")
}
