package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestClientSubscribe_AckFramesAndEOF(t *testing.T) {
	handler := newSubscribingHandler()
	handler.onSubscribe = func(sink *StreamSink) { sink.SetSnapshot(json.RawMessage(`{"v":1}`)) }
	server, socketPath := startSubscriptionServer(t, handler)

	sub, err := NewClientWithSocket(socketPath).Subscribe(context.Background(), protocol.SubscribeRequest{Protocol: 1, Client: "test"})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = sub.Close() }()
	if sub.Ack.SubscriberID == "" || sub.Ack.Protocol != protocol.CompanionProtocolVersion {
		t.Fatalf("ack = %+v", sub.Ack)
	}
	frame, err := sub.Next()
	if err != nil || frame.Type != protocol.FrameSnapshot || string(frame.Data) != `{"v":1}` {
		t.Fatalf("first frame = %+v, %v", frame, err)
	}

	server.Close()
	frame, err = sub.Next()
	if err != nil || frame.Type != protocol.FrameBye {
		t.Fatalf("after close: frame = %+v, %v; want bye", frame, err)
	}
	if _, err := sub.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after bye: err = %v; want EOF", err)
	}
}

func TestClientSubscribe_RefusedByHandlerWithoutStream(t *testing.T) {
	_, socketPath := startSubscriptionServer(t, newTestHandler())
	_, err := NewClientWithSocket(socketPath).Subscribe(context.Background(), protocol.SubscribeRequest{Protocol: 1})
	var refused *SubscribeRefusedError
	if !errors.As(err, &refused) || refused.Code != "unknown request type: subscribe" {
		t.Fatalf("err = %v; want an unknown-request refusal", err)
	}
}

func TestClientSubscribe_ContextCancelEndsNext(t *testing.T) {
	handler := newSubscribingHandler()
	_, socketPath := startSubscriptionServer(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := NewClientWithSocket(socketPath).Subscribe(ctx, protocol.SubscribeRequest{Protocol: 1})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = sub.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := sub.Next()
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Next returned a frame after cancel; want an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not return after the context was cancelled")
	}
}
