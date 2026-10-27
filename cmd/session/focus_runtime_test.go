package session

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// focusWorld is three stand-in agents on a private tmux server, titled so the
// list order is alpha, bravo, charlie, with every seam of the focus and mute
// commands pointed at it and every Kitty seam failing the test if reached.
type focusWorld struct {
	f        *tmuxtest.Fixture
	launched map[string]session.LaunchResult
	views    []protocol.WorkspaceViewRequest
	states   map[runtimesession.ID]string
}

func newFocusWorld(t *testing.T) *focusWorld {
	t.Helper()
	f := tmuxtest.New(t)
	w := &focusWorld{f: f, launched: map[string]session.LaunchResult{}, states: map[runtimesession.ID]string{}}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}}
	cfg.Defaults.Sort = "alpha"
	// Launched out of order, so the order asserted is the list's and not the
	// server's.
	for _, title := range []string{"charlie", "alpha", "bravo"} {
		project := config.Session{Title: title, Directory: t.TempDir(), Profile: "work"}
		cfg.Sessions = append(cfg.Sessions, project)
		launched, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg)
		if err != nil {
			t.Fatal(err)
		}
		w.launched[title] = launched
	}
	previousPath, previousTmux, previousConfig := attachRegistryPath, attachNewTmux, focusLoadConfig
	previousCurrent, previousHosted, previousView := currentRuntimeSession, focusWorkspaceHosted, focusWorkspaceView
	previousDaemon, previousKitty, previousMuteKitty := sessionCommandsDaemon, sessionCommandsNewKitty, muteToggleClient
	attachRegistryPath = f.Registry.Path
	attachNewTmux = func() (backend.TargetLauncher, error) { return f.Backend, nil }
	focusLoadConfig = func() (*config.Config, error) { return cfg, nil }
	currentRuntimeSession = func() string { return "" }
	focusWorkspaceHosted = func() bool { return false }
	focusWorkspaceView = func(view protocol.WorkspaceViewRequest) error {
		w.views = append(w.views, view)
		return nil
	}
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) {
		response := protocol.Response{OK: true, States: map[int]protocol.WindowStateSnapshot{}, RuntimeSlots: map[int]protocol.RuntimeSlot{}}
		slot := 1 << 40
		for id, icon := range w.states {
			slot++
			response.RuntimeSlots[slot] = protocol.RuntimeSlot{RuntimeSessionID: string(id), Backend: "tmux"}
			response.States[slot] = protocol.WindowStateSnapshot{IconState: icon}
		}
		return response, nil
	}
	noKitty := func() kitty.KittyClient {
		t.Error("a command about a tmux session reached for Kitty")
		return cli.NewKittyClient()
	}
	sessionCommandsNewKitty = noKitty
	muteToggleClient = noKitty
	t.Cleanup(func() {
		attachRegistryPath, attachNewTmux, focusLoadConfig = previousPath, previousTmux, previousConfig
		currentRuntimeSession, focusWorkspaceHosted, focusWorkspaceView = previousCurrent, previousHosted, previousView
		sessionCommandsDaemon, sessionCommandsNewKitty, muteToggleClient = previousDaemon, previousKitty, previousMuteKitty
	})
	return w
}

func (w *focusWorld) id(title string) runtimesession.ID { return w.launched[title].RuntimeSessionID }

func (w *focusWorld) pane(title string) backend.Ref { return w.launched[title].Pane }

// attach puts a real terminal on a session and returns its client name.
func (w *focusWorld) attach(t *testing.T, title string) (string, *tmuxtest.Viewer) {
	t.Helper()
	client, err := w.f.Backend.AttachCommand(w.pane(title), tmux.AttachView{StatusLine: true})
	if err != nil {
		t.Fatal(err)
	}
	viewer := tmuxtest.StartViewer(t, client, 90, 24)
	var name string
	w.eventually(t, "a terminal on "+title, func(s tmux.Survey) bool {
		clients := s.ClientsOf(w.pane(title))
		if len(clients) == 1 {
			name = clients[0].Name
		}
		return len(clients) == 1
	})
	return name, viewer
}

func (w *focusWorld) eventually(t *testing.T, what string, ok func(tmux.Survey) bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		s, err := w.f.Backend.Survey()
		if err != nil {
			t.Fatal(err)
		}
		if ok(s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: clients=%+v", what, s.Clients)
		}
	}
}

// on waits for the terminal called client to be the one owner of title's
// session, with no terminal anywhere else it was not before.
func (w *focusWorld) on(t *testing.T, client, title string, total int) {
	t.Helper()
	w.eventually(t, client+" alone on "+title, func(s tmux.Survey) bool {
		clients := s.ClientsOf(w.pane(title))
		return len(s.Clients) == total && len(clients) == 1 && clients[0].Name == client
	})
}

// runFocus runs one of the real focus commands with flags, restoring them.
func runFocus(t *testing.T, cmd *cobra.Command, flags map[string]string) error {
	t.Helper()
	for name, value := range flags {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for name := range flags {
			_ = cmd.Flags().Set(name, "")
		}
	})
	defer func() {
		for name := range flags {
			_ = cmd.Flags().Set(name, "")
		}
	}()
	return cmd.RunE(cmd, nil)
}

func TestFocusManagedSwitchesTheNamedClientInListOrder(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "alpha")

	next := func(from string) error {
		return runFocus(t, focusNextManagedCmd, map[string]string{"runtime-session": string(w.id(from)), "client": client})
	}
	if err := next("alpha"); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "bravo", 1)
	if err := next("bravo"); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)
	// It wraps around.
	if err := next("charlie"); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "alpha", 1)
	if err := runFocus(t, focusPrevManagedCmd, map[string]string{"runtime-session": string(w.id("alpha")), "client": client}); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)

	// The registry followed the terminal: one owner, where it is now.
	registry := w.f.Load(t)
	for title := range w.launched {
		record, err := registry.Get(w.id(title))
		if err != nil {
			t.Fatal(err)
		}
		if owned := record.Attachment != nil; owned != (title == "charlie") {
			t.Errorf("%s: recorded owner = %+v", title, record.Attachment)
		}
	}
}

func TestFocusManagedUsesTheCurrentSessionAndItsTerminalWhenNothingIsNamed(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "bravo")
	// Another terminal owns the session this one is going to.
	_, resident := w.attach(t, "charlie")
	currentRuntimeSession = func() string { return string(w.id("bravo")) }

	if err := runFocus(t, focusNextManagedCmd, nil); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)
	if !resident.Exited(5 * time.Second) {
		t.Fatal("the terminal that owned the session was left attached beside the one that arrived")
	}
}

func TestFocusNextGoesToTheNextSessionWaitingOnTheOperator(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "alpha")
	flags := map[string]string{"runtime-session": string(w.id("alpha")), "client": client}

	// Nothing waits: the terminal stays where it is.
	w.states[w.id("bravo")] = "bolt"
	if err := runFocus(t, focusNextCmd, flags); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "alpha", 1)

	w.states[w.id("charlie")] = "stop"
	if err := runFocus(t, focusNextCmd, flags); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)

	// The current session is never chosen, however much it waits.
	if err := runFocus(t, focusNextCmd, map[string]string{"runtime-session": string(w.id("charlie")), "client": client}); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)
}

func TestFocusNeverReachesAnExitedOrLostSession(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "alpha")
	// bravo's agent ends where it ran; its pane stays, exited.
	panes, err := w.f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := backend.FindPane(panes, w.pane("bravo"))
	if !ok || len(agent.Foreground) == 0 {
		t.Fatalf("bravo's pane = %+v, %v", agent, ok)
	}
	if err := syscall.Kill(agent.Foreground[0].PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.eventually(t, "bravo to exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, w.pane("bravo"))
		return ok && pane.Exit != nil
	})
	flags := map[string]string{"runtime-session": string(w.id("alpha")), "client": client}
	// An exited agent is not in the list, so the neighbour is the next one
	// that runs. (tmux.Switcher refuses one named outright; its own tests
	// hold that.)
	if err := runFocus(t, focusNextManagedCmd, flags); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)
	if err := runFocus(t, focusPrevManagedCmd, map[string]string{"runtime-session": string(w.id("charlie")), "client": client}); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "alpha", 1)

	// A session nobody runs is not a place to start from either.
	stranger, err := runtimesession.NewID()
	if err != nil {
		t.Fatal(err)
	}
	err = runFocus(t, focusNextManagedCmd, map[string]string{"runtime-session": string(stranger), "client": client})
	if err == nil {
		t.Fatal("a runtime session that is not running was accepted")
	}
	// And a terminal that is not attached is not switched.
	err = runFocus(t, focusNextManagedCmd, map[string]string{"runtime-session": string(w.id("alpha")), "client": "/dev/ttys99999"})
	if !errors.Is(err, tmux.ErrNoClient) {
		t.Fatalf("switching an unknown client = %v, want it refused as no such client", err)
	}
}

// A terminal left on a session whose agent exited, its pane kept, moves on
// from there: the exited session keeps its place in the order, and the
// neighbour is the nearest one that runs.
func TestFocusMovesOnFromAnExitedSession(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "bravo")
	panes, err := w.f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := backend.FindPane(panes, w.pane("bravo"))
	if !ok || len(agent.Foreground) == 0 {
		t.Fatalf("bravo's pane = %+v, %v", agent, ok)
	}
	if err := syscall.Kill(agent.Foreground[0].PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.eventually(t, "bravo to exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, w.pane("bravo"))
		return ok && pane.Exit != nil
	})
	currentRuntimeSession = func() string { return string(w.id("bravo")) }

	if err := runFocus(t, focusNextManagedCmd, nil); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "charlie", 1)
	if err := runFocus(t, focusPrevManagedCmd, map[string]string{"runtime-session": string(w.id("bravo")), "client": client}); err != nil {
		t.Fatal(err)
	}
	w.on(t, client, "alpha", 1)
}

func TestFocusHostedByTheWorkspaceAsksItToShowTheSession(t *testing.T) {
	w := newFocusWorld(t)
	client, _ := w.attach(t, "alpha")
	focusWorkspaceHosted = func() bool { return true }
	currentRuntimeSession = func() string { return string(w.id("alpha")) }

	if err := runFocus(t, focusNextManagedCmd, nil); err != nil {
		t.Fatal(err)
	}
	want := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(w.id("bravo"))}
	if len(w.views) != 1 || w.views[0] != want {
		t.Fatalf("workspace views = %+v, want %+v", w.views, want)
	}
	// The workspace's own terminal was not moved from under it.
	w.on(t, client, "alpha", 1)
}

// attachAs runs the real `amx attach` on a pseudo-terminal of its own, as the
// desktop workspace does in one of its terminals (hosted) or as an operator
// does in a plain one, and returns once that terminal is the session's one
// owner. The marker is in the environment of that attach alone: it is gone
// again before anything else runs.
func (w *focusWorld) attachAs(t *testing.T, title string, hosted bool) string {
	t.Helper()
	before, err := w.f.Backend.Survey()
	if err != nil {
		t.Fatal(err)
	}
	if hosted {
		t.Setenv(tmux.WorkspaceHostEnv, "1")
	}
	// Ownership is recorded and the client started before the marker goes,
	// and before the next attach touches the command's flags.
	started := make(chan *tmuxtest.Viewer, 1)
	attachRunClient = func(client *exec.Cmd) error {
		viewer := tmuxtest.StartViewer(t, client, 90, 24)
		started <- viewer
		for !viewer.Exited(time.Second) {
		}
		return nil
	}
	finished := make(chan error, 1)
	go func() {
		_, err := attachRun(t, string(w.id(title)))
		finished <- err
	}()
	var viewer *tmuxtest.Viewer
	select {
	case viewer = <-started:
	case err := <-finished:
		t.Fatalf("attaching to %s: %v", title, err)
	}
	t.Cleanup(func() {
		// Its terminal closes, and the attach releases what it still owns.
		viewer.Close()
		if err := <-finished; err != nil {
			t.Errorf("attach to %s: %v", title, err)
		}
	})
	if hosted {
		if err := os.Unsetenv(tmux.WorkspaceHostEnv); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	w.eventually(t, "the new terminal alone on "+title, func(s tmux.Survey) bool {
		clients := s.ClientsOf(w.pane(title))
		if len(clients) != 1 {
			return false
		}
		name = clients[0].Name
		_, old := before.Client(name)
		return !old
	})
	return name
}

// A focus command typed in an agent's pane has that session's environment,
// which never says who is viewing it. Whether the terminal showing the pane
// is the workspace's is read from the session's recorded owner, and follows
// ownership as it moves between the workspace and a plain terminal.
func TestFocusHostedPaneUsesWorkspaceOwner(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses BSD script(1) for a pseudo-terminal")
	}
	w := newFocusWorld(t)
	previousTerminal, previousRun := attachIsTerminal, attachRunClient
	attachIsTerminal = func() bool { return true }
	t.Cleanup(func() { attachIsTerminal, attachRunClient = previousTerminal, previousRun })
	// The production answers: nothing here says "hosted" but the attach the
	// workspace ran.
	focusWorkspaceHosted = attachHosted
	inPane := func(title string) {
		t.Helper()
		currentRuntimeSession = func() string { return string(w.id(title)) }
		if _, marked := os.LookupEnv(tmux.WorkspaceHostEnv); marked {
			t.Fatal("the pane's command carries the workspace's marker")
		}
		if err := runFocus(t, focusNextManagedCmd, nil); err != nil {
			t.Fatal(err)
		}
	}
	view := func(title string) protocol.WorkspaceViewRequest {
		return protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(w.id(title))}
	}

	// The workspace shows alpha: it is asked for bravo, and its terminal is
	// not moved from under it.
	embedded := w.attachAs(t, "alpha", true)
	inPane("alpha")
	if len(w.views) != 1 || w.views[0] != view("bravo") {
		t.Fatalf("workspace views = %+v, want %+v", w.views, view("bravo"))
	}
	w.on(t, embedded, "alpha", 1)
	if record, err := w.f.Load(t).Get(w.id("bravo")); err != nil || record.Attachment != nil {
		t.Fatalf("bravo's owner = %+v, %v; the workspace was only asked", record.Attachment, err)
	}
	// The agent's environment was told nothing about its viewer.
	for _, scope := range [][]string{{"-g"}, {"-t", w.pane("alpha").ID}} {
		args := append([]string{"-S", w.f.Server.Socket(), "show-environment"}, scope...)
		environment, err := exec.Command(w.f.Server.Binary(), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("show-environment %v: %v: %s", scope, err, environment)
		}
		if strings.Contains(string(environment), tmux.WorkspaceHostEnv) {
			t.Fatalf("the environment (%v) names the session's viewer:\n%s", scope, environment)
		}
	}

	// A plain terminal takes alpha: the workspace's terminal is detached, and
	// the same command now switches the plain one.
	plain := w.attachAs(t, "alpha", false)
	if plain == embedded {
		t.Fatalf("the plain terminal is the workspace's (%s)", plain)
	}
	inPane("alpha")
	w.on(t, plain, "bravo", 1)
	if len(w.views) != 1 {
		t.Fatalf("a plain terminal's command asked the workspace: %+v", w.views)
	}

	// The workspace takes bravo back from it, and is asked again.
	embedded = w.attachAs(t, "bravo", true)
	inPane("bravo")
	if len(w.views) != 2 || w.views[1] != view("charlie") {
		t.Fatalf("workspace views = %+v, want a second one for %+v", w.views, view("charlie"))
	}
	w.on(t, embedded, "bravo", 1)

	// A terminal that is named is switched whoever owns the session.
	if err := runFocus(t, focusNextManagedCmd, map[string]string{"runtime-session": string(w.id("bravo")), "client": embedded}); err != nil {
		t.Fatal(err)
	}
	w.on(t, embedded, "charlie", 1)
	if len(w.views) != 2 {
		t.Fatalf("a named terminal asked the workspace: %+v", w.views)
	}
}

func TestMuteToggleWithoutAFlagMutesTheCurrentRuntimeSession(t *testing.T) {
	w := newFocusWorld(t)
	currentRuntimeSession = func() string { return string(w.id("bravo")) }
	muteToggleWindowID, muteToggleRuntimeSession, muteToggleJSON = 0, "", false

	if err := runMuteToggle(muteToggleCmd, nil); err != nil {
		t.Fatal(err)
	}
	panes, err := w.f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil) {
		if want := rs.RuntimeSessionID == w.id("bravo"); rs.Muted != want {
			t.Errorf("%s muted = %v, want %v", rs.Base, rs.Muted, want)
		}
	}
}
