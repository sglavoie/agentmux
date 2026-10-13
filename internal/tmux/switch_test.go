package tmux_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// on reports the one terminal attached to pane.
func on(t *testing.T, s tmux.Survey, pane backend.Ref) tmux.Client {
	t.Helper()
	clients := s.ClientsOf(pane)
	if len(clients) != 1 {
		t.Fatalf("clients of %s = %+v, want exactly one", pane.ID, clients)
	}
	return clients[0]
}

func owned(pane backend.Ref) func(tmux.Survey) bool {
	return func(s tmux.Survey) bool { return len(s.ClientsOf(pane)) == 1 }
}

func TestSwitchClientMovesATerminalAndNeverLeavesTwoOwners(t *testing.T) {
	f := tmuxtest.New(t)
	a, b := launchAgent(t, f), launchAgent(t, f)
	attach(t, f, a.Pane, 100, 30)
	resident := attach(t, f, b.Pane, 70, 18)
	s := eventually(t, f, "both owners", func(s tmux.Survey) bool { return owned(a.Pane)(s) && owned(b.Pane)(s) })
	mover := on(t, s, a.Pane)

	if err := f.Backend.SwitchClient(mover.Name, b.Pane); err != nil {
		t.Fatal(err)
	}
	s = eventually(t, f, "the terminal on the other session, alone", func(s tmux.Survey) bool {
		return len(s.Clients) == 1 && owned(b.Pane)(s) && s.Sizes[b.Pane.ID] == tmux.Size{Cols: 100, Rows: 29}
	})
	if got := on(t, s, b.Pane); got.Name != mover.Name {
		t.Fatalf("owner of the target = %+v, want the terminal that was switched (%s)", got, mover.Name)
	}
	if !resident.Exited(5 * time.Second) {
		t.Fatal("the terminal that owned the target was not detached")
	}
	if len(s.ClientsOf(a.Pane)) != 0 {
		t.Fatalf("the session left behind still has %+v attached", s.ClientsOf(a.Pane))
	}
	// The agent left behind is untouched.
	if pane, ok := backend.FindPane(s.Panes, a.Pane); !ok || pane.Exit != nil {
		t.Fatalf("the session left behind = %+v, %v; want it running", pane, ok)
	}

	// Switching to where the terminal already is changes nothing.
	if err := f.Backend.SwitchClient(mover.Name, b.Pane); err != nil {
		t.Fatal(err)
	}
	if s = survey(t, f); len(s.Clients) != 1 || on(t, s, b.Pane).Name != mover.Name {
		t.Fatalf("clients after a switch in place = %+v", s.Clients)
	}
}

func TestSwitchClientRefusesExitedAndMissingTargets(t *testing.T) {
	f := tmuxtest.New(t)
	a := launchAgent(t, f)
	exited := launchAgent(t, f, "/bin/sh", "-c", "exit 3")
	attach(t, f, a.Pane, 90, 24)
	s := eventually(t, f, "the owner and the exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, exited.Pane)
		return owned(a.Pane)(s) && ok && pane.Exit != nil
	})
	mover := on(t, s, a.Pane)

	if err := f.Backend.SwitchClient(mover.Name, exited.Pane); !errors.Is(err, tmux.ErrPaneExited) {
		t.Fatalf("switch to an exited pane = %v, want ErrPaneExited", err)
	}
	if err := f.Backend.SwitchClient(mover.Name, backend.PaneID("%987")); !errors.Is(err, tmux.ErrPaneGone) {
		t.Fatalf("switch to a missing pane = %v, want ErrPaneGone", err)
	}
	if err := f.Backend.SwitchClient("/dev/ttys99999", a.Pane); !errors.Is(err, tmux.ErrNoClient) {
		t.Fatalf("switch of an unknown client = %v, want ErrNoClient", err)
	}
	if err := f.Backend.SwitchClient("$0; kill-server", a.Pane); err == nil {
		t.Fatal("a name that is not a terminal was accepted")
	}
	if got := on(t, survey(t, f), a.Pane); got.Name != mover.Name {
		t.Fatalf("a refused switch moved the terminal: %+v", got)
	}
}

func TestSwitcherValidatesTheSessionAndMovesTheOwnershipRecord(t *testing.T) {
	f := tmuxtest.New(t)
	a, b := launchAgent(t, f), launchAgent(t, f)
	gone := launchAgent(t, f, "/bin/sh", "-c", "exit 0")
	old := time.Now().Add(-time.Hour)
	recordA, recordB, recordGone := register(t, f, a, old), register(t, f, b, old), register(t, f, gone, old)
	if err := f.Registry.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Attach(recordA.ID, runtimesession.Attachment{ID: "amx-attach-a", Client: "test"}, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	attach(t, f, a.Pane, 90, 24)
	s := eventually(t, f, "the owner and the exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, gone.Pane)
		return owned(a.Pane)(s) && ok && pane.Exit != nil
	})
	mover := on(t, s, a.Pane)
	switcher := tmux.Switcher{Store: f.Registry, Backend: f.Backend}

	if viewer, err := switcher.Viewer(recordA.ID); err != nil || viewer.Name != mover.Name {
		t.Fatalf("viewer of the attached session = %+v, %v; want %s", viewer, err, mover.Name)
	}
	if _, err := switcher.Viewer(recordB.ID); !errors.Is(err, tmux.ErrNoClient) {
		t.Fatalf("viewer of a session nobody is attached to = %v, want ErrNoClient", err)
	}

	// An agent that exited is refused, and recorded as exited.
	var notLive *tmux.NotLiveError
	if err := switcher.Switch(mover.Name, recordGone.ID); !errors.As(err, &notLive) || notLive.Record.Outcome() != runtimesession.OutcomeExited {
		t.Fatalf("switch to an exited agent = %v, want a NotLiveError saying it exited", err)
	}
	if got := get(t, f, recordGone.ID); got.State == runtimesession.StateLive {
		t.Fatalf("the exited session is still recorded live: %+v", got)
	}
	if got := on(t, survey(t, f), a.Pane); got.Name != mover.Name {
		t.Fatalf("a refused switch moved the terminal: %+v", got)
	}

	if err := switcher.Switch(mover.Name, recordB.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, f, "the terminal on the other session", func(s tmux.Survey) bool {
		return len(s.Clients) == 1 && owned(b.Pane)(s)
	})
	if got := get(t, f, recordA.ID); got.Attachment != nil || got.State != runtimesession.StateLive {
		t.Fatalf("the session left behind = %+v, want live with no owner", got)
	}
	if got := get(t, f, recordB.ID); got.Attachment == nil || !strings.Contains(got.Attachment.Client, mover.Name) {
		t.Fatalf("the session arrived at = %+v, want it owned by the switched terminal", got)
	}

	// A server that went away takes its sessions with it: lost, not switched to.
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	notLive = nil
	if err := switcher.Switch(mover.Name, recordA.ID); !errors.As(err, &notLive) || notLive.Record.Outcome() != runtimesession.OutcomeLost {
		t.Fatalf("switch to a lost session = %v, want a NotLiveError saying it was lost", err)
	}
}

func TestPopupRunsAProgramOverAnAttachedTerminalWithTheSessionsEnvironment(t *testing.T) {
	f := tmuxtest.New(t)
	launched, err := f.Backend.LaunchTarget(backend.LaunchSpec{
		Title: "agent", Directory: t.TempDir(), Argv: []string{"/bin/sh", "-c", "exec sleep 600"},
		Env: map[string]string{runtimesession.EnvVar: "rs-popup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	attach(t, f, launched.Pane, 90, 24)
	client := on(t, eventually(t, f, "the owner", owned(launched.Pane)), launched.Pane)

	out := filepath.Join(t.TempDir(), "popup")
	popup, err := f.Backend.PopupCommand(client.Name, map[string]string{"AMX_POPUP_TEST": "yes"},
		[]string{"/bin/sh", "-c", `printf '%s %s' "$` + runtimesession.EnvVar + `" "$AMX_POPUP_TEST" > "$0"`, out})
	if err != nil {
		t.Fatal(err)
	}
	if err := popup.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "rs-popup yes" {
		t.Fatalf("the popup's program saw %q, %v; want the session's runtime session and the added variable", data, err)
	}
	// A popup is a view: the server holds no pane for it.
	if s := survey(t, f); len(s.Panes) != 1 || len(s.Clients) != 1 {
		t.Fatalf("after a popup: panes=%+v clients=%+v", s.Panes, s.Clients)
	}
	if _, err := f.Backend.PopupCommand("not a client", nil, []string{"true"}); err == nil {
		t.Fatal("a name that is not a terminal was accepted")
	}
}
