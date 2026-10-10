package commands

import (
	"errors"
	"os/exec"
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
	t.Cleanup(func() { Portable = previous })
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
	if req := SessionDetailRequest(apiRow.WindowID); req.WindowID != 0 || req.RuntimeSessionID != string(api) {
		t.Fatalf("detail request = %+v, want it addressed by runtime session", req)
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
