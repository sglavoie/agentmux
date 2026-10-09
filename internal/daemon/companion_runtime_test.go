package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// TestCompanionSnapshotVersionIsNegotiated: on one daemon holding a Kitty
// session and a tmux session, a subscriber that did not ask for runtime
// sessions keeps the v1 contract (Kitty only, no synthetic window IDs), and one
// that asked receives v2 with the tmux session addressed by runtime identity.
func TestCompanionSnapshotVersionIsNegotiated(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.publishSnapshot(companionWindows(1), time.Now())
	requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "tmux-conv"})))
	tmuxSlot := slotOf(t, h, onTmux)
	h.publishCompanionSnapshot(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := ipc.NewClientWithSocket(sock)

	old, err := client.Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1, Client: "old-app"})
	if err != nil {
		t.Fatalf("subscribe v1: %v", err)
	}
	defer func() { _ = old.Close() }()
	if slices.Contains(old.Ack.Features, protocol.CompanionFeatureRuntimeSessions) {
		t.Fatalf("v1 ack granted runtime sessions: %v", old.Ack.Features)
	}
	v1 := decodeCompanionSnapshot(t, nextFrameOfType(t, old, protocol.FrameSnapshot).Data)
	if v1.V != protocol.CompanionSnapshotVersion || len(v1.Sessions) != 1 || v1.Sessions[0].WindowID != 1 {
		t.Fatalf("v1 snapshot = %+v; want only Kitty window 1", v1)
	}
	for _, s := range v1.Sessions {
		if s.WindowID == tmuxSlot || s.Runtime != nil {
			t.Fatalf("v1 session leaked runtime data: %+v", s)
		}
	}

	runtime, err := client.Subscribe(ctx, protocol.SubscribeRequest{
		Protocol: 1, Client: "desktop", Features: []string{protocol.CompanionFeatureRuntimeSessions},
	})
	if err != nil {
		t.Fatalf("subscribe v2: %v", err)
	}
	defer func() { _ = runtime.Close() }()
	if !slices.Contains(runtime.Ack.Features, protocol.CompanionFeatureRuntimeSessions) {
		t.Fatalf("v2 ack did not grant runtime sessions: %v", runtime.Ack.Features)
	}
	v2 := decodeCompanionSnapshot(t, nextFrameOfType(t, runtime, protocol.FrameSnapshot).Data)
	if v2.V != protocol.CompanionSnapshotVersionRuntime || len(v2.Sessions) != 2 {
		t.Fatalf("v2 snapshot = %+v; want Kitty window 1 and the tmux session", v2)
	}
	tmux := v2.Sessions[1]
	if tmux.Runtime == nil || tmux.Runtime.SessionID != string(onTmux) || tmux.Runtime.Backend != "tmux" ||
		tmux.Runtime.Pane != "%4" || tmux.WindowID != 0 || tmux.TabID != 0 || tmux.OSWindowID != 0 ||
		tmux.KittySocket != "" || tmux.IconState != "key" || !tmux.Attention {
		t.Fatalf("v2 tmux session = %+v (runtime %+v)", tmux, tmux.Runtime)
	}

	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	versions := map[string]int{}
	for _, c := range health.Companion.Clients {
		versions[c.Client] = c.SnapshotVersion
	}
	if versions["old-app"] != protocol.CompanionSnapshotVersion || versions["desktop"] != protocol.CompanionSnapshotVersionRuntime {
		t.Fatalf("health snapshot versions = %v", versions)
	}
}
