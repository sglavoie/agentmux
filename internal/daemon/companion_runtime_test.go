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

// TestCompanionRuntimeSnapshotForEverySubscriber: the runtime snapshot is the
// only published shape, including when a subscriber sends no feature list.
func TestCompanionRuntimeSnapshotForEverySubscriber(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "tmux-conv"})))
	h.publishCompanionSnapshot(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := ipc.NewClientWithSocket(sock)

	old, err := client.Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1, Client: "plain-app"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = old.Close() }()
	if !slices.Contains(old.Ack.Features, protocol.CompanionFeatureRuntimeSessions) {
		t.Fatalf("runtime feature missing from ack: %v", old.Ack.Features)
	}
	plain := decodeCompanionSnapshot(t, nextFrameOfType(t, old, protocol.FrameSnapshot).Data)
	if plain.V != protocol.CompanionSnapshotVersion || len(plain.Sessions) != 1 {
		t.Fatalf("plain snapshot = %+v; want the tmux session", plain)
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
	if v2.V != protocol.CompanionSnapshotVersion || len(v2.Sessions) != 1 {
		t.Fatalf("v2 snapshot = %+v; want the tmux session", v2)
	}
	tmux := v2.Sessions[0]
	if tmux.Runtime == nil || tmux.Runtime.SessionID != string(onTmux) || tmux.Runtime.Backend != "tmux" ||
		tmux.Runtime.Pane != "%4" || tmux.IconState != "key" || !tmux.Attention {
		t.Fatalf("v2 tmux session = %+v (runtime %+v)", tmux, tmux.Runtime)
	}

	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	versions := map[string]int{}
	for _, c := range health.Companion.Clients {
		versions[c.Client] = c.SnapshotVersion
	}
	if versions["plain-app"] != protocol.CompanionSnapshotVersion || versions["desktop"] != protocol.CompanionSnapshotVersion {
		t.Fatalf("health snapshot versions = %v", versions)
	}
}
