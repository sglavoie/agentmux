package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func workspaceView(view protocol.WorkspaceViewRequest) protocol.Request {
	return protocol.Request{Type: protocol.TypeWorkspaceView, WorkspaceView: &view}
}

func decodeWorkspaceView(t *testing.T, frame protocol.StreamFrame) protocol.WorkspaceViewRequest {
	t.Helper()
	var view protocol.WorkspaceViewRequest
	if err := json.Unmarshal(frame.Data, &view); err != nil {
		t.Fatalf("decode workspace_view frame: %v", err)
	}
	return view
}

// TestWorkspaceViewReachesOnlyWorkspaces: a view request is forwarded to the
// subscriber that hosts workspace views and to no other, and the snapshot a
// plain subscriber reads is not disturbed by it.
func TestWorkspaceViewReachesOnlyWorkspaces(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	listed := newRuntimeID(t)
	directory.put(listed, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	requireAccepted(t, h.Handle(runtimeHook(t, listed, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	h.publishCompanionSnapshot(time.Now())

	if resp := h.Handle(workspaceView(protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewDashboard})); resp.OK ||
		resp.Error != protocol.WorkspaceViewErrorUnavailable {
		t.Fatalf("with no workspace connected: %+v; want %s", resp, protocol.WorkspaceViewErrorUnavailable)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := ipc.NewClientWithSocket(sock)
	plain, err := client.Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1, Client: "menu-only"})
	if err != nil {
		t.Fatalf("subscribe plain: %v", err)
	}
	defer func() { _ = plain.Close() }()
	if slices.Contains(plain.Ack.Features, protocol.CompanionFeatureWorkspaceViews) {
		t.Fatalf("plain ack granted workspace views: %v", plain.Ack.Features)
	}
	if resp := h.Handle(workspaceView(protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewDashboard})); resp.OK {
		t.Fatalf("a subscriber without the feature was treated as a workspace: %+v", resp)
	}

	desktop, err := client.Subscribe(ctx, protocol.SubscribeRequest{
		Protocol: 1, Client: "desktop",
		Features: []string{protocol.CompanionFeatureRuntimeSessions, protocol.CompanionFeatureWorkspaceViews},
	})
	if err != nil {
		t.Fatalf("subscribe desktop: %v", err)
	}
	defer func() { _ = desktop.Close() }()
	if !slices.Contains(desktop.Ack.Features, protocol.CompanionFeatureWorkspaceViews) {
		t.Fatalf("desktop ack = %v; want workspace views granted", desktop.Ack.Features)
	}

	resp, err := client.Send(workspaceView(protocol.WorkspaceViewRequest{
		Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(listed),
		Directory: "/ignored", Path: "/ignored", Title: "ignored\nlabel",
	}))
	if err != nil || !resp.OK || resp.WorkspaceView == nil || resp.WorkspaceView.Delivered != 1 {
		t.Fatalf("session view: %+v, %v; want delivered to one workspace", resp, err)
	}
	got := decodeWorkspaceView(t, nextFrameOfType(t, desktop, protocol.FrameWorkspaceView))
	want := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(listed), Title: "ignored label"}
	if got != want {
		t.Fatalf("forwarded view = %+v; want %+v", got, want)
	}

	// The plain subscriber's stream carries its snapshot and no view frame.
	done := make(chan protocol.StreamFrame, 8)
	go func() {
		for {
			frame, err := plain.Next()
			if err != nil {
				close(done)
				return
			}
			done <- frame
		}
	}()
	deadline := time.After(300 * time.Millisecond)
	for waiting := true; waiting; {
		select {
		case frame, ok := <-done:
			if !ok {
				waiting = false
				break
			}
			if frame.Type == protocol.FrameWorkspaceView {
				t.Fatalf("a subscriber without the feature received a workspace view")
			}
		case <-deadline:
			waiting = false
		}
	}

	health := h.companionHealth()
	hosts := 0
	for _, c := range health.Clients {
		if c.WorkspaceViews {
			hosts++
		}
	}
	if hosts != 1 {
		t.Fatalf("health reports %d workspace hosts; want 1: %+v", hosts, health.Clients)
	}
}

// TestWorkspaceViewValidation: the daemon forwards only a view a workspace
// can act on, and names why it refused the rest.
func TestWorkspaceViewValidation(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)

	listed, quiet, ended, pending, onKitty, unknown := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(listed, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%1")
	directory.put(quiet, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%2")
	directory.put(ended, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
	directory.put(pending, runtimesession.StatePending, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	requireAccepted(t, h.Handle(runtimeHook(t, listed, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	desktop, err := ipc.NewClientWithSocket(sock).Subscribe(ctx, protocol.SubscribeRequest{
		Protocol: 1, Client: "desktop", Features: []string{protocol.CompanionFeatureWorkspaceViews},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = desktop.Close() }()

	refused := []struct {
		name string
		view *protocol.WorkspaceViewRequest
		code string
	}{
		{"no block", nil, protocol.WorkspaceViewErrorInvalid},
		{"unknown kind", &protocol.WorkspaceViewRequest{Kind: "exec"}, protocol.WorkspaceViewErrorInvalid},
		{"session without an id", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession}, protocol.WorkspaceViewErrorInvalid},
		{"malformed id", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: "%4"}, protocol.WorkspaceViewErrorInvalid},
		{"unregistered session", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(unknown)}, protocol.WorkspaceViewErrorSessionUnknown},
		{"ended session", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(ended), Await: true}, protocol.WorkspaceViewErrorSessionUnknown},
		{"pending session", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(pending), Await: true}, protocol.WorkspaceViewErrorSessionUnknown},
		{"Kitty session", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(onKitty)}, protocol.WorkspaceViewErrorSessionUnknown},
		{"session the daemon has not heard from", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(quiet)}, protocol.WorkspaceViewErrorSessionNotListed},
		{"pager of an ended session", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewPager, RuntimeSessionID: string(ended)}, protocol.WorkspaceViewErrorSessionUnknown},
		{"shell without a directory", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell}, protocol.WorkspaceViewErrorInvalid},
		{"shell in a relative directory", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell, Directory: "projects/api"}, protocol.WorkspaceViewErrorInvalid},
		{"shell in an unclean directory", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell, Directory: "/tmp/../etc"}, protocol.WorkspaceViewErrorInvalid},
		{"editor on a path with a newline", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewEditor, Path: "/tmp/a\nb"}, protocol.WorkspaceViewErrorInvalid},
		{"editor without a path", &protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewEditor, Directory: "/tmp"}, protocol.WorkspaceViewErrorInvalid},
	}
	for _, tc := range refused {
		resp := h.Handle(protocol.Request{Type: protocol.TypeWorkspaceView, WorkspaceView: tc.view})
		if resp.OK || resp.Error != tc.code {
			t.Errorf("%s: %+v; want refusal %s", tc.name, resp, tc.code)
		}
	}

	forwarded := []struct {
		name string
		view protocol.WorkspaceViewRequest
		want protocol.WorkspaceViewRequest
	}{
		{
			"a just-launched session is awaited",
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(quiet), Await: true},
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(quiet), Await: true},
		},
		{
			"a pager needs no listing",
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewPager, RuntimeSessionID: string(quiet), Await: true, Title: "API"},
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewPager, RuntimeSessionID: string(quiet), Title: "API"},
		},
		{
			"a shell keeps only its directory",
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell, Directory: "/tmp/api", Path: "/x", RuntimeSessionID: string(listed), Title: strings.Repeat("é", 100)},
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell, Directory: "/tmp/api", Title: strings.Repeat("é", 48)},
		},
		{
			"an editor keeps only its path",
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewEditor, Path: "/tmp/api/main.go", Directory: "/x"},
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewEditor, Path: "/tmp/api/main.go"},
		},
		{
			"the overview names nothing",
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewOverview, Path: "/x", Title: "x", RuntimeSessionID: "junk"},
			protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewOverview},
		},
	}
	for _, tc := range forwarded {
		resp := h.Handle(workspaceView(tc.view))
		if !resp.OK {
			t.Fatalf("%s: refused: %+v", tc.name, resp)
		}
		if got := decodeWorkspaceView(t, nextFrameOfType(t, desktop, protocol.FrameWorkspaceView)); got != tc.want {
			t.Errorf("%s: forwarded %+v; want %+v", tc.name, got, tc.want)
		}
	}

	// Forwarding a view creates no session: the quiet session is still not
	// listed, and nothing waits on anyone because a view was asked for.
	if h.runtimeSessionListed(string(quiet)) {
		t.Fatalf("a view request made the daemon hold state for a session it never heard from")
	}
	snapshot := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersionRuntime]
	if len(snapshot.Sessions) != 1 || snapshot.Attention.Total != 1 {
		t.Fatalf("snapshot after view requests = %d session(s), %d waiting; want the one hooked session only",
			len(snapshot.Sessions), snapshot.Attention.Total)
	}
}
