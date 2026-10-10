package commands

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// desktopHost records the views a surface asked the workspace for and answers
// each with err.
type desktopHost struct {
	views []protocol.WorkspaceViewRequest
	err   error
}

func (h *desktopHost) show(view protocol.WorkspaceViewRequest) error {
	h.views = append(h.views, view)
	return h.err
}

// ranHere reports whether a command asked Bubble Tea to hand the
// terminal to a process, which is what attaching or running here does. The
// message type is Bubble Tea's own, so it is recognized by not being one of
// ours.
func ranHere(t *testing.T, msg any) bool {
	t.Helper()
	switch msg.(type) {
	case AttachDoneMsg, PeekDoneMsg, PickerOpenDoneMsg, UtilityDoneMsg:
		return false
	}
	return msg != nil
}

func hostedSurface(t *testing.T, host *desktopHost, panes ...backend.Pane) *portableServer {
	t.Helper()
	server := &portableServer{panes: panes}
	attach := func(session.RunningSession) (*exec.Cmd, error) { return exec.Command("true"), nil }
	usePortable(t, PortableTerminals{Tmux: server, Launch: runtimesession.BackendTmux, Attach: attach, Desktop: host.show})
	return server
}

// Hosted by the desktop workspace, focusing a session asks the workspace for
// that session's native view and leaves this terminal to the dashboard.
func TestHostedFocusGoesToTheWorkspaceSessionView(t *testing.T) {
	api := newPortableRuntimeID(t)
	host := &desktopHost{}
	hostedSurface(t, host, portablePane("$0", "%0", "api", api))
	sessions := portableRefresh(t, nil).Sessions

	msg := Focus(nil, sessions[0].TabID)()
	if done, ok := msg.(AttachDoneMsg); !ok || done.Err != nil {
		t.Fatalf("hosted focus returned %T (%v); want a clean AttachDoneMsg with the terminal kept", msg, msg)
	}
	want := []protocol.WorkspaceViewRequest{{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(api)}}
	if !reflect.DeepEqual(host.views, want) {
		t.Fatalf("workspace was asked for %+v; want %+v", host.views, want)
	}
}

// When the workspace cannot show the session — it went away, or does not list
// the session yet — focus still shows it, by attaching this terminal.
func TestHostedFocusFallsBackToAttachingHere(t *testing.T) {
	api := newPortableRuntimeID(t)
	host := &desktopHost{err: errors.New(protocol.WorkspaceViewErrorSessionNotListed)}
	hostedSurface(t, host, portablePane("$0", "%0", "api", api))
	sessions := portableRefresh(t, nil).Sessions

	if msg := Focus(nil, sessions[0].TabID)(); !ranHere(t, msg) {
		t.Fatalf("refused focus returned %T (%v); want this terminal handed to the session", msg, msg)
	}
	if len(host.views) != 1 {
		t.Fatalf("workspace was asked %d time(s); want once before falling back", len(host.views))
	}
}

// A session with no runtime session has no native view to ask for.
func TestHostedFocusOfAnUnregisteredSessionAttachesHere(t *testing.T) {
	host := &desktopHost{}
	hostedSurface(t, host, portablePane("$0", "%0", "api", ""))
	sessions := portableRefresh(t, nil).Sessions

	if msg := Focus(nil, sessions[0].TabID)(); !ranHere(t, msg) {
		t.Fatalf("focus returned %T; want this terminal handed to the session", msg)
	}
	if len(host.views) != 0 {
		t.Fatalf("workspace was asked for %+v; an unregistered session has no view", host.views)
	}
}

// The pager, the shell and the editor are utility views of the workspace:
// each request names its kind and target and never a runtime session of its
// own, except the pager, which names the session it reads.
func TestHostedUtilityViewsGoToTheWorkspace(t *testing.T) {
	api := newPortableRuntimeID(t)
	host := &desktopHost{}
	server := hostedSurface(t, host, portablePane("$0", "%0", "api", api))
	sessions := portableRefresh(t, nil).Sessions
	dir := t.TempDir()
	project := config.Session{Title: "API", Directory: dir}

	if msg := Peek(nil, sessions[0].WindowID)(); msg != (PeekDoneMsg{}) {
		t.Fatalf("hosted peek returned %T (%v)", msg, msg)
	}
	if len(server.captured) != 0 {
		t.Fatalf("hosted peek captured %v here; the workspace's pager view reads the pane", server.captured)
	}
	if msg := OpenDirectory(nil, &config.Config{}, project)(); msg != (PickerOpenDoneMsg{Stay: true}) {
		t.Fatalf("hosted shell returned %T (%v)", msg, msg)
	}
	if msg := OpenInEditor(nil, &config.Config{}, project)(); msg != (PickerOpenDoneMsg{Stay: true}) {
		t.Fatalf("hosted editor returned %T (%v)", msg, msg)
	}
	want := []protocol.WorkspaceViewRequest{
		{Kind: protocol.WorkspaceViewPager, RuntimeSessionID: string(api), Title: "api"},
		{Kind: protocol.WorkspaceViewShell, Directory: dir, Title: filepath.Base(dir)},
		{Kind: protocol.WorkspaceViewEditor, Path: dir, Title: filepath.Base(dir)},
	}
	if !reflect.DeepEqual(host.views, want) {
		t.Fatalf("workspace was asked for\n %+v\nwant\n %+v", host.views, want)
	}
}

// A refused utility view runs in this terminal, as it does with no workspace.
func TestHostedUtilityViewsFallBackToThisTerminal(t *testing.T) {
	api := newPortableRuntimeID(t)
	host := &desktopHost{err: errors.New(protocol.WorkspaceViewErrorUnavailable)}
	hostedSurface(t, host, portablePane("$0", "%0", "api", api))
	sessions := portableRefresh(t, nil).Sessions
	project := config.Session{Title: "API", Directory: t.TempDir()}

	for name, msg := range map[string]any{
		"peek":   Peek(nil, sessions[0].WindowID)(),
		"shell":  OpenDirectory(nil, &config.Config{}, project)(),
		"editor": OpenInEditor(nil, &config.Config{}, project)(),
	} {
		if !ranHere(t, msg) {
			t.Errorf("%s returned %T (%v); want it run in this terminal", name, msg, msg)
		}
	}
	if len(host.views) != 3 {
		t.Fatalf("workspace was asked %d time(s); want once per view", len(host.views))
	}
}

// Every launch that registers a runtime session is announced to the
// workspace as a session to show once it is listed; a surface in an ordinary
// terminal announces nothing.
func TestHostedLaunchesAreShownByTheWorkspace(t *testing.T) {
	host := &desktopHost{}
	hostedSurface(t, host)
	deps := TerminalDependencies(nil, &config.Config{})
	if deps.Launched == nil {
		t.Fatal("a hosted surface's dependencies do not announce launches")
	}
	launched := newPortableRuntimeID(t)
	deps.Launched(session.LaunchResult{Backend: runtimesession.BackendTmux, RuntimeSessionID: launched})
	want := []protocol.WorkspaceViewRequest{{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(launched), Await: true}}
	if !reflect.DeepEqual(host.views, want) {
		t.Fatalf("workspace was asked for %+v; want %+v", host.views, want)
	}

	usePortable(t, PortableTerminals{Tmux: &portableServer{}, Launch: runtimesession.BackendTmux})
	if TerminalDependencies(nil, &config.Config{}).Launched != nil {
		t.Fatal("a surface with no workspace announces launches")
	}
}
