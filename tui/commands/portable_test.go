package commands

import (
	"errors"
	"os/exec"
	"slices"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

func stubStateQuery(t *testing.T, query func() (protocol.Response, error)) {
	t.Helper()
	previous := StateQuery
	StateQuery = query
	t.Cleanup(func() { StateQuery = previous })
}

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

func portableRefresh(t *testing.T, _ ...any) SessionsMsg {
	t.Helper()
	msg := Refresh(&config.Config{}, "alpha", 1, 0)()
	sessions, ok := msg.(SessionsMsg)
	if !ok {
		t.Fatalf("refresh returned %T (%v), want SessionsMsg", msg, msg)
	}
	return sessions
}

func TestRefreshListsTmuxSessionsWithTheirDaemonState(t *testing.T) {
	api, web := newPortableRuntimeID(t), newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{portablePane("$0", "%0", "api", api), portablePane("$1", "%1", "web", web)}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
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

	msg := portableRefresh(t)
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
	again := portableRefresh(t)
	for _, rs := range again.Sessions {
		if rs.WindowID != byProject[rs.EffectiveProject()].WindowID {
			t.Fatalf("%s changed ID between refreshes", rs.EffectiveProject())
		}
	}

	// Everything addressed by that ID finds the session behind it.
	if rs, ok := PortableSession(apiRow.TabID); !ok || rs.RuntimeSessionID != api {
		t.Fatalf("PortableSession(%d) = %v %v", apiRow.TabID, rs.RuntimeSessionID, ok)
	}
	if text, err := ReadScrollback(apiRow.WindowID); err != nil || text != "scrollback of %0" {
		t.Fatalf("ReadScrollback = %q, %v", text, err)
	}
	if req, err := SessionDetailRequest(apiRow.WindowID); err != nil || req.WindowID != 0 || req.RuntimeSessionID != string(api) {
		t.Fatalf("detail request = %+v, %v; want it addressed by runtime session", req, err)
	}
	if live, ok := LivePortableConversation(identity); !ok || live.RuntimeSessionID != api {
		t.Fatalf("LivePortableConversation = %v %v, want the api session", live.RuntimeSessionID, ok)
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
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	row := portableRefresh(t).Sessions[0]

	// The session is shown by attaching its tmux pane.
	msg := Focus(row.TabID)()
	done, ok := msg.(AttachDoneMsg)
	if !ok || !errors.Is(done.Err, wantErr) {
		t.Fatalf("focus returned %T %v, want the attach's own completion", msg, msg)
	}
	if len(attached) != 1 || attached[0].RuntimeSessionID != runtime || attached[0].PaneRef() != backend.PaneID("%0") {
		t.Fatalf("attached = %+v, want the session's runtime and pane", attached)
	}
}

func TestFailedTmuxDiscoveryKeepsTheLastListedSessions(t *testing.T) {
	api, web := newPortableRuntimeID(t), newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{
		portablePane("$0", "%0", "api", api), portablePane("$1", "%1", "web", web),
	}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	before := portableRefresh(t)
	if len(before.Sessions) != 2 || before.PortableErr != nil {
		t.Fatalf("first refresh = %+v", before)
	}
	server.panes, server.discoverErr = nil, errors.New("tmux: list-panes timed out")
	during := portableRefresh(t)
	if len(during.Sessions) != 2 || during.PortableErr == nil {
		t.Fatalf("outage discarded the listed sessions: %+v", during)
	}
	if got, want := []runtimesession.ID{during.Sessions[0].RuntimeSessionID, during.Sessions[1].RuntimeSessionID},
		[]runtimesession.ID{before.Sessions[0].RuntimeSessionID, before.Sessions[1].RuntimeSessionID}; !slices.Equal(got, want) {
		t.Fatalf("kept sessions = %v, want %v", got, want)
	}
	if _, err := TerminalDependencies(&config.Config{}).LiveSessions(&config.Config{}); err == nil {
		t.Fatal("the live-session probe answered from kept rows")
	}
	server.panes, server.discoverErr = []backend.Pane{portablePane("$0", "%0", "api", api)}, nil
	after := portableRefresh(t)
	if after.PortableErr != nil || len(after.Sessions) != 1 {
		t.Fatalf("recovered refresh = %+v", after)
	}
}

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
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	row := portableRefresh(t).Sessions[0]

	done, ok := Focus(row.TabID)().(AttachDoneMsg)
	if !ok || done.Err != nil || !done.Switched {
		t.Fatalf("focus = %+v, want the terminal switched", done)
	}
	if len(switched) != 1 || switched[0].RuntimeSessionID != runtime {
		t.Fatalf("switched to %+v, want the session's runtime", switched)
	}
	// A refused switch leaves the surface up, with the reason.
	refuse = errors.New("the agent exited")
	if done := Focus(row.TabID)().(AttachDoneMsg); !errors.Is(done.Err, refuse) || done.Switched {
		t.Fatalf("refused switch = %+v, want the error and the surface kept", done)
	}
}

func TestASurfaceOpensOnTheRowOfTheRuntimeSessionItWasStartedOn(t *testing.T) {
	first, second := newPortableRuntimeID(t), newPortableRuntimeID(t)
	server := &portableServer{panes: []backend.Pane{
		portablePane("$0", "%0", "api", first),
		portablePane("$1", "%1", "web", second),
	}}
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux})
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })

	// Asked before anything was listed, as a command does before its surface
	// starts.
	source := PortableSelection(second)
	if source < PortableIDBase || PortableSelection("") != 0 {
		t.Fatalf("selection = %d (and %d for no session)", source, PortableSelection(""))
	}
	msg, ok := Refresh(&config.Config{}, "alpha", 1, source)().(SessionsMsg)
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
	live, err := readLiveSessions(&config.Config{}, source)
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
		{"default", PortableTerminals{}, "tmux"},
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
