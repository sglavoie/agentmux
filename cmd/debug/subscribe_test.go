package debug

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type fakeSubscription struct {
	frames []protocol.StreamFrame
	closed bool
}

func (f *fakeSubscription) Next() (protocol.StreamFrame, error) {
	if len(f.frames) == 0 {
		return protocol.StreamFrame{}, io.EOF
	}
	frame := f.frames[0]
	f.frames = f.frames[1:]
	return frame, nil
}

func (f *fakeSubscription) Close() error { f.closed = true; return nil }

func snapshotFrame(t *testing.T, seq uint64, icons ...string) protocol.StreamFrame {
	t.Helper()
	snap := protocol.CompanionSnapshot{V: 1, Daemon: protocol.CompanionDaemon{Ready: true}, Kitty: protocol.CompanionKitty{Ready: true}}
	for i, icon := range icons {
		attention := protocol.AttentionStates[icon]
		snap.Sessions = append(snap.Sessions, protocol.CompanionSession{WindowID: i + 1, IconState: icon, Attention: attention})
		if attention {
			snap.Attention.Total++
		}
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.StreamFrame{V: 1, Seq: seq, Type: protocol.FrameSnapshot, Data: data}
}

func frameDigest(frame protocol.StreamFrame) string {
	sum := sha256.Sum256(frame.Data)
	return hex.EncodeToString(sum[:4])
}

func runSubscribeForTest(t *testing.T, frames int, jsonOut bool, sub *fakeSubscription, subErr error) (string, protocol.SubscribeRequest, error) {
	t.Helper()
	oldSubscribe, oldFrames, oldJSON := debugSubscribe, debugSubscribeFrames, debugSubscribeJSON
	t.Cleanup(func() { debugSubscribe, debugSubscribeFrames, debugSubscribeJSON = oldSubscribe, oldFrames, oldJSON })
	var got protocol.SubscribeRequest
	debugSubscribe = func(_ context.Context, req protocol.SubscribeRequest) (protocol.SubscriptionAck, debugSubscription, error) {
		got = req
		if subErr != nil {
			return protocol.SubscriptionAck{}, nil, subErr
		}
		return protocol.SubscriptionAck{Protocol: 1, ServerProtocolMax: 1, SubscriberID: "sub-1", HeartbeatMS: 15000, Features: []string{"snapshot", "heartbeat"}}, sub, nil
	}
	debugSubscribeFrames, debugSubscribeJSON = frames, jsonOut
	var out bytes.Buffer
	debugSubscribeCmd.SetOut(&out)
	t.Cleanup(func() { debugSubscribeCmd.SetOut(nil) })
	err := runDebugSubscribe(debugSubscribeCmd, nil)
	return out.String(), got, err
}

func TestDebugSubscribeTextStopsAfterFrames(t *testing.T) {
	sub := &fakeSubscription{frames: []protocol.StreamFrame{
		snapshotFrame(t, 1, "bolt"),
		{V: 1, Seq: 2, Type: protocol.FrameHeartbeat},
		snapshotFrame(t, 3, "stop"),
		snapshotFrame(t, 4, "bolt"),
	}}
	out, req, err := runSubscribeForTest(t, 3, false, sub, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	first, third := snapshotFrame(t, 1, "bolt"), snapshotFrame(t, 3, "stop")
	want := "subscribed: sub-1 protocol=1 server_max=1 heartbeat=15000ms features=snapshot,heartbeat\n" +
		"#1 snapshot digest=" + frameDigest(first) + " ready=true kitty_ready=true sessions=1 attention=0 [1:bolt]\n" +
		"#2 heartbeat\n" +
		"#3 snapshot digest=" + frameDigest(third) + " ready=true kitty_ready=true sessions=1 attention=1 [1:stop*]\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	if !sub.closed {
		t.Fatal("subscription not closed")
	}
	if req.Protocol != protocol.CompanionProtocolVersion || req.Client != subscribeClientName || req.AMXBuild == nil {
		t.Fatalf("subscribe request = %+v", req)
	}
}

func TestDebugSubscribeEndsOnByeAndEOF(t *testing.T) {
	bye, _ := json.Marshal(protocol.Bye{Reason: protocol.ByeShutdown})
	sub := &fakeSubscription{frames: []protocol.StreamFrame{
		snapshotFrame(t, 1),
		{V: 1, Seq: 2, Type: protocol.FrameBye, Data: bye},
	}}
	out, _, err := runSubscribeForTest(t, 0, false, sub, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(out, "#2 bye reason=shutdown\n") {
		t.Fatalf("output does not end with the bye:\n%s", out)
	}
}

func TestDebugSubscribeJSONLines(t *testing.T) {
	sub := &fakeSubscription{frames: []protocol.StreamFrame{snapshotFrame(t, 1, "key")}}
	out, _, err := runSubscribeForTest(t, 0, true, sub, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want ack + one frame line, got %d:\n%s", len(lines), out)
	}
	var ack, frame subscribeRecord
	if err := json.Unmarshal([]byte(lines[0]), &ack); err != nil || ack.SchemaVersion != subscribeSchemaVersion || ack.Ack == nil || ack.Frame != nil {
		t.Fatalf("ack line = %s (%v)", lines[0], err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &frame); err != nil || frame.Frame == nil || frame.Frame.Type != protocol.FrameSnapshot || frame.Ack != nil {
		t.Fatalf("frame line = %s (%v)", lines[1], err)
	}
}

func TestDebugSubscribeReportsRefusal(t *testing.T) {
	_, _, err := runSubscribeForTest(t, 0, false, nil, &ipc.SubscribeRefusedError{Code: "subscriber_limit"})
	var refused *ipc.SubscribeRefusedError
	if !errors.As(err, &refused) || refused.Code != "subscriber_limit" {
		t.Fatalf("err = %v; want a subscriber_limit refusal", err)
	}
}

func TestDebugSubscribeRejectsNegativeFrames(t *testing.T) {
	if _, _, err := runSubscribeForTest(t, -1, false, &fakeSubscription{}, nil); err == nil {
		t.Fatal("negative --frames accepted")
	}
}
