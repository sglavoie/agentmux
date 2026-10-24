package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

func focusSession(id string) protocol.Request {
	return protocol.Request{Type: protocol.TypeFocusSession, FocusSession: &protocol.FocusSessionRequest{RuntimeSessionID: id}}
}

// TestFocusSessionAsksAWorkspaceBeforeAnyTerminal: a focus_session request
// naming a live runtime session is forwarded to a connected workspace, marked
// as the operator's own request; a terminal is switched only when no workspace
// is there, and a request that names nothing showable switches nothing.
func TestFocusSessionAsksAWorkspaceBeforeAnyTerminal(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	live, stopped, inKitty := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(live, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	directory.put(stopped, runtimesession.StateStale, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	directory.put(inKitty, runtimesession.StateLive, runtimesession.BackendKitty, "unix:/tmp/kitty-a", "kitty-1", "7")

	if resp := h.Handle(focusSession(string(live))); resp.OK || resp.Error != protocol.FocusSessionErrorNoViewer {
		t.Fatalf("with no workspace and no switcher: %+v; want %s", resp, protocol.FocusSessionErrorNoViewer)
	}

	var switched []runtimesession.ID
	var switchErr error
	h.SetRuntimeSessionSwitcher(func(record runtimesession.Record) (string, error) {
		switched = append(switched, record.ID)
		return "/dev/ttys009", switchErr
	})
	for name, tc := range map[string]struct {
		request protocol.Request
		want    string
	}{
		"no block":        {protocol.Request{Type: protocol.TypeFocusSession}, protocol.FocusSessionErrorInvalid},
		"not an ID":       {focusSession("7"), protocol.FocusSessionErrorInvalid},
		"unregistered":    {focusSession(string(newRuntimeID(t))), protocol.FocusSessionErrorUnknown},
		"a Kitty session": {focusSession(string(inKitty)), protocol.FocusSessionErrorUnknown},
		"a stopped agent": {focusSession(string(stopped)), protocol.FocusErrorStaleTarget},
	} {
		if resp := h.Handle(tc.request); resp.OK || resp.Error != tc.want {
			t.Errorf("%s: %+v; want %s", name, resp, tc.want)
		}
	}
	if len(switched) != 0 {
		t.Fatalf("a refused request switched a terminal: %v", switched)
	}

	resp := h.Handle(focusSession(" " + string(live) + " "))
	if !resp.OK || resp.FocusedSession == nil || *resp.FocusedSession != (protocol.FocusSessionResult{RuntimeSessionID: string(live), Client: "/dev/ttys009"}) {
		t.Fatalf("with no workspace: %+v; want the last-used terminal switched", resp)
	}
	if len(switched) != 1 || switched[0] != live {
		t.Fatalf("switched = %v; want %s once", switched, live)
	}
	for err, want := range map[error]string{
		tmux.ErrNoClient:         protocol.FocusSessionErrorNoViewer,
		tmux.ErrServerNotRunning: protocol.FocusSessionErrorNoViewer,
		tmux.ErrPaneExited:       protocol.FocusErrorStaleTarget,
		&tmux.NotLiveError{}:     protocol.FocusErrorStaleTarget,
		errors.New("tmux broke"): protocol.FocusSessionErrorFailed,
	} {
		switchErr = err
		if resp := h.Handle(focusSession(string(live))); resp.OK || resp.Error != want {
			t.Errorf("switch failing with %v: %+v; want %s", err, resp, want)
		}
	}
	switchErr, switched = nil, nil

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := ipc.NewClientWithSocket(sock)
	desktop, err := client.Subscribe(ctx, protocol.SubscribeRequest{
		Protocol: 1, Client: "desktop",
		Features: []string{protocol.CompanionFeatureRuntimeSessions, protocol.CompanionFeatureWorkspaceViews},
	})
	if err != nil {
		t.Fatalf("subscribe desktop: %v", err)
	}
	defer func() { _ = desktop.Close() }()

	resp, err = client.Send(focusSession(string(live)))
	if err != nil || !resp.OK || resp.FocusedSession == nil || *resp.FocusedSession != (protocol.FocusSessionResult{RuntimeSessionID: string(live), Workspaces: 1}) {
		t.Fatalf("with a workspace: %+v, %v; want it handed to one workspace", resp, err)
	}
	got := decodeWorkspaceView(t, nextFrameOfType(t, desktop, protocol.FrameWorkspaceView))
	want := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(live), Await: true, Raise: true}
	if got != want {
		t.Fatalf("forwarded view = %+v; want %+v", got, want)
	}
	if len(switched) != 0 {
		t.Fatalf("a terminal was switched although a workspace took the session: %v", switched)
	}

	// A surface the workspace embeds cannot bring the app forward: only the
	// operator's own request carries Raise.
	requireAccepted(t, h.Handle(runtimeHook(t, live, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	if resp := h.Handle(workspaceView(protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(live), Raise: true})); !resp.OK {
		t.Fatalf("session view: %+v", resp)
	}
	if got := decodeWorkspaceView(t, nextFrameOfType(t, desktop, protocol.FrameWorkspaceView)); got.Raise {
		t.Fatalf("a workspace_view request kept Raise: %+v", got)
	}

	// A clicked notification about the slot is the same request, addressed by
	// the runtime session and never by the slot.
	slot := h.runtimeSessionSlot(string(live))
	target := h.notificationTarget(slot, "work")
	if !target.Navigable || target.RuntimeSessionID != string(live) || target.Backend != "tmux" || target.KittyListenOn != "" {
		t.Fatalf("notification target = %+v; want the runtime session as its focus target", target)
	}
	if err := h.focusSessionForClick(target.RuntimeSessionID); err != nil {
		t.Fatalf("click: %v", err)
	}
	if got := decodeWorkspaceView(t, nextFrameOfType(t, desktop, protocol.FrameWorkspaceView)); got != want {
		t.Fatalf("clicked view = %+v; want %+v", got, want)
	}
	if err := h.focusSessionForClick(string(stopped)); !errors.Is(err, notification.ErrFocusTargetStale) {
		t.Fatalf("click on a stopped session = %v; want a stale target", err)
	}
}

// TestFocusSessionSwitchesTheTerminalUsedLast runs the terminal half against
// a real private tmux server with no Kitty and no workspace: the request moves
// the attached terminal to the named session, the registry follows it, and
// with nothing attached the request is refused and nothing is started.
func TestFocusSessionSwitchesTheTerminalUsedLast(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	alpha, bravo := w.launch("alpha"), w.launch("bravo")
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeSessionSwitcher(NewTmuxRuntimeSwitcher(w.tmux.Registry))

	if resp := h.Handle(focusSession(string(bravo.id))); resp.OK || resp.Error != protocol.FocusSessionErrorNoViewer {
		t.Fatalf("with no terminal attached: %+v; want %s", resp, protocol.FocusSessionErrorNoViewer)
	}
	if clients := w.survey().Clients; len(clients) != 0 {
		t.Fatalf("a refused request attached something: %+v", clients)
	}

	viewer, terminal := w.attach(alpha, 100, 30)
	defer viewer.Close()
	resp := h.Handle(focusSession(string(bravo.id)))
	if !resp.OK || resp.FocusedSession == nil || resp.FocusedSession.Client != terminal.Name || resp.FocusedSession.Workspaces != 0 {
		t.Fatalf("focus_session = %+v; want terminal %s switched", resp, terminal.Name)
	}
	s := w.survey()
	if on := s.ClientsOf(bravo.launched.Pane); len(on) != 1 || on[0].Name != terminal.Name || len(s.ClientsOf(alpha.launched.Pane)) != 0 {
		t.Fatalf("clients = %+v; want the terminal on bravo alone", s.Clients)
	}
	if record := w.record(bravo.id); record.Attachment == nil {
		t.Fatalf("the registry did not follow the terminal: %+v", record)
	}
	w.requireSameAgent(alpha, "its terminal was switched away")
	w.requireSameAgent(bravo, "a terminal was switched to it")
}

// TestFocusSessionNeverSwitchesAWorkspaceTerminal: with no workspace taking
// the request, a terminal the desktop workspace hosts is still the
// workspace's. It is never switched, even when it is the one used last, and
// its hosted owner record stays; a plain terminal is switched instead, and
// with none the request finds no viewer.
func TestFocusSessionNeverSwitchesAWorkspaceTerminal(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	alpha, bravo, charlie := w.launch("alpha"), w.launch("bravo"), w.launch("charlie")
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeSessionSwitcher(NewTmuxRuntimeSwitcher(w.tmux.Registry))

	hostedViewer, hosted := w.attach(alpha, 100, 30)
	defer hostedViewer.Close()
	owner := runtimesession.Attachment{ID: tmux.AttachmentID(true, "workspace"), Client: "tmux client " + hosted.Name}
	if err := w.tmux.Registry.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Attach(alpha.id, owner, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if resp := h.Handle(focusSession(string(bravo.id))); resp.OK || resp.Error != protocol.FocusSessionErrorNoViewer {
		t.Fatalf("with only a workspace terminal attached: %+v; want %s", resp, protocol.FocusSessionErrorNoViewer)
	}
	if on := w.survey().ClientsOf(alpha.launched.Pane); len(on) != 1 || on[0].Name != hosted.Name {
		t.Fatalf("the workspace terminal moved: %+v", w.survey().Clients)
	}
	if record := w.record(alpha.id); record.Attachment == nil || *record.Attachment != owner {
		t.Fatalf("the workspace's owner record was replaced: %+v", record.Attachment)
	}

	plainViewer, plain := w.attach(charlie, 100, 30)
	defer plainViewer.Close()
	resp := h.Handle(focusSession(string(bravo.id)))
	if !resp.OK || resp.FocusedSession == nil || resp.FocusedSession.Client != plain.Name {
		t.Fatalf("focus_session = %+v; want the plain terminal %s switched", resp, plain.Name)
	}
	s := w.survey()
	if on := s.ClientsOf(alpha.launched.Pane); len(on) != 1 || on[0].Name != hosted.Name {
		t.Fatalf("the workspace terminal moved: %+v", s.Clients)
	}
	if on := s.ClientsOf(bravo.launched.Pane); len(on) != 1 || on[0].Name != plain.Name {
		t.Fatalf("clients = %+v; want the plain terminal on bravo", s.Clients)
	}
}
