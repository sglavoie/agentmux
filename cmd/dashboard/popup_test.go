package dashboard

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/tui/commands"
)

// popupWorld is the portable world with real terminals attached, and the
// seams of a popup surface pointed at it.
type popupWorld struct {
	*portableWorld
	started []dashboardStart
}

type dashboardStart struct {
	client kitty.KittyClient
	source int
}

func newPopupWorld(t *testing.T, titles ...string) *popupWorld {
	t.Helper()
	w := &popupWorld{portableWorld: newPortableWorld(t, titles...)}
	oldRun, oldConfig, oldOpenConfig := runDashboard, loadRunnableConfig, loadOpenDashboardConfig
	oldCurrent, oldRegistry, oldPortable := viewCurrentSession, viewRegistryPath, commands.Portable
	oldEnviron := workspaceViewEnviron
	runDashboard = func(_ *config.Config, client kitty.KittyClient, source int) error {
		w.started = append(w.started, dashboardStart{client, source})
		return nil
	}
	loadRunnableConfig = func() (*config.Config, error) { return w.cfg, nil }
	loadOpenDashboardConfig = loadRunnableConfig
	viewCurrentSession = func() string { return "" }
	viewRegistryPath = w.tmux.Registry.Path
	// A popup gives up the identity it runs with; a test must keep its own.
	workspaceViewEnviron = func() []string { return nil }
	t.Cleanup(func() {
		runDashboard, loadRunnableConfig, loadOpenDashboardConfig = oldRun, oldConfig, oldOpenConfig
		viewCurrentSession, viewRegistryPath, commands.Portable = oldCurrent, oldRegistry, oldPortable
		workspaceViewEnviron = oldEnviron
	})
	return w
}

func (w *popupWorld) pane(t *testing.T, title string) backend.Ref {
	t.Helper()
	panes, err := w.tmux.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range panes {
		if pane.Vars[session.RuntimeSessionVar] == string(w.ids[title]) {
			return pane.Ref
		}
	}
	t.Fatalf("no pane for %s", title)
	return backend.Ref{}
}

func (w *popupWorld) eventually(t *testing.T, what string, ok func(tmux.Survey) bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		s, err := w.tmux.Backend.Survey()
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

// attach puts a real terminal on a session and returns its client name.
func (w *popupWorld) attach(t *testing.T, title string) string {
	t.Helper()
	pane := w.pane(t, title)
	client, err := w.tmux.Backend.AttachCommand(pane, tmux.AttachView{StatusLine: true})
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.StartViewer(t, client, 90, 24)
	var name string
	w.eventually(t, "a terminal on "+title, func(s tmux.Survey) bool {
		clients := s.ClientsOf(pane)
		if len(clients) == 1 {
			name = clients[0].Name
		}
		return len(clients) == 1
	})
	return name
}

func (w *popupWorld) row(t *testing.T, title string) session.RunningSession {
	t.Helper()
	panes, err := w.tmux.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, w.cfg) {
		if rs.RuntimeSessionID == w.ids[title] {
			return rs
		}
	}
	t.Fatalf("no row for %s", title)
	return session.RunningSession{}
}

// The dashboard in a popup opens on the session the terminal is on, and
// choosing a row switches that terminal and ends the dashboard.
func TestPopupDashboardOpensOnItsSessionAndSwitchesTheTerminal(t *testing.T) {
	w := newPopupWorld(t, "alpha", "bravo")
	client := w.attach(t, "alpha")
	// The popup runs in the environment of the session it is over.
	viewCurrentSession = func() string { return string(w.ids["alpha"]) }

	if err := Run(0, "", View{Popup: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 || w.started[0].client != nil {
		t.Fatalf("dashboards started = %+v, want one with no Kitty client", w.started)
	}
	if want := commands.PortableSelection(w.ids["alpha"]); w.started[0].source != want || want < commands.PortableIDBase {
		t.Fatalf("the dashboard opened on %d, want the row of the session it is over (%d)", w.started[0].source, want)
	}
	if commands.Portable.Switch == nil {
		t.Fatal("a popup dashboard shows a session by attaching, which would nest a client in the popup")
	}

	// Choosing a row: the surface's own Attach, as Enter runs it.
	msg, ok := commands.Attach(w.row(t, "bravo"))().(commands.AttachDoneMsg)
	if !ok || msg.Err != nil || !msg.Switched {
		t.Fatalf("choosing a row = %+v, want the terminal switched", msg)
	}
	bravo := w.pane(t, "bravo")
	w.eventually(t, "the terminal alone on bravo", func(s tmux.Survey) bool {
		clients := s.ClientsOf(bravo)
		return len(s.Clients) == 1 && len(clients) == 1 && clients[0].Name == client
	})
	record, err := w.tmux.Load(t).Get(w.ids["bravo"])
	if err != nil || record.Attachment == nil {
		t.Fatalf("bravo's record = %+v, %v; want the terminal recorded as its owner", record, err)
	}
}

// Outside a popup nothing is switched: the dashboard owns its terminal and
// attaches it, as before, and only opens on the session it was asked for.
func TestDashboardSelectsARuntimeSessionWithoutBecomingAPopup(t *testing.T) {
	w := newPopupWorld(t, "alpha", "bravo")
	if err := Run(0, "", View{Select: string(w.ids["bravo"])}); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 || w.started[0].source != commands.PortableSelection(w.ids["bravo"]) {
		t.Fatalf("dashboards started = %+v, want one opened on bravo", w.started)
	}
	if commands.Portable.Switch != nil {
		t.Fatal("a dashboard that owns its terminal was given a terminal to switch")
	}
	if err := Run(0, "", View{Select: "not-a-session"}); err == nil {
		t.Fatal("--select accepted something that is not a runtime session ID")
	}
}

func runOpenDashboardOutsideKitty(t *testing.T, jsonOut bool) (string, error) {
	t.Helper()
	oldJSON := openDashboardJSON
	openDashboardJSON = jsonOut
	t.Cleanup(func() { openDashboardJSON = oldJSON })
	var out bytes.Buffer
	cmd := OpenDashboardCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runOpenDashboard(cmd, nil)
	return out.String(), err
}

// open-dashboard outside Kitty opens the popup over the terminal used last,
// on the session it shows; with no terminal attached it starts the dashboard
// where it was typed.
func TestOpenDashboardOutsideKittyOpensAPopupOverTheLastTerminal(t *testing.T) {
	w := newPopupWorld(t, "alpha", "bravo")
	oldBinary, oldStart, oldHere := openDashboardBinary, openDashboardStartPopup, openDashboardHere
	t.Cleanup(func() { openDashboardBinary, openDashboardStartPopup, openDashboardHere = oldBinary, oldStart, oldHere })
	openDashboardBinary = func() (string, error) { return "/opt/amx", nil }
	var popups [][]string
	openDashboardStartPopup = func(popup *exec.Cmd) error {
		popups = append(popups, popup.Args)
		return nil
	}
	here := 0
	openDashboardHere = func() error { here++; return nil }

	// Nobody is attached: the dashboard starts here, and --json says why not.
	if _, err := runOpenDashboardOutsideKitty(t, false); err != nil || here != 1 || len(popups) != 0 {
		t.Fatalf("with no terminal attached: %v, started here %d times, popups %v", err, here, popups)
	}
	if out, err := runOpenDashboardOutsideKitty(t, true); err == nil || !strings.Contains(out, "no_attached_terminal") || here != 1 {
		t.Fatalf("--json with no terminal attached = %q, %v", out, err)
	}

	client := w.attach(t, "bravo")
	out, err := runOpenDashboardOutsideKitty(t, true)
	if err != nil {
		t.Fatalf("open-dashboard: %v\n%s", err, out)
	}
	var envelope struct {
		Data openDashboardData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if want := (openDashboardData{Opened: true, Client: client}); envelope.Data != want {
		t.Fatalf("data = %+v, want %+v", envelope.Data, want)
	}
	if len(popups) != 1 || here != 1 {
		t.Fatalf("popups = %v, started here %d times; want one popup", popups, here)
	}
	args := popups[0]
	at := slices.Index(args, "--")
	if at < 0 || !slices.Contains(args[:at], "display-popup") || args[slices.Index(args, "-c")+1] != client {
		t.Fatalf("popup command = %v, want a display-popup over %s", args, client)
	}
	want := []string{"/opt/amx", "--popup", "--client", client, "--select", string(w.ids["bravo"])}
	if !slices.Equal(args[at+1:], want) {
		t.Fatalf("popup runs %v, want %v", args[at+1:], want)
	}
}
