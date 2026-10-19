package commands

import (
	"errors"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// portableServer is a tmux backend that answers discovery and capture from
// fixed values. Every other operation reaches the nil embedded interface and
// panics, so a refresh that did more than read fails loudly.
type portableServer struct {
	backend.TargetLauncher
	panes       []backend.Pane
	discoverErr error
	captured    []backend.Ref
}

func (s *portableServer) Discover() ([]backend.Pane, error) { return s.panes, s.discoverErr }

func (s *portableServer) Capture(ref backend.Ref, _ backend.Extent) (string, error) {
	s.captured = append(s.captured, ref)
	return "scrollback of " + ref.ID, nil
}

func portablePane(group, pane, project string, runtime runtimesession.ID) backend.Pane {
	return backend.Pane{
		Ref: backend.PaneID(pane), Group: backend.GroupID(group), GroupTitle: project, CWD: "/work/" + project,
		Vars: map[string]string{
			"amx_profile": "work", "amx_harness": "claude", "amx_project": project,
			"amx_directory": "/work/" + project, session.RuntimeSessionVar: string(runtime),
		},
	}
}

func usePortable(t *testing.T, terminals PortableTerminals) {
	t.Helper()
	previous := Portable
	Portable = terminals
	// Every test starts with nothing listed: an outage keeps what the last
	// discovery listed, and that must not be another test's.
	recordPortableListing(nil, false)
	t.Cleanup(func() {
		Portable = previous
		recordPortableListing(nil, false)
	})
}

func newPortableRuntimeID(t *testing.T) runtimesession.ID {
	t.Helper()
	id, err := runtimesession.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func portableRefresh(t *testing.T, client *recordingKittyClient) SessionsMsg {
	t.Helper()
	var msg any
	if client == nil {
		msg = Refresh(nil, &config.Config{}, "alpha", 1, 0)()
	} else {
		msg = Refresh(client, &config.Config{}, "alpha", 1, 0)()
	}
	sessions, ok := msg.(SessionsMsg)
	if !ok {
		t.Fatalf("refresh returned %T (%v), want SessionsMsg", msg, msg)
	}
	return sessions
}

// Outside Kitty the dashboard lists tmux sessions alone, with no Kitty call at
// all, and reads each one's daemon state through the runtime session the
// daemon reported for its slot.
func TestRefreshWithoutKittyListsPortableSessionsWithTheirDaemonState(t *testing.T) {
	api, web := newPortableRuntimeID(t), newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "api", api), portablePane("$1", "%1", "web", web)}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubLocalKittySocket(t, "")
	const slot = 1<<40 + 3
	identity := conversation.Identity{Harness: "claude", SessionID: "conv-api"}
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{
			OK:           true,
			States:       map[int]protocol.WindowStateSnapshot{slot: {IconState: "key", Conversation: &identity, ASLRunIDs: []string{"run-7"}}},
			Activity:     map[int]int64{slot: 1700000000},
			Watchdog:     map[int]protocol.WatchdogState{slot: {LastAction: "active"}},
			RuntimeSlots: map[int]protocol.RuntimeSlot{slot: {RuntimeSessionID: string(api), Backend: "tmux"}},
		}, nil
	})

	msg := portableRefresh(t, nil)
	if len(msg.Sessions) != 2 {
		t.Fatalf("sessions = %d, want the two tmux sessions", len(msg.Sessions))
	}
	byProject := map[string]session.RunningSession{}
	for _, rs := range msg.Sessions {
		byProject[rs.EffectiveProject()] = rs
	}
	apiRow, webRow := byProject["api"], byProject["web"]
	if apiRow.BackendKind() != runtimesession.BackendTmux || apiRow.RuntimeSessionID != api {
		t.Fatalf("api row = backend %s runtime %s, want tmux %s", apiRow.BackendKind(), apiRow.RuntimeSessionID, api)
	}
	if apiRow.WindowID < PortableIDBase || apiRow.TabID != apiRow.WindowID || apiRow.WindowID == webRow.WindowID {
		t.Fatalf("portable IDs api=%d/%d web=%d, want distinct process-local IDs used for both tab and window", apiRow.TabID, apiRow.WindowID, webRow.WindowID)
	}
	if apiRow.State != "key" || apiRow.ASLRunID != "run-7" {
		t.Fatalf("api row state=%q run=%q, want the daemon's slot state and run", apiRow.State, apiRow.ASLRunID)
	}
	if webRow.State != "" {
		t.Fatalf("web row state = %q, want none: the daemon has not heard from it", webRow.State)
	}
	if _, ok := msg.Activity[apiRow.WindowID]; !ok {
		t.Fatalf("activity is not keyed by the row's own ID: %v", msg.Activity)
	}
	if msg.Watchdog[apiRow.WindowID].LastAction != "active" {
		t.Fatalf("watchdog is not keyed by the row's own ID: %v", msg.Watchdog)
	}
	if _, raw := msg.States[slot]; raw {
		t.Fatalf("the daemon's slot number leaked into the refresh: %v", msg.States)
	}

	// The IDs are stable across refreshes, which is what keeps the selection.
	again := portableRefresh(t, nil)
	for _, rs := range again.Sessions {
		if rs.WindowID != byProject[rs.EffectiveProject()].WindowID {
			t.Fatalf("%s changed ID between refreshes", rs.EffectiveProject())
		}
	}

	// Everything addressed by that ID finds the session behind it.
	if rs, ok := PortableSession(apiRow.TabID); !ok || rs.RuntimeSessionID != api {
		t.Fatalf("PortableSession(%d) = %v %v", apiRow.TabID, rs.RuntimeSessionID, ok)
	}
	if text, err := ReadScrollback(nil, apiRow.WindowID); err != nil || text != "scrollback of %0" {
		t.Fatalf("ReadScrollback = %q, %v", text, err)
	}
	if req, err := SessionDetailRequest(apiRow.WindowID); err != nil || req.WindowID != 0 || req.RuntimeSessionID != string(api) {
		t.Fatalf("detail request = %+v, %v; want it addressed by runtime session", req, err)
	}
	if live, ok := LivePortableConversation(identity); !ok || live.RuntimeSessionID != api {
		t.Fatalf("LivePortableConversation = %v %v, want the api session", live.RuntimeSessionID, ok)
	}
}

func TestKittySessionDetailRequestCarriesLocalSocket(t *testing.T) {
	stubLocalKittySocket(t, "unix:/kitty-second")
	req, err := SessionDetailRequest(7)
	if err != nil || req.WindowID != 7 || req.RuntimeSessionID != "" || req.KittyListenOn != "unix:/kitty-second" {
		t.Fatalf("Kitty detail request = %+v, %v", req, err)
	}
}

func TestKittySessionDetailRequestWithoutSocketFailsClosed(t *testing.T) {
	stubLocalKittySocket(t, "")
	if req, err := SessionDetailRequest(7); err == nil || req.WindowID != 0 {
		t.Fatalf("unattributed Kitty detail request = %+v, %v", req, err)
	}
}

func TestFocusOnAPortableSessionAttachesInsteadOfFocusingATab(t *testing.T) {
	runtime := newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "api", runtime)}}
	var attached []session.RunningSession
	wantErr := errors.New("no terminal")
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux, Attach: func(rs session.RunningSession) (*exec.Cmd, error) {
		attached = append(attached, rs)
		return nil, wantErr
	}})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	row := portableRefresh(t, nil).Sessions[0]

	// A nil Kitty client would panic if the tab were focused through it.
	msg := Focus(nil, row.TabID)()
	done, ok := msg.(AttachDoneMsg)
	if !ok || !errors.Is(done.Err, wantErr) {
		t.Fatalf("focus returned %T %v, want the attach's own completion", msg, msg)
	}
	if len(attached) != 1 || attached[0].RuntimeSessionID != runtime || attached[0].PaneRef() != backend.PaneID("%0") {
		t.Fatalf("attached = %+v, want the session's runtime and pane", attached)
	}
}

// A discovery that fails before anything was ever listed leaves a surface
// without Kitty nothing to show: that is the total failure. Beside Kitty the
// Kitty rows are listed and the failure rides beside them.
func TestPortableDiscoveryFailureIsTotalOnlyWithoutKitty(t *testing.T) {
	server := &portableServer{discoverErr: errors.New("tmux exploded")}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })

	if msg, ok := Refresh(nil, &config.Config{}, "alpha", 4, 0)().(ErrMsg); !ok || msg.Seq != 4 {
		t.Fatalf("refresh without Kitty = %#v, want a sequence-bearing error", msg)
	}
	client := &recordingKittyClient{windows: managedWindows(7, 70, "api")}
	msg := portableRefresh(t, client)
	if len(msg.Sessions) != 1 || msg.Sessions[0].TabID != 7 {
		t.Fatalf("refresh beside Kitty = %+v, want the Kitty row kept", msg.Sessions)
	}
	var outage *PortableOutage
	if !errors.As(msg.PortableErr, &outage) || outage.Err == nil || outage.Kept != 0 {
		t.Fatalf("refresh beside Kitty reports %v; want the failed tmux discovery", msg.PortableErr)
	}
}

// Once tmux sessions are on the list, a tmux that stops answering degrades
// the list instead of emptying it: the same rows stay, with or without Kitty,
// the refresh says they are kept, and the next good answer replaces them.
func TestAFailedTmuxDiscoveryKeepsTheLastListedSessions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client *recordingKittyClient
		kitty  int
	}{
		{name: "without Kitty"},
		{name: "beside Kitty", client: &recordingKittyClient{windows: managedWindows(7, 70, "api")}, kitty: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, web := newPortableRuntimeID(t), newPortableRuntimeID(t)
			server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "api", api), portablePane("$1", "%1", "web", web)}}
			usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
			stubLocalKittySocket(t, "")
			stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })

			before := portableRefresh(t, tc.client)
			if len(before.Sessions) != 2+tc.kitty || before.PortableErr != nil {
				t.Fatalf("first refresh = %d sessions, %v; want %d and no outage", len(before.Sessions), before.PortableErr, 2+tc.kitty)
			}

			server.panes, server.discoverErr = nil, errors.New("tmux: list-panes timed out")
			for range 2 {
				during := portableRefresh(t, tc.client)
				if got, want := portableRuntimes(during.Sessions), portableRuntimes(before.Sessions); !slices.Equal(got, want) {
					t.Fatalf("tmux rows during the outage = %v; want the last listed %v", got, want)
				}
				if len(during.Sessions) != len(before.Sessions) {
					t.Fatalf("%d rows during the outage; want all %d", len(during.Sessions), len(before.Sessions))
				}
				var outage *PortableOutage
				if !errors.As(during.PortableErr, &outage) || outage.Kept != 2 || outage.Lost != 0 || !errors.Is(during.PortableErr, server.discoverErr) {
					t.Fatalf("outage = %v; want 2 sessions kept after the failed discovery", during.PortableErr)
				}
				if !strings.Contains(during.PortableErr.Error(), "not answering") || !strings.Contains(during.PortableErr.Error(), "2 sessions") {
					t.Fatalf("outage reads %q; want it to say tmux is not answering and what is kept", during.PortableErr)
				}
				// A kept row is still a session an action can find.
				for _, rs := range during.Sessions {
					if IsPortable(rs) {
						if _, ok := PortableSession(rs.WindowID); !ok {
							t.Fatalf("kept row %d is no longer addressable", rs.WindowID)
						}
					}
				}
			}
			// Whether anything is running is a question a kept row cannot
			// answer: the probe actions rely on reports the failure.
			if _, err := TerminalDependencies(nil, &config.Config{}).LiveSessions(&config.Config{}); err == nil {
				t.Fatal("the live-session probe answered from kept rows")
			}

			server.panes, server.discoverErr = []backend.Pane{portablePane("$0", "%0", "api", api)}, nil
			after := portableRefresh(t, tc.client)
			if after.PortableErr != nil || len(after.Sessions) != 1+tc.kitty {
				t.Fatalf("recovered refresh = %d sessions, %v; want the fresh listing and no outage", len(after.Sessions), after.PortableErr)
			}
		})
	}
}

func portableRuntimes(sessions []session.RunningSession) []string {
	var ids []string
	for _, rs := range sessions {
		if IsPortable(rs) {
			ids = append(ids, string(rs.RuntimeSessionID))
		}
	}
	sort.Strings(ids)
	return ids
}

func TestKittyAndPortableSessionsShareOneList(t *testing.T) {
	runtime := newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "web", runtime)}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	client := &recordingKittyClient{windows: managedWindows(7, 70, "api")}

	msg := portableRefresh(t, client)
	if len(msg.Sessions) != 2 {
		t.Fatalf("sessions = %d, want one Kitty and one tmux row", len(msg.Sessions))
	}
	kinds := map[runtimesession.BackendKind]int{}
	for _, rs := range msg.Sessions {
		kinds[rs.BackendKind()]++
	}
	if kinds[runtimesession.BackendKitty] != 1 || kinds[runtimesession.BackendTmux] != 1 {
		t.Fatalf("backends = %v", kinds)
	}
	deps := TerminalDependencies(client, &config.Config{})
	if deps.Backend != runtimesession.BackendTmux || deps.Tmux == nil || deps.LiveSessions == nil {
		t.Fatalf("dependencies = backend %q tmux %v, want launches and the live probe to include tmux", deps.Backend, deps.Tmux != nil)
	}
	live, err := deps.LiveSessions(&config.Config{})
	if err != nil || len(live) != 2 {
		t.Fatalf("live probe = %d sessions, %v; want both backends", len(live), err)
	}
}

// With nothing portable installed the surface is exactly the Kitty one.
func TestAKittyOnlySurfaceIsUnchanged(t *testing.T) {
	usePortable(t, PortableTerminals{})
	if !LaunchesInKitty() {
		t.Fatal("a zero portable set must launch in Kitty")
	}
	deps := TerminalDependencies(nil, nil)
	if deps.Backend != "" || deps.Tmux != nil || deps.LiveSessions != nil {
		t.Fatalf("dependencies = %+v, want the Kitty defaults", deps)
	}
	if _, ok := PortableSession(70); ok {
		t.Fatal("a Kitty window ID resolved to a portable session")
	}
	if _, err := ReadScrollback(nil, 70); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("ReadScrollback without a client = %v, want ErrNoTerminal", err)
	}
}

// In a popup over an attached terminal, showing a session switches that
// terminal: attaching would nest a second client inside the popup. The
// completion says the terminal was switched, which is what ends the surface.
func TestFocusInAPopupSwitchesTheTerminalInsteadOfAttaching(t *testing.T) {
	runtime := newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "api", runtime)}}
	var switched []session.RunningSession
	var refuse error
	usePortable(t, PortableTerminals{
		Tmux: server, Launch: runtimesession.BackendTmux,
		Attach: func(session.RunningSession) (*exec.Cmd, error) {
			t.Error("a popup attached its own terminal")
			return nil, errors.New("attached")
		},
		Desktop: func(protocol.WorkspaceViewRequest) error {
			t.Error("a popup asked the desktop workspace for a view")
			return nil
		},
		Switch: func(rs session.RunningSession) error {
			switched = append(switched, rs)
			return refuse
		},
	})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	row := portableRefresh(t, nil).Sessions[0]

	done, ok := Focus(nil, row.TabID)().(AttachDoneMsg)
	if !ok || done.Err != nil || !done.Switched {
		t.Fatalf("focus = %+v, want the terminal switched", done)
	}
	if len(switched) != 1 || switched[0].RuntimeSessionID != runtime {
		t.Fatalf("switched to %+v, want the session's runtime", switched)
	}
	// A refused switch leaves the surface up, with the reason.
	refuse = errors.New("the agent exited")
	if done := Focus(nil, row.TabID)().(AttachDoneMsg); !errors.Is(done.Err, refuse) || done.Switched {
		t.Fatalf("refused switch = %+v, want the error and the surface kept", done)
	}
}

// A surface started on a runtime session opens on that session's row: the ID
// it is started with is the one the session is listed under.
func TestASurfaceOpensOnTheRowOfTheRuntimeSessionItWasStartedOn(t *testing.T) {
	first, second := newPortableRuntimeID(t), newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{
		portablePane("$0", "%0", "api", first),
		portablePane("$1", "%1", "web", second),
	}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })

	// Asked before anything was listed, as a command does before its surface
	// starts.
	source := PortableSelection(second)
	if source < PortableIDBase || PortableSelection("") != 0 {
		t.Fatalf("selection = %d (and %d for no session)", source, PortableSelection(""))
	}
	msg, ok := Refresh(nil, &config.Config{}, "alpha", 1, source)().(SessionsMsg)
	if !ok {
		t.Fatal("refresh failed")
	}
	var want int
	for _, rs := range msg.Sessions {
		if rs.RuntimeSessionID == second {
			want = rs.TabID
		}
	}
	if want == 0 || msg.SelectionTabID != want {
		t.Fatalf("selection = %d, want the row of the second session (%d)", msg.SelectionTabID, want)
	}
	live, err := readLiveSessions(nil, &config.Config{}, source)
	if err != nil || live.SourceTabID != want {
		t.Fatalf("source tab = %d, %v; want %d (the overview reads it the same way)", live.SourceTabID, err, want)
	}
}

func TestLaunchDestinationMatchesEffectBackend(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminals PortableTerminals
		want      string
	}{
		{"legacy", PortableTerminals{}, "Kitty"},
		{"tmux", PortableTerminals{Launch: runtimesession.BackendTmux}, "tmux"},
		{"workspace", PortableTerminals{Launch: runtimesession.BackendTmux, Desktop: func(protocol.WorkspaceViewRequest) error { return nil }}, "workspace (tmux)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usePortable(t, tc.terminals)
			if got := LaunchDestination(); got != tc.want {
				t.Fatalf("destination = %q, want %q", got, tc.want)
			}
		})
	}
}
