package tmux_test

import (
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
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
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := survey(t, f)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: clients=%+v sizes=%+v", what, s.Clients, s.Sizes)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func attach(t *testing.T, f *tmuxtest.Fixture, pane backend.Ref, cols, rows int) *tmuxtest.Viewer {
	t.Helper()
	client, err := f.Backend.AttachCommand(pane)
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
