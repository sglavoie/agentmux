package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// subscribingHandler is testHandler plus the optional SubscriptionHandler
// half. Every accepted sink is published on sinks so a test can drive the
// publisher side; onSubscribe, when set, runs inside Subscribe (the replay
// point) before the ack is returned.
type subscribingHandler struct {
	*testHandler
	sinks       chan *StreamSink
	onSubscribe func(*StreamSink)
}

func newSubscribingHandler() *subscribingHandler {
	return &subscribingHandler{testHandler: newTestHandler(), sinks: make(chan *StreamSink, maxSubscribers+4)}
}

func (h *subscribingHandler) Subscribe(_ protocol.Request, sink *StreamSink) protocol.Response {
	if h.onSubscribe != nil {
		h.onSubscribe(sink)
	}
	h.sinks <- sink
	return protocol.Response{OK: true, Subscription: &protocol.SubscriptionAck{
		Protocol:          protocol.CompanionProtocolVersion,
		ServerProtocolMax: protocol.CompanionProtocolVersion,
	}}
}

func (h *subscribingHandler) nextSink(t *testing.T) *StreamSink {
	t.Helper()
	select {
	case sink := <-h.sinks:
		return sink
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never asked to subscribe")
		return nil
	}
}

// startSubscriptionServer serves handler on a fresh socket. It records the
// goroutine baseline before the server exists and, at cleanup, closes the
// server and asserts every transport goroutine is gone again.
func startSubscriptionServer(t *testing.T, handler RequestHandler) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-sub-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	baseline := runtime.NumGoroutine()
	socketPath := filepath.Join(dir, "d.sock")
	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(context.Background())
		close(serveDone)
	}()
	t.Cleanup(func() {
		server.Close()
		<-serveDone
		_ = os.RemoveAll(dir)
		assertNoGoroutineLeak(t, baseline)
	})
	return server, socketPath
}

func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var final int
	for time.Now().Before(deadline) {
		runtime.GC()
		final = runtime.NumGoroutine()
		if final <= baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	t.Fatalf("goroutines leaked: baseline=%d final=%d\n%s", baseline, final, buf[:n])
}

type subscriberClient struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func dialSubscriber(t *testing.T, socketPath string) *subscriberClient {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	line, _ := json.Marshal(protocol.Request{Type: protocol.TypeSubscribe, Subscribe: &protocol.SubscribeRequest{Protocol: protocol.CompanionProtocolVersion}})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*protocol.CompanionFrameMaxBytes)
	return &subscriberClient{conn: conn, scanner: scanner}
}

func (c *subscriberClient) readLine(t *testing.T) []byte {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !c.scanner.Scan() {
		t.Fatalf("stream ended early: %v", c.scanner.Err())
	}
	return c.scanner.Bytes()
}

func (c *subscriberClient) readAck(t *testing.T) protocol.Response {
	t.Helper()
	var resp protocol.Response
	if err := json.Unmarshal(c.readLine(t), &resp); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	return resp
}

func (c *subscriberClient) readFrame(t *testing.T) protocol.StreamFrame {
	t.Helper()
	var frame protocol.StreamFrame
	if err := json.Unmarshal(c.readLine(t), &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return frame
}

// expectEOF reads until the server closes the stream.
func (c *subscriberClient) expectEOF(t *testing.T) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for c.scanner.Scan() {
	}
	if err := c.scanner.Err(); err != nil {
		t.Fatalf("stream did not end with EOF: %v", err)
	}
}

func byeReason(t *testing.T, frame protocol.StreamFrame) string {
	t.Helper()
	if frame.Type != protocol.FrameBye {
		t.Fatalf("frame type = %q; want bye", frame.Type)
	}
	var bye protocol.Bye
	if err := json.Unmarshal(frame.Data, &bye); err != nil {
		t.Fatalf("decode bye: %v", err)
	}
	return bye.Reason
}

func waitDone(t *testing.T, sink *StreamSink) {
	t.Helper()
	select {
	case <-sink.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not end")
	}
}

func TestSubscribe_ReplaysSnapshotOnConnect(t *testing.T) {
	handler := newSubscribingHandler()
	handler.onSubscribe = func(sink *StreamSink) { sink.SetSnapshot(json.RawMessage(`{"v":1,"seed":true}`)) }
	_, socketPath := startSubscriptionServer(t, handler)

	client := dialSubscriber(t, socketPath)
	ack := client.readAck(t)
	if !ack.OK || ack.Subscription == nil {
		t.Fatalf("ack = %+v; want an OK subscription", ack)
	}
	sink := handler.nextSink(t)
	if ack.Subscription.SubscriberID != sink.ID() || sink.ID() == "" {
		t.Fatalf("ack subscriber_id = %q; want the sink's %q", ack.Subscription.SubscriberID, sink.ID())
	}
	frame := client.readFrame(t)
	if frame.Type != protocol.FrameSnapshot || frame.Seq != 1 || frame.V != protocol.CompanionProtocolVersion {
		t.Fatalf("first frame = %+v; want snapshot seq 1", frame)
	}
	if string(frame.Data) != `{"v":1,"seed":true}` {
		t.Fatalf("snapshot data = %s", frame.Data)
	}

	// Frames after the replay arrive in order with advancing sequence.
	if !sink.Send(protocol.FrameHeartbeat, nil) {
		t.Fatal("Send refused on a live subscription")
	}
	if frame := client.readFrame(t); frame.Type != protocol.FrameHeartbeat || frame.Seq != 2 {
		t.Fatalf("second frame = %+v; want heartbeat seq 2", frame)
	}
}

// gatedConn is a streamConn whose Write blocks until released, so a test can
// hold the writer mid-frame while it fills the snapshot slot or the queue.
type gatedConn struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	writing chan struct{} // receives once per Write entry
	release chan struct{} // each receive lets one Write finish
	closed  chan struct{}
	once    sync.Once
}

func newGatedConn() *gatedConn {
	return &gatedConn{writing: make(chan struct{}, 256), release: make(chan struct{}, 256), closed: make(chan struct{})}
}

func (c *gatedConn) Write(p []byte) (int, error) {
	c.writing <- struct{}{}
	select {
	case <-c.release:
	case <-c.closed:
		return 0, io.ErrClosedPipe
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *gatedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *gatedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *gatedConn) SetWriteDeadline(time.Time) error { return nil }

func (c *gatedConn) frames(t *testing.T) []protocol.StreamFrame {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var frames []protocol.StreamFrame
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var frame protocol.StreamFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func (c *gatedConn) awaitWrite(t *testing.T) {
	t.Helper()
	select {
	case <-c.writing:
	case <-time.After(2 * time.Second):
		t.Fatal("writer never wrote")
	}
}

func runWriter(s *Server, sink *StreamSink, conn streamConn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.streamFrames(sink, conn)
	}()
	return done
}

func TestStreamSink_SnapshotIsLatestWins(t *testing.T) {
	s := newTestServer()
	sink := newStreamSink("sub-test", &s.subscriptionMetrics)
	conn := newGatedConn()
	writerDone := runWriter(s, sink, conn)

	sink.SetSnapshot(json.RawMessage(`"a"`))
	conn.awaitWrite(t) // the writer holds "a" mid-write
	sink.SetSnapshot(json.RawMessage(`"b"`))
	sink.SetSnapshot(json.RawMessage(`"c"`))
	conn.release <- struct{}{}
	conn.awaitWrite(t)
	conn.release <- struct{}{}

	sink.end("")
	<-writerDone
	frames := conn.frames(t)
	if len(frames) != 2 || string(frames[0].Data) != `"a"` || string(frames[1].Data) != `"c"` {
		t.Fatalf("frames = %+v; want a then c, with b coalesced away", frames)
	}
	if frames[1].Seq != 2 {
		t.Fatalf("coalesced snapshot seq = %d; want 2", frames[1].Seq)
	}
}

func TestStreamSink_SnapshotDrainsBeforeQueue(t *testing.T) {
	s := newTestServer()
	sink := newStreamSink("sub-test", &s.subscriptionMetrics)
	conn := newGatedConn()
	writerDone := runWriter(s, sink, conn)

	sink.Send(protocol.FrameHeartbeat, nil)
	conn.awaitWrite(t)
	sink.Send(protocol.FrameNotification, json.RawMessage(`{}`))
	sink.SetSnapshot(json.RawMessage(`"snap"`))
	for i := 0; i < 3; i++ {
		conn.release <- struct{}{}
		if i < 2 {
			conn.awaitWrite(t)
		}
	}
	sink.end("")
	<-writerDone
	frames := conn.frames(t)
	var types []protocol.StreamFrameType
	for _, f := range frames {
		types = append(types, f.Type)
	}
	want := []protocol.StreamFrameType{protocol.FrameHeartbeat, protocol.FrameSnapshot, protocol.FrameNotification}
	if len(types) != len(want) {
		t.Fatalf("frame types = %v; want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("frame types = %v; want %v", types, want)
		}
	}
}

func TestStreamSink_OverflowSendsSlowConsumerBye(t *testing.T) {
	s := newTestServer()
	sink := newStreamSink("sub-test", &s.subscriptionMetrics)
	conn := newGatedConn()
	writerDone := runWriter(s, sink, conn)

	sink.Send(protocol.FrameHeartbeat, nil)
	conn.awaitWrite(t) // the writer holds frame 1; the queue is empty
	for i := 0; i < streamQueueSize; i++ {
		if !sink.Send(protocol.FrameHeartbeat, nil) {
			t.Fatalf("Send %d refused before the queue was full", i)
		}
	}
	if sink.Send(protocol.FrameHeartbeat, nil) {
		t.Fatal("Send past the queue bound was accepted")
	}
	waitDone(t, sink)
	if sink.Send(protocol.FrameHeartbeat, nil) {
		t.Fatal("Send after the end was accepted")
	}

	conn.release <- struct{}{} // finish frame 1
	conn.awaitWrite(t)         // the bye
	conn.release <- struct{}{}
	<-writerDone

	frames := conn.frames(t)
	if len(frames) != 2 {
		t.Fatalf("frames = %+v; want the in-flight frame then a bye, and no queued frame", frames)
	}
	if reason := byeReason(t, frames[1]); reason != protocol.ByeSlowConsumer {
		t.Fatalf("bye reason = %q; want %q", reason, protocol.ByeSlowConsumer)
	}
	if got := s.Metrics().SubscribersSlowConsumer; got != 1 {
		t.Fatalf("slow consumer count = %d; want 1", got)
	}
}

func TestSubscribe_OverflowDisconnects(t *testing.T) {
	prev := streamWriteDeadline
	streamWriteDeadline = 200 * time.Millisecond
	t.Cleanup(func() { streamWriteDeadline = prev })

	handler := newSubscribingHandler()
	server, socketPath := startSubscriptionServer(t, handler)
	client := dialSubscriber(t, socketPath)
	if ack := client.readAck(t); !ack.OK {
		t.Fatalf("ack = %+v", ack)
	}
	sink := handler.nextSink(t)

	// The client reads nothing. Large frames fill the socket buffer, park the
	// writer, and then the queue; the stream must end rather than block Send.
	payload := json.RawMessage(`"` + strings.Repeat("x", 64*1024) + `"`)
	for i := 0; i < 4*streamQueueSize && sink.Send(protocol.FrameNotification, payload); i++ {
	}
	waitDone(t, sink)
	if got := server.Metrics().SubscribersSlowConsumer; got != 1 {
		t.Fatalf("slow consumer count = %d; want 1", got)
	}
	client.expectEOF(t)
	deadline := time.Now().Add(2 * time.Second)
	for server.Metrics().SubscribersActive != 0 {
		if time.Now().After(deadline) {
			t.Fatal("slow subscriber was never unregistered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubscribe_SubscriberLimit(t *testing.T) {
	handler := newSubscribingHandler()
	server, socketPath := startSubscriptionServer(t, handler)
	for i := 0; i < maxSubscribers; i++ {
		client := dialSubscriber(t, socketPath)
		if ack := client.readAck(t); !ack.OK {
			t.Fatalf("subscriber %d refused: %+v", i, ack)
		}
	}
	extra := dialSubscriber(t, socketPath)
	resp := extra.readAck(t)
	if resp.OK || resp.Error != "subscriber_limit" {
		t.Fatalf("subscriber %d response = %+v; want subscriber_limit", maxSubscribers+1, resp)
	}
	extra.expectEOF(t)
	metrics := server.Metrics()
	if metrics.SubscribersActive != maxSubscribers || metrics.SubscribersRejected != 1 {
		t.Fatalf("metrics active=%d rejected=%d; want %d and 1", metrics.SubscribersActive, metrics.SubscribersRejected, maxSubscribers)
	}
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o; want 600", perm)
	}
}

func TestSubscribe_CloseEndsLiveSubscribersPromptly(t *testing.T) {
	handler := newSubscribingHandler()
	server, socketPath := startSubscriptionServer(t, handler)
	var clients []*subscriberClient
	for i := 0; i < 3; i++ {
		client := dialSubscriber(t, socketPath)
		if ack := client.readAck(t); !ack.OK {
			t.Fatalf("ack = %+v", ack)
		}
		clients = append(clients, client)
	}

	closed := make(chan struct{})
	go func() {
		server.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on live subscribers")
	}
	for _, client := range clients {
		if reason := byeReason(t, client.readFrame(t)); reason != protocol.ByeShutdown {
			t.Fatalf("bye reason = %q; want %q", reason, protocol.ByeShutdown)
		}
		client.expectEOF(t)
	}
	if got := server.Metrics().SubscribersActive; got != 0 {
		t.Fatalf("active subscribers after Close = %d; want 0", got)
	}
}

func TestSubscribe_ServeCancellationEndsSubscribers(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-subcancel-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	handler := newSubscribingHandler()
	socketPath := filepath.Join(dir, "d.sock")
	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(ctx)
		close(serveDone)
	}()
	client := dialSubscriber(t, socketPath)
	if ack := client.readAck(t); !ack.OK {
		t.Fatalf("ack = %+v", ack)
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve did not drain a live subscriber after cancellation")
	}
	if reason := byeReason(t, client.readFrame(t)); reason != protocol.ByeShutdown {
		t.Fatalf("bye reason = %q; want %q", reason, protocol.ByeShutdown)
	}
}

func TestSubscribe_PeerCloseEndsSubscription(t *testing.T) {
	handler := newSubscribingHandler()
	server, socketPath := startSubscriptionServer(t, handler)
	client := dialSubscriber(t, socketPath)
	if ack := client.readAck(t); !ack.OK {
		t.Fatalf("ack = %+v", ack)
	}
	sink := handler.nextSink(t)
	_ = client.conn.Close()
	waitDone(t, sink)
	deadline := time.Now().Add(2 * time.Second)
	for server.Metrics().SubscribersActive != 0 {
		if time.Now().After(deadline) {
			t.Fatal("closed subscriber was never unregistered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubscribe_OneShotRequestsUnaffected(t *testing.T) {
	handler := newSubscribingHandler()
	_, socketPath := startSubscriptionServer(t, handler)
	client := dialSubscriber(t, socketPath)
	if ack := client.readAck(t); !ack.OK {
		t.Fatalf("ack = %+v", ack)
	}
	sink := handler.nextSink(t)

	// A line written on the stream is not a second request.
	line, _ := json.Marshal(protocol.Request{Type: protocol.TypeHealth})
	if _, err := client.conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("write on stream: %v", err)
	}

	// A one-shot request on its own connection still gets exactly one answer
	// while the subscription is live.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	data, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(bytes.TrimSpace(data), &resp); err != nil || !resp.OK {
		t.Fatalf("one-shot response = %q (%v)", data, err)
	}

	sink.Send(protocol.FrameHeartbeat, nil)
	if frame := client.readFrame(t); frame.Type != protocol.FrameHeartbeat {
		t.Fatalf("frame = %+v; want heartbeat", frame)
	}
	handler.mu.Lock()
	handled := len(handler.requests)
	handler.mu.Unlock()
	if handled != 1 {
		t.Fatalf("Handle called %d times; want 1 (the stream line must be ignored)", handled)
	}
}

func TestSubscribe_HandlerWithoutSubscriptionIsUnknownType(t *testing.T) {
	handler := newTestHandler()
	_, socketPath := startSubscriptionServer(t, handler)
	client := dialSubscriber(t, socketPath)
	resp := client.readAck(t)
	if resp.OK || resp.Error != "unknown request type: subscribe" {
		t.Fatalf("response = %+v; want unknown request type", resp)
	}
	client.expectEOF(t)
	if len(handler.requests) != 0 {
		t.Fatal("subscribe reached Handle")
	}
}

func TestSubscribe_OversizeFrameIsDroppedAndCounted(t *testing.T) {
	handler := newSubscribingHandler()
	big := json.RawMessage(`"` + strings.Repeat("x", protocol.CompanionFrameMaxBytes) + `"`)
	handler.onSubscribe = func(sink *StreamSink) { sink.SetSnapshot(big) }
	server, socketPath := startSubscriptionServer(t, handler)
	client := dialSubscriber(t, socketPath)
	if ack := client.readAck(t); !ack.OK {
		t.Fatalf("ack = %+v", ack)
	}
	sink := handler.nextSink(t)
	// The stream keeps only the newest pending snapshot. Wait until the writer
	// has inspected the oversized one before replacing it with the small one.
	deadline := time.Now().Add(5 * time.Second)
	for server.Metrics().StreamFramesOversize == 0 {
		if time.Now().After(deadline) {
			t.Fatal("oversized snapshot was not inspected by the stream writer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	sink.SetSnapshot(json.RawMessage(`"small"`))
	frame := client.readFrame(t)
	if frame.Type != protocol.FrameSnapshot || string(frame.Data) != `"small"` || frame.Seq != 1 {
		t.Fatalf("frame = %+v; want the small snapshot as seq 1", frame)
	}
	if got := server.Metrics().StreamFramesOversize; got != 1 {
		t.Fatalf("oversize count = %d; want 1", got)
	}
	if got := server.Metrics().StreamFramesWritten; got != 1 {
		t.Fatalf("frames written = %d; want 1", got)
	}
}
