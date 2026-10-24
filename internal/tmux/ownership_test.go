package tmux_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/testutil"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// These tests run real tmux clients on pseudo-terminals against a private
// server, because who owns a session's keyboard and size is decided by the
// server and nowhere else.

func launchAgent(t *testing.T, f *tmuxtest.Fixture, argv ...string) backend.Launched {
	t.Helper()
	if len(argv) == 0 {
		argv = []string{"/bin/sh", "-c", "exec sleep 600"}
	}
	launched, err := f.Backend.LaunchTarget(backend.LaunchSpec{Title: "agent", Directory: t.TempDir(), Argv: argv})
	if err != nil {
		t.Fatal(err)
	}
	return launched
}

func survey(t *testing.T, f *tmuxtest.Fixture) tmux.Survey {
	t.Helper()
	s, err := f.Backend.Survey()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// eventually polls the server until ok, failing with the last survey.
func eventually(t *testing.T, f *tmuxtest.Fixture, what string, ok func(tmux.Survey) bool) tmux.Survey {
	t.Helper()
	var s tmux.Survey
	testutil.Eventually(t, what, func() bool {
		s = survey(t, f)
		return ok(s)
	}, testutil.Every(25*time.Millisecond), testutil.Detail(func() string {
		return fmt.Sprintf("clients=%+v sizes=%+v", s.Clients, s.Sizes)
	}))
	return s
}

func attach(t *testing.T, f *tmuxtest.Fixture, pane backend.Ref, cols, rows int) *tmuxtest.Viewer {
	t.Helper()
	client, err := f.Backend.AttachCommand(pane, tmux.AttachView{})
	if err != nil {
		t.Fatal(err)
	}
	return tmuxtest.StartViewer(t, client, cols, rows)
}

func sized(pane backend.Ref, cols, rows int) func(tmux.Survey) bool {
	return func(s tmux.Survey) bool {
		return len(s.ClientsOf(pane)) == 1 && s.Sizes[pane.ID] == tmux.Size{Cols: cols, Rows: rows}
	}
}

func TestSurveyOfAServerThatIsNotRunning(t *testing.T) {
	f := tmuxtest.New(t)
	s := survey(t, f)
	if s.Running || s.Instance.Generation != "" || len(s.Panes) != 0 || len(s.Clients) != 0 {
		t.Fatalf("survey = %+v, want a server that is not running", s)
	}
	if s.Instance.Address != f.Server.Socket() {
		t.Fatalf("address = %q, want the socket", s.Instance.Address)
	}
}

func TestOwnerSetsTheSizeAndLosingItKeepsTheSize(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	before := survey(t, f)
	if len(before.Clients) != 0 || before.Instance != launched.Instance {
		t.Fatalf("fresh server: %+v", before)
	}

	attach(t, f, launched.Pane, 101, 31)
	owned := eventually(t, f, "the owner's size", sized(launched.Pane, 101, 31))

	// The viewer dies without a word, as a crashed app's terminal does.
	if err := syscall.Kill(owned.ClientsOf(launched.Pane)[0].PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	after := eventually(t, f, "the dead client to go", func(s tmux.Survey) bool { return len(s.Clients) == 0 })
	if got := after.Sizes[launched.Pane.ID]; got != (tmux.Size{Cols: 101, Rows: 31}) {
		t.Fatalf("size after the owner died = %+v, want the owner's last size kept", got)
	}
	pane, _ := backend.FindPane(after.Panes, launched.Pane)
	if pane.Exit != nil || len(pane.Foreground) != 1 || pane.Foreground[0].PID != launched.PID {
		t.Fatalf("agent after its viewer died = %+v, want pid %d untouched", pane, launched.PID)
	}
	if after.Instance != launched.Instance {
		t.Fatalf("server generation changed: %+v", after.Instance)
	}
}

func TestReadingAPaneNeitherAttachesNorResizes(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	attach(t, f, launched.Pane, 97, 29)
	owned := eventually(t, f, "the owner's size", sized(launched.Pane, 97, 29))
	owner := owned.ClientsOf(launched.Pane)[0]

	// Everything a preview, a probe, or the daemon does to look at a pane.
	for range 3 {
		if _, err := f.Backend.Capture(launched.Pane, backend.ExtentScreen); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Backend.Capture(launched.Pane, backend.ExtentAll); err != nil {
			t.Fatal(err)
		}
		if screen, err := f.Backend.Observe(launched.Pane); err != nil || screen.Cols != 97 || screen.Rows != 29 {
			t.Fatalf("Observe = %+v, %v; want the owner's 97x29", screen, err)
		}
		if _, err := f.Backend.Discover(); err != nil {
			t.Fatal(err)
		}
	}
	after := survey(t, f)
	clients := after.ClientsOf(launched.Pane)
	if len(clients) != 1 || clients[0].Name != owner.Name || clients[0].PID != owner.PID {
		t.Fatalf("clients after reading = %+v, want only the owner %+v", clients, owner)
	}
	if got := after.Sizes[launched.Pane.ID]; got != (tmux.Size{Cols: 97, Rows: 29}) {
		t.Fatalf("size after reading = %+v, want 97x29", got)
	}

	// With nobody attached a read still attaches nothing and resizes nothing.
	if err := f.Backend.DetachClient(owner); err != nil {
		t.Fatal(err)
	}
	eventually(t, f, "the owner to detach", func(s tmux.Survey) bool { return len(s.Clients) == 0 })
	if _, err := f.Backend.Observe(launched.Pane); err != nil {
		t.Fatal(err)
	}
	unowned := survey(t, f)
	if len(unowned.Clients) != 0 || unowned.Sizes[launched.Pane.ID] != (tmux.Size{Cols: 97, Rows: 29}) {
		t.Fatalf("unowned pane after a read: clients=%+v size=%+v", unowned.Clients, unowned.Sizes[launched.Pane.ID])
	}
}

func TestAttachingElsewhereTransfersOwnership(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	first := attach(t, f, launched.Pane, 100, 30)
	a := eventually(t, f, "the first owner", sized(launched.Pane, 100, 30)).ClientsOf(launched.Pane)[0]

	attach(t, f, launched.Pane, 80, 20)
	now := eventually(t, f, "the second owner's size", sized(launched.Pane, 80, 20))
	b := now.ClientsOf(launched.Pane)[0]
	if b.Name == a.Name {
		t.Fatalf("the owner is still %+v", a)
	}
	if !first.Exited(5 * time.Second) {
		t.Fatal("the first viewer kept running after ownership moved")
	}
	if pane, _ := backend.FindPane(now.Panes, launched.Pane); len(pane.Foreground) != 1 || pane.Foreground[0].PID != launched.PID {
		t.Fatalf("agent after the transfer = %+v, want pid %d untouched", pane, launched.PID)
	}
}

func TestACompetingClientIsFoundAndDetached(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	other := launchAgent(t, f)
	attach(t, f, launched.Pane, 100, 30)
	a := eventually(t, f, "the owner", sized(launched.Pane, 100, 30)).ClientsOf(launched.Pane)[0]
	attach(t, f, other.Pane, 70, 18)
	eventually(t, f, "the other session's owner", sized(other.Pane, 70, 18))
	if competing := survey(t, f).Competing(); len(competing) != 0 {
		t.Fatalf("one owner per session, yet competing = %+v", competing)
	}

	// A client that attaches without detaching the owner, as a hand-typed
	// `tmux attach` does. Created is in whole seconds: make it later.
	time.Sleep(1100 * time.Millisecond)
	raw := exec.Command(f.Server.Binary(), "-S", f.Server.Socket(), "-f", f.Server.ConfigPath(), "attach-session", "-t", launched.Pane.ID)
	raw.Env = slices.DeleteFunc(raw.Environ(), func(kv string) bool { return len(kv) > 5 && kv[:5] == "TMUX=" })
	tmuxtest.StartViewer(t, raw, 120, 40)
	shared := eventually(t, f, "two clients on one session", func(s tmux.Survey) bool { return len(s.ClientsOf(launched.Pane)) == 2 })

	competing := shared.Competing()
	if len(competing) != 1 || competing[0].Name != a.Name {
		t.Fatalf("competing = %+v, want only the earlier client %+v", competing, a)
	}
	if err := f.Backend.DetachClient(competing[0]); err != nil {
		t.Fatal(err)
	}
	// Detaching a client that is already gone is not an error.
	if err := f.Backend.DetachClient(competing[0]); err != nil {
		t.Fatalf("detaching a gone client: %v", err)
	}
	left := eventually(t, f, "one owner at its own size", sized(launched.Pane, 120, 40))
	if left.ClientsOf(launched.Pane)[0].Name == a.Name {
		t.Fatal("the later client was detached instead of the earlier one")
	}
	if got := left.Sizes[other.Pane.ID]; got != (tmux.Size{Cols: 70, Rows: 18}) || len(left.ClientsOf(other.Pane)) != 1 {
		t.Fatalf("the other session was disturbed: size=%+v clients=%+v", got, left.ClientsOf(other.Pane))
	}
	if err := f.Backend.DetachClient(tmux.Client{Name: "$0; kill-server"}); err == nil {
		t.Fatal("a name that is not a terminal was accepted")
	}
}

// register records launched as a live runtime session, as a launch does.
func register(t *testing.T, f *tmuxtest.Fixture, launched backend.Launched, at time.Time) runtimesession.Record {
	t.Helper()
	var record runtimesession.Record
	err := f.Registry.Update(func(registry *runtimesession.Registry) error {
		pending, err := registry.Begin(runtimesession.BeginRequest{
			Project: "alpha", LaunchID: "launch-" + launched.Pane.ID, Relation: runtimesession.RelationFresh,
		}, at)
		if err != nil {
			return err
		}
		record, err = registry.Commit(pending.ID,
			runtimesession.Target{Instance: launched.Instance, Ref: launched.Pane.ID},
			runtimesession.ProcessFingerprint{PID: launched.PID, StartToken: "launch"}, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func get(t *testing.T, f *tmuxtest.Fixture, id runtimesession.ID) runtimesession.Record {
	t.Helper()
	record, err := f.Load(t).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestReconcileTellsExitedFromLostAndLaunchesNothing(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	running := register(t, f, launchAgent(t, f), start)
	quitter := launchAgent(t, f, "/bin/sh", "-c", "read line; exit 7")
	exiting := register(t, f, quitter, start)
	closed := launchAgent(t, f)
	closing := register(t, f, closed, start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	report, err := reconciler.Reconcile(time.Now())
	if err != nil || !report.Empty() {
		t.Fatalf("pass over a healthy server = %+v, %v; want no change", report, err)
	}

	// One agent exits on its own; another's session is taken away.
	if err := exec.Command(f.Server.Binary(), "-S", f.Server.Socket(), "send-keys", "-t", quitter.Pane.ID, "Enter").Run(); err != nil {
		t.Fatal(err)
	}
	if err := f.Backend.Close(closed.Group); err != nil {
		t.Fatal(err)
	}
	eventually(t, f, "the agent to exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, quitter.Pane)
		return ok && pane.Exit != nil
	})
	report, err = reconciler.Reconcile(time.Now())
	if err != nil || len(report.Ended) != 2 {
		t.Fatalf("pass = %+v, %v; want two sessions ended", report, err)
	}
	if got := get(t, f, exiting.ID); got.State != runtimesession.StateStale || got.Outcome() != runtimesession.OutcomeExited || got.StaleReason != "agent exited (status 7)" {
		t.Fatalf("exited agent = %s %q (%s), want stale, exited with status 7", got.State, got.StaleReason, got.Outcome())
	}
	if got := get(t, f, closing.ID); got.State != runtimesession.StateStale || got.Outcome() != runtimesession.OutcomeLost {
		t.Fatalf("vanished agent = %s %q (%s), want stale and lost", got.State, got.StaleReason, got.Outcome())
	}
	if got := get(t, f, running.ID); got.State != runtimesession.StateLive {
		t.Fatalf("running agent = %s, want live", got.State)
	}

	// The server dies with the last agent in it. Nothing brings it back.
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	report, err = reconciler.Reconcile(time.Now())
	if err != nil || len(report.Ended) != 1 || report.Ended[0].ID != running.ID || report.Ended[0].Outcome != runtimesession.OutcomeLost {
		t.Fatalf("pass after the server died = %+v, %v; want the last session lost", report, err)
	}
	if s := survey(t, f); s.Running {
		t.Fatalf("reconciling started a server: %+v", s)
	}
	if report, err = reconciler.Reconcile(time.Now()); err != nil || !report.Empty() {
		t.Fatalf("pass with nothing live = %+v, %v; want no change", report, err)
	}
}

// TestReconcileEndsNothingOverASocketThatStillListens: a client turned away
// by something that still listens on the socket is not evidence that the
// server is gone, so no session is ended over it; a socket a crashed server
// left behind, with nothing listening, still is.
func TestReconcileEndsNothingOverASocketThatStillListens(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	agent := register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}

	// A listener that hangs up on every client is what a server too busy to
	// answer looks like to one: tmux reports it as gone.
	if err := os.Remove(f.Server.Socket()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", f.Server.Socket())
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	report, err := reconciler.Reconcile(time.Now())
	if err == nil || len(report.Ended) != 0 {
		t.Fatalf("pass over a socket that still listens = %+v, %v; want an unreadable server and nothing ended", report, err)
	}
	if got := get(t, f, agent.ID); got.State != runtimesession.StateLive {
		t.Fatalf("agent = %s %q, want live", got.State, got.StaleReason)
	}

	// The listener goes and its socket stays, as a crashed server's does.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.Server.Socket()); err != nil {
		t.Fatalf("the socket was removed: %v", err)
	}
	report, err = reconciler.Reconcile(time.Now())
	if err != nil || len(report.Ended) != 1 || report.Ended[0].ID != agent.ID || report.Ended[0].Outcome != runtimesession.OutcomeLost {
		t.Fatalf("pass over a dead socket = %+v, %v; want the session lost", report, err)
	}
}

func TestReconcileReleasesAnOwnerWithNoTerminal(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	record := register(t, f, launched, time.Now().Add(-time.Hour))
	reconciler := tmux.NewReconciler(f.Registry, nil)
	own := func(id string, at time.Time) {
		t.Helper()
		if err := f.Registry.Update(func(registry *runtimesession.Registry) error {
			_, err := registry.Attach(record.ID, runtimesession.Attachment{ID: id, Client: "test"}, at)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Ownership recorded a moment ago: its terminal may still be connecting.
	own("fresh", time.Now())
	if report, err := reconciler.Reconcile(time.Now()); err != nil || !report.Empty() {
		t.Fatalf("pass = %+v, %v; want a new owner left alone", report, err)
	}

	// An owner with a terminal is never released, however old.
	viewer := attach(t, f, launched.Pane, 90, 24)
	eventually(t, f, "the owner", sized(launched.Pane, 90, 24))
	later := time.Now().Add(time.Hour)
	if report, err := reconciler.Reconcile(later); err != nil || !report.Empty() {
		t.Fatalf("pass = %+v, %v; want an attached owner left alone", report, err)
	}

	// Its terminal goes away without releasing: the record is all that is left.
	viewer.Close()
	eventually(t, f, "the terminal to go", func(s tmux.Survey) bool { return len(s.Clients) == 0 })
	report, err := reconciler.Reconcile(later)
	if err != nil || len(report.Released) != 1 || report.Released[0] != record.ID {
		t.Fatalf("pass = %+v, %v; want the ownership released", report, err)
	}
	got := get(t, f, record.ID)
	if got.Attachment != nil || got.State != runtimesession.StateLive {
		t.Fatalf("record = %+v, want live with no owner", got)
	}
}

func TestReconcileDetachesCompetingClients(t *testing.T) {
	f := tmuxtest.New(t)
	launched := launchAgent(t, f)
	register(t, f, launched, time.Now().Add(-time.Hour))
	attach(t, f, launched.Pane, 100, 30)
	eventually(t, f, "the owner", sized(launched.Pane, 100, 30))
	time.Sleep(1100 * time.Millisecond)
	raw := exec.Command(f.Server.Binary(), "-S", f.Server.Socket(), "-f", f.Server.ConfigPath(), "attach-session", "-t", launched.Pane.ID)
	raw.Env = slices.DeleteFunc(raw.Environ(), func(kv string) bool { return len(kv) > 5 && kv[:5] == "TMUX=" })
	tmuxtest.StartViewer(t, raw, 110, 35)
	eventually(t, f, "two clients", func(s tmux.Survey) bool { return len(s.ClientsOf(launched.Pane)) == 2 })

	report, err := tmux.NewReconciler(f.Registry, nil).Reconcile(time.Now())
	if err != nil || len(report.Detached) != 1 {
		t.Fatalf("pass = %+v, %v; want one client detached", report, err)
	}
	eventually(t, f, "one owner at its own size", sized(launched.Pane, 110, 35))
}

// A pass says which live sessions have a terminal on them and when that
// terminal attached. That is an observation and not a change: a pass that
// found only viewers is still an empty one.
func TestReconcileReportsWhoIsViewedWithoutCountingItAsAChange(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	watched := launchAgent(t, f)
	watching := register(t, f, watched, start)
	register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	report, err := reconciler.Reconcile(time.Now())
	if err != nil || len(report.Viewed) != 0 {
		t.Fatalf("pass with no terminal = %+v, %v; want nobody viewed", report, err)
	}

	before := time.Now().Truncate(time.Second)
	attach(t, f, watched.Pane, 90, 24)
	eventually(t, f, "the terminal", sized(watched.Pane, 90, 24))
	report, err = reconciler.Reconcile(time.Now())
	if err != nil || !report.Empty() {
		t.Fatalf("pass = %+v, %v; want no change from a terminal attaching", report, err)
	}
	if len(report.Viewed) != 1 || report.Viewed[0].ID != watching.ID {
		t.Fatalf("viewed = %+v; want only the session with the terminal", report.Viewed)
	}
	if at := report.Viewed[0].At; at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("viewed at %v; want the moment the terminal attached (after %v)", at, before)
	}
}

// A pass says which live sessions are muted: the pane variable the operator's
// mute writes, read with everything else. It is an observation, like who is
// viewing, so a pass that found only a mute is still an empty one.
func TestReconcileReportsWhichSessionsAreMuted(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	quiet := launchAgent(t, f)
	muted := register(t, f, quiet, start)
	loud := register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	read := func() map[runtimesession.ID]bool {
		t.Helper()
		report, err := reconciler.Reconcile(time.Now())
		if err != nil || !report.Empty() {
			t.Fatalf("pass = %+v, %v; want no change", report, err)
		}
		mutes := map[runtimesession.ID]bool{}
		for _, m := range report.Mutes {
			mutes[m.ID] = m.Muted
		}
		if len(mutes) != 2 {
			t.Fatalf("mutes = %+v; want both live sessions read", report.Mutes)
		}
		return mutes
	}
	if got := read(); got[muted.ID] || got[loud.ID] {
		t.Fatalf("mutes before any mute = %v", got)
	}
	if err := f.Backend.SetMetadata(quiet.Pane, map[string]string{"cc_mute": "1"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !got[muted.ID] || got[loud.ID] {
		t.Fatalf("mutes after muting one = %v; want only it", got)
	}
	if err := f.Backend.SetMetadata(quiet.Pane, map[string]string{"cc_mute": ""}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[muted.ID] {
		t.Fatalf("mutes after unmuting = %v", got)
	}
}

// A pass says which color was picked for each live session: the pane variable
// the color picker writes, read with everything else, and empty again once
// the pick is removed.
func TestReconcileReportsTheColorPickedForASession(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	painted := launchAgent(t, f)
	picked := register(t, f, painted, start)
	plain := register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	read := func() map[runtimesession.ID]string {
		t.Helper()
		report, err := reconciler.Reconcile(time.Now())
		if err != nil || !report.Empty() {
			t.Fatalf("pass = %+v, %v; want no change", report, err)
		}
		colors := map[runtimesession.ID]string{}
		for _, c := range report.Colors {
			colors[c.ID] = c.Color
		}
		if len(colors) != 2 {
			t.Fatalf("colors = %+v; want both live sessions read", report.Colors)
		}
		return colors
	}
	if got := read(); got[picked.ID] != "" || got[plain.ID] != "" {
		t.Fatalf("colors before any pick = %v", got)
	}
	if err := f.Backend.SetMetadata(painted.Pane, map[string]string{"cc_color": "red"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[picked.ID] != "red" || got[plain.ID] != "" {
		t.Fatalf("colors after picking one = %v; want only it", got)
	}
	if err := f.Backend.SetMetadata(painted.Pane, map[string]string{"cc_color": ""}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[picked.ID] != "" {
		t.Fatalf("colors after removing the pick = %v", got)
	}
}

// A pass says which name each live session was given: the pane variable
// `amx name-session` writes, empty again once the name is removed.
func TestReconcileReportsTheNameGivenToASession(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	labeled := launchAgent(t, f)
	named := register(t, f, labeled, start)
	plain := register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	read := func() map[runtimesession.ID]string {
		t.Helper()
		report, err := reconciler.Reconcile(time.Now())
		if err != nil || !report.Empty() {
			t.Fatalf("pass = %+v, %v; want no change", report, err)
		}
		names := map[runtimesession.ID]string{}
		for _, n := range report.Names {
			names[n.ID] = n.Name
		}
		if len(names) != 2 {
			t.Fatalf("names = %+v; want both live sessions read", report.Names)
		}
		return names
	}
	if got := read(); got[named.ID] != "" || got[plain.ID] != "" {
		t.Fatalf("names before any was given = %v", got)
	}
	if err := f.Backend.SetMetadata(labeled.Pane, map[string]string{"cc_name": "billing fix"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[named.ID] != "billing fix" || got[plain.ID] != "" {
		t.Fatalf("names after naming one = %v; want only it", got)
	}
	if err := f.Backend.SetMetadata(labeled.Pane, map[string]string{"cc_name": ""}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[named.ID] != "" {
		t.Fatalf("names after removing it = %v", got)
	}
}

// TestReconcileReportsTheTitleOfASession: a pass reports every live session's
// group title, which is where the harness icon is kept, and follows it when
// the title is rewritten.
func TestReconcileReportsTheTitleOfASession(t *testing.T) {
	f := tmuxtest.New(t)
	start := time.Now().Add(-time.Minute)
	launched := launchAgent(t, f)
	retitled := register(t, f, launched, start)
	plain := register(t, f, launchAgent(t, f), start)
	reconciler := tmux.NewReconciler(f.Registry, nil)

	read := func() map[runtimesession.ID]string {
		t.Helper()
		report, err := reconciler.Reconcile(time.Now())
		if err != nil || !report.Empty() {
			t.Fatalf("pass = %+v, %v; want no change", report, err)
		}
		titles := map[runtimesession.ID]string{}
		for _, titled := range report.Titles {
			titles[titled.ID] = titled.Title
		}
		if len(titles) != 2 {
			t.Fatalf("titles = %+v; want both live sessions read", report.Titles)
		}
		return titles
	}
	before := read()
	if err := f.Backend.SetTitle(launched.Pane, "🦊API"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got[retitled.ID] != "🦊API" || got[plain.ID] != before[plain.ID] {
		t.Fatalf("titles after retitling one = %v; want only it changed from %v", got, before)
	}
}
