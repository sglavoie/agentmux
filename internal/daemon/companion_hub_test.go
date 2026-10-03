package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// companionWindows is a Kitty tree of n managed windows, IDs 1..n, one per tab.
func companionWindows(n int) []kitty.OSWindow {
	osw := kitty.OSWindow{ID: 1, IsFocused: true}
	for i := 1; i <= n; i++ {
		osw.Tabs = append(osw.Tabs, kitty.Tab{
			ID:    100 + i,
			Title: fmt.Sprintf("W:session-%d", i),
			Windows: []kitty.Window{{
				ID:       i,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			}},
		})
	}
	return []kitty.OSWindow{osw}
}

func companionStateUpdate(h *Handler, windowID int, action, event string) {
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: event})
}

func decodeCompanionSnapshot(t *testing.T, data json.RawMessage) protocol.CompanionSnapshot {
	t.Helper()
	var snap protocol.CompanionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snap
}

func sessionIcon(snap protocol.CompanionSnapshot, windowID int) string {
	for _, s := range snap.Sessions {
		if s.WindowID == windowID {
			return s.IconState
		}
	}
	return ""
}

// TestCompanionDirtyNeverBlocksUnderStateMu: the signal is rung from inside
// stateMu on every publish path, so it must return however many times it is
// rung with nobody draining it.
func TestCompanionDirtyNeverBlocksUnderStateMu(t *testing.T) {
	h := newTestHandler()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.stateMu.Lock()
		defer h.stateMu.Unlock()
		for range 10_000 {
			h.companionDirty()
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("companionDirty blocked while stateMu was held")
	}
	if got := len(h.companion.dirty); got != 1 {
		t.Fatalf("dirty slot holds %d signals; want exactly 1 (coalesced)", got)
	}

	// A hand-built Handler has no hub; the signal is a no-op rather than a
	// nil-channel hang or a panic.
	(&Handler{}).companionDirty()
}

// TestCompanionDirtyRungByPublishPaths pins the wiring: a state change, a
// snapshot publication, and a readiness transition each ring the doorbell.
func TestCompanionDirtyRungByPublishPaths(t *testing.T) {
	cases := map[string]func(h *Handler){
		"state update": func(h *Handler) { companionStateUpdate(h, 7, "subagent", "SubagentStart") },
		"snapshot":     func(h *Handler) { h.publishSnapshot(companionWindows(1), time.Now()) },
		"ready":        func(h *Handler) { h.MarkReady() },
		"degraded":     func(h *Handler) { h.MarkDegraded("test") },
	}
	for name, ring := range cases {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler()
			ring(h)
			select {
			case <-h.companion.dirty:
			default:
				t.Fatalf("%s did not signal the companion hub", name)
			}
		})
	}
}

// TestCompanionHubBuildsOutsideStateMu proves the projection runs with
// stateMu released: the seam between copy and project acquires the lock
// itself, which would deadlock (and fail TryLock) if the build held it. The
// concurrent writers make the race detector check the copy is a real copy.
func TestCompanionHubBuildsOutsideStateMu(t *testing.T) {
	h := newTestHandler()
	h.publishSnapshot(companionWindows(4), time.Now())
	var projected, lockFree atomic.Int32
	h.companion.beforeProject = func() {
		projected.Add(1)
		if h.stateMu.TryLock() {
			lockFree.Add(1)
			h.stateMu.Unlock()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var writers sync.WaitGroup
	for w := 1; w <= 4; w++ {
		writers.Add(1)
		go func(windowID int) {
			defer writers.Done()
			for i := 0; ctx.Err() == nil; i++ {
				if i%2 == 0 {
					companionStateUpdate(h, windowID, "active", "UserPromptSubmit")
				} else {
					companionStateUpdate(h, windowID, "subagent", "SubagentStart")
				}
			}
		}(w)
	}
	for range 50 {
		h.publishCompanionSnapshot(time.Now())
	}
	cancel()
	writers.Wait()

	if projected.Load() != 50 {
		t.Fatalf("projected %d times; want 50", projected.Load())
	}
	// TryLock can lose to a writer; what matters is that it can win at all,
	// which is impossible if the builder holds the lock.
	if lockFree.Load() == 0 {
		t.Fatal("stateMu was never free during the projection: the hub builds under the lock")
	}
}

// TestCompanionHubSkipsUnchangedDigest: republishing an unchanged world hands
// subscribers nothing new.
func TestCompanionHubSkipsUnchangedDigest(t *testing.T) {
	h := newTestHandler()
	h.publishSnapshot(companionWindows(2), time.Now())
	h.publishCompanionSnapshot(time.Now())
	first := h.companion.publish
	h.publishCompanionSnapshot(time.Now())
	if h.companion.publish != first {
		t.Fatalf("unchanged world republished: publish count %d → %d", first, h.companion.publish)
	}
	companionStateUpdate(h, 1, "subagent", "SubagentStart")
	h.publishCompanionSnapshot(time.Now())
	if h.companion.publish != first+1 {
		t.Fatalf("changed world not republished: publish count %d → %d", first, h.companion.publish)
	}
	if icon := sessionIcon(decodeCompanionSnapshot(t, h.companion.latestSnapshot()), 1); icon != "subagent" {
		t.Fatalf("window 1 icon = %q; want subagent", icon)
	}
}

// TestCompanionSubscribeAckReplayAndHealth drives a real socket: the ack
// carries the negotiated contract, the first frame replays the latest
// snapshot, the hub loop pushes a changed snapshot after a state change, and
// health names the subscriber as it identified itself.
func TestCompanionSubscribeAckReplayAndHealth(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	h.publishSnapshot(companionWindows(2), time.Now())
	h.publishCompanionSnapshot(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); h.runCompanionHub(ctx) }()
	defer func() { cancel(); <-hubDone }()

	sub, err := ipc.NewClientWithSocket(sock).Subscribe(ctx, protocol.SubscribeRequest{
		Protocol: 1, Client: "test-app", ClientVersion: "9.9",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Close() }()
	ack := sub.Ack
	if ack.Protocol != 1 || ack.ServerProtocolMax != protocol.CompanionProtocolVersion || ack.SubscriberID == "" || ack.HeartbeatMS <= 0 || len(ack.Features) == 0 {
		t.Fatalf("ack = %+v", ack)
	}

	frame := nextFrameOfType(t, sub, protocol.FrameSnapshot)
	if icon := sessionIcon(decodeCompanionSnapshot(t, frame.Data), 1); icon == "subagent" {
		t.Fatalf("replayed snapshot already shows the later state")
	}

	health := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if health.Companion == nil || health.Companion.Subscribers != 1 || len(health.Companion.Clients) != 1 {
		t.Fatalf("health companion = %+v; want one subscriber", health.Companion)
	}
	if c := health.Companion.Clients[0]; c.Client != "test-app" || c.ClientVersion != "9.9" || c.SubscriberID != ack.SubscriberID {
		t.Fatalf("client = %+v", c)
	}

	companionStateUpdate(h, 1, "subagent", "SubagentStart")
	deadline := time.Now().Add(3 * time.Second)
	for {
		frame := nextFrameOfType(t, sub, protocol.FrameSnapshot)
		if sessionIcon(decodeCompanionSnapshot(t, frame.Data), 1) == "subagent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("state change never reached the subscriber")
		}
	}

	_ = sub.Close()
	waitFor(t, func() bool {
		resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
		return resp.Companion != nil && resp.Companion.Subscribers == 0
	}, "subscriber never left the hub after closing")
}

// TestCompanionHeartbeat: the hub sends heartbeats at its advertised cadence.
func TestCompanionHeartbeat(t *testing.T) {
	restore := shortenCompanionCadence()
	defer restore()
	sock, _, h := newTestServerWithRef(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); h.runCompanionHub(ctx) }()
	defer func() { cancel(); <-hubDone }()

	sub, err := ipc.NewClientWithSocket(sock).Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Close() }()
	if sub.Ack.HeartbeatMS != companionHeartbeat.Milliseconds() {
		t.Fatalf("ack heartbeat %dms; want %dms", sub.Ack.HeartbeatMS, companionHeartbeat.Milliseconds())
	}
	nextFrameOfType(t, sub, protocol.FrameHeartbeat)
}

// TestCompanionNotificationBackendRequiresAuthorization: a backend claim
// without OS authorization is not reported as a connected backend.
func TestCompanionNotificationBackendRequiresAuthorization(t *testing.T) {
	sock, _, h := newTestServerWithRef(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claim, err := ipc.NewClientWithSocket(sock).Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1, NotificationBackend: true})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = claim.Close() }()
	if got := h.companionHealth(); got.NotificationBackendConnected {
		t.Fatalf("unauthorized claim reported as backend: %+v", got)
	}
	authorized, err := ipc.NewClientWithSocket(sock).Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1, NotificationBackend: true, NotificationsAuthorized: true})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = authorized.Close() }()
	if got := h.companionHealth(); !got.NotificationBackendConnected || got.Subscribers != 2 {
		t.Fatalf("authorized backend not reported: %+v", got)
	}
}

// TestFaultDaemonRestartSendsByeShutdown: a daemon going down tells every
// subscriber why, so a companion reconnects instead of reporting a crash.
func TestFaultDaemonRestartSendsByeShutdown(t *testing.T) {
	sock, server, h := newTestServerWithRef(t)
	h.publishSnapshot(companionWindows(1), time.Now())
	h.publishCompanionSnapshot(time.Now())
	sub, err := ipc.NewClientWithSocket(sock).Subscribe(context.Background(), protocol.SubscribeRequest{Protocol: 1})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Close() }()
	nextFrameOfType(t, sub, protocol.FrameSnapshot)

	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	bye := nextFrameOfType(t, sub, protocol.FrameBye)
	var payload protocol.Bye
	if err := json.Unmarshal(bye.Data, &payload); err != nil || payload.Reason != protocol.ByeShutdown {
		t.Fatalf("bye = %s (%v); want reason %q", bye.Data, err, protocol.ByeShutdown)
	}
	if _, err := sub.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after bye: err = %v; want EOF", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("server.Close hung with a subscriber connected")
	}
}

// TestFaultSubscriberKilledMidWrite: a subscriber that stops reading and then
// vanishes while a large snapshot is half-written must cost the daemon
// nothing — the writer unwinds, the hub forgets the sink, and both a new
// subscriber and ordinary requests are served afterwards.
func TestFaultSubscriberKilledMidWrite(t *testing.T) {
	sock, _, h := newTestServerWithRef(t)
	// Enough windows that one snapshot frame is far larger than a Unix socket
	// buffer, so the writer is parked mid-frame when the peer dies.
	const windows = 300
	h.publishSnapshot(companionWindows(windows), time.Now())
	for id := 1; id <= windows; id++ {
		companionStateUpdate(h, id, "active", "UserPromptSubmit")
	}
	h.publishCompanionSnapshot(time.Now())
	if size := len(h.companion.latestSnapshot()); size < 64*1024 {
		t.Fatalf("snapshot is only %d bytes; the fault needs a frame larger than the socket buffer", size)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte(`{"type":"subscribe","subscribe":{"protocol":1}}` + "\n")); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	// Read the ack and a sliver of the first frame, then stop reading so the
	// daemon's write blocks partway through the snapshot.
	buf := make([]byte, 4096)
	if _, err := io.ReadAtLeast(conn, buf, 1); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	waitFor(t, func() bool { return h.companionHealth().Subscribers == 1 }, "subscriber never registered")
	// Keep the writer busy with fresh snapshots, then kill the peer.
	for i := range 5 {
		companionStateUpdate(h, 1+i, "subagent", "SubagentStart")
		h.publishCompanionSnapshot(time.Now())
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseRead()
	}
	_ = conn.Close()

	waitFor(t, func() bool { return h.companionHealth().Subscribers == 0 }, "killed subscriber was never removed from the hub")

	// The daemon is healthy: ordinary requests and a fresh subscription work.
	client := ipc.NewClientWithSocket(sock)
	resp, err := client.Send(protocol.Request{Type: protocol.TypeHealth})
	if err != nil || !resp.OK {
		t.Fatalf("health after killed subscriber: %+v, %v", resp, err)
	}
	sub, err := client.Subscribe(context.Background(), protocol.SubscribeRequest{Protocol: 1})
	if err != nil {
		t.Fatalf("resubscribe after killed subscriber: %v", err)
	}
	defer func() { _ = sub.Close() }()
	frame := nextFrameOfType(t, sub, protocol.FrameSnapshot)
	if got := len(decodeCompanionSnapshot(t, frame.Data).Sessions); got != windows {
		t.Fatalf("replayed snapshot carries %d sessions; want %d", got, windows)
	}
}

func shortenCompanionCadence() func() {
	debounce, safety, heartbeat := companionDebounce, companionSafetyTick, companionHeartbeat
	companionDebounce, companionSafetyTick, companionHeartbeat = 5*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	return func() { companionDebounce, companionSafetyTick, companionHeartbeat = debounce, safety, heartbeat }
}

// nextFrameOfType reads frames until one of typ arrives, skipping others.
func nextFrameOfType(t *testing.T, sub *ipc.Subscription, typ protocol.StreamFrameType) protocol.StreamFrame {
	t.Helper()
	type result struct {
		frame protocol.StreamFrame
		err   error
	}
	got := make(chan result, 1)
	go func() {
		for {
			frame, err := sub.Next()
			if err != nil || frame.Type == typ {
				got <- result{frame, err}
				return
			}
		}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("waiting for %s frame: %v", typ, r.err)
		}
		return r.frame
	case <-time.After(5 * time.Second):
		_ = sub.Close()
		t.Fatalf("no %s frame within 5s", typ)
		return protocol.StreamFrame{}
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
