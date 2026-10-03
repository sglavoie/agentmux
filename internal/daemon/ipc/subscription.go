package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// The subscription transport is the one exception to "one request, one
// response, one connection". A subscribe request is answered with an ack line
// and then the connection carries server-to-client StreamFrames only, until a
// bye or EOF. It never carries a second request: whatever the client writes
// after its subscribe line is read and discarded, solely so that EOF can be
// seen.
//
// Everything here is bounded so a subscriber cannot make the daemon buffer or
// wait without limit: a snapshot slot that keeps only the latest snapshot, a
// fixed queue for every other frame, a per-frame write deadline, a frame-size
// ceiling, and a cap on concurrent subscribers. A subscriber that cannot keep
// up is told so and disconnected rather than slowing the publisher down.

const (
	// maxSubscribers bounds concurrent subscriptions. A companion app holds
	// one; the headroom covers reconnect overlap and a diagnostic observer.
	maxSubscribers = 8
	// streamQueueSize bounds the non-snapshot frames waiting for one
	// subscriber's writer. Overflowing it disconnects that subscriber.
	streamQueueSize = 64
	// errSubscriberLimit is the Response.Error a subscribe beyond
	// maxSubscribers receives.
	errSubscriberLimit = "subscriber_limit"
	// errSubscriberShutdown answers a subscribe that raced daemon shutdown.
	errSubscriberShutdown = "shutting_down"
)

// streamWriteDeadline bounds writing one frame. A subscriber that cannot drain
// a single frame in this long is treated as a slow consumer. It is a variable
// so tests can shorten it.
var streamWriteDeadline = 2 * time.Second

// SubscriptionHandler is the optional half of the application a Server
// dispatches subscribe requests to. It sits beside RequestHandler rather than
// inside it, so a handler that offers no stream keeps compiling unchanged and
// its peers are answered "unknown request type".
//
// Subscribe decides whether to accept req and returns the response line that
// opens the stream: a Response with OK set and a Subscription ack accepts, any
// other response refuses and is written as-is before the connection closes.
// Subscribe must not block; it may seed the sink (typically with SetSnapshot,
// so a new subscriber is replayed the current state before anything else) and
// keep it to publish to later. The sink stops accepting frames once Done is
// closed, which is the handler's signal to forget it.
type SubscriptionHandler interface {
	Subscribe(req protocol.Request, sink *StreamSink) protocol.Response
}

// StreamSink is the publisher's side of one subscription. Every method is
// safe for concurrent use and none of them blocks.
type StreamSink struct {
	id      string
	metrics *subscriptionMetrics

	mu       sync.Mutex
	snapshot json.RawMessage
	hasSnap  bool
	reason   string
	ended    bool

	queue chan queuedFrame
	// wake is a one-slot doorbell rung when the snapshot slot fills.
	wake chan struct{}
	done chan struct{}
}

type queuedFrame struct {
	typ  protocol.StreamFrameType
	data json.RawMessage
}

func newStreamSink(id string, metrics *subscriptionMetrics) *StreamSink {
	return &StreamSink{
		id:      id,
		metrics: metrics,
		queue:   make(chan queuedFrame, streamQueueSize),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// ID is the subscriber identifier the ack reports.
func (s *StreamSink) ID() string { return s.id }

// Done is closed when the subscription has ended, for any reason.
func (s *StreamSink) Done() <-chan struct{} { return s.done }

// SetSnapshot replaces the pending snapshot. Only the latest one is kept: a
// snapshot superseded before the writer reached it is never written, because
// a snapshot is the whole state and an older one has nothing the newer lacks.
// The writer drains this slot before the frame queue.
func (s *StreamSink) SetSnapshot(data json.RawMessage) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.snapshot = data
	s.hasSnap = true
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Send enqueues one non-snapshot frame without blocking. It reports false when
// the subscription has ended or the frame did not fit; a full queue ends the
// subscription as a slow consumer, because a frame silently lost from the
// middle of a stream is worse than a reconnect that replays the snapshot.
func (s *StreamSink) Send(typ protocol.StreamFrameType, data json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return false
	}
	select {
	case s.queue <- queuedFrame{typ: typ, data: data}:
		return true
	default:
		s.endLocked(protocol.ByeSlowConsumer)
		return false
	}
}

// end stops the subscription with reason, which becomes the bye the writer
// sends; an empty reason closes without one. The first reason wins.
func (s *StreamSink) end(reason string) {
	s.mu.Lock()
	s.endLocked(reason)
	s.mu.Unlock()
}

func (s *StreamSink) endLocked(reason string) {
	if s.ended {
		return
	}
	s.ended = true
	s.reason = reason
	s.snapshot = nil
	s.hasSnap = false
	if reason == protocol.ByeSlowConsumer {
		s.metrics.slowConsumer.Add(1)
	}
	close(s.done)
}

// endReason reports the reason the subscription ended with.
func (s *StreamSink) endReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

func (m *subscriptionMetrics) fill(snap *protocol.MetricsSnapshot) {
	snap.SubscribersActive = m.active.Load()
	snap.SubscribersRejected = m.rejected.Load()
	snap.SubscribersSlowConsumer = m.slowConsumer.Load()
	snap.StreamFramesWritten = m.framesWritten.Load()
	snap.StreamFramesOversize = m.oversize.Load()
}

// takeSnapshot empties the snapshot slot.
func (s *StreamSink) takeSnapshot() (json.RawMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasSnap || s.ended {
		return nil, false
	}
	data := s.snapshot
	s.snapshot = nil
	s.hasSnap = false
	return data, true
}

// subscriptionMetrics are the transport's subscription counters: subscribers
// streaming now, subscribes refused, subscriptions ended as slow consumers,
// frames written, and frames dropped for exceeding the frame cap.
type subscriptionMetrics struct {
	active        atomic.Int64
	rejected      atomic.Uint64
	slowConsumer  atomic.Uint64
	framesWritten atomic.Uint64
	oversize      atomic.Uint64
}

// subscriberRegistry admits subscriptions up to maxSubscribers and ends every
// one of them at shutdown. Once closed it admits nothing, so a connection that
// was already registered with the server's wait group cannot start a stream
// after Close has cancelled the ones it knew about.
type subscriberRegistry struct {
	metrics *subscriptionMetrics

	mu     sync.Mutex
	closed bool
	nextID uint64
	sinks  map[*StreamSink]struct{}
}

func newSubscriberRegistry(metrics *subscriptionMetrics) *subscriberRegistry {
	return &subscriberRegistry{metrics: metrics, sinks: make(map[*StreamSink]struct{})}
}

// register returns a new sink, or the refusal to answer the subscriber with.
func (r *subscriberRegistry) register() (*StreamSink, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		r.metrics.rejected.Add(1)
		return nil, errSubscriberShutdown
	}
	if len(r.sinks) >= maxSubscribers {
		r.metrics.rejected.Add(1)
		return nil, errSubscriberLimit
	}
	r.nextID++
	sink := newStreamSink("sub-"+strconv.FormatUint(r.nextID, 10), r.metrics)
	r.sinks[sink] = struct{}{}
	r.metrics.active.Add(1)
	return sink, ""
}

func (r *subscriberRegistry) unregister(sink *StreamSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sinks[sink]; ok {
		delete(r.sinks, sink)
		r.metrics.active.Add(-1)
	}
}

// close refuses every later registration and ends every live subscription
// with a shutdown bye. It does not wait for the writers; the server's wait
// group does that.
func (r *subscriberRegistry) close() {
	r.mu.Lock()
	r.closed = true
	sinks := make([]*StreamSink, 0, len(r.sinks))
	for sink := range r.sinks {
		sinks = append(sinks, sink)
	}
	r.mu.Unlock()
	for _, sink := range sinks {
		sink.end(protocol.ByeShutdown)
	}
}

// streamConn is what a subscription needs from its connection beyond
// io.Writer: per-direction deadlines and a way to unblock the EOF reader.
// net.Conn satisfies it; a test writer that does not is refused a stream.
type streamConn interface {
	io.Writer
	io.Closer
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// serveSubscription answers a decoded subscribe request on w and, if the
// handler accepts it, streams frames until the subscription ends. scanner is
// the request reader, which may already hold buffered bytes past the request
// line; it is drained only to observe EOF.
func (s *Server) serveSubscription(req protocol.Request, scanner *bufio.Scanner, w io.Writer) {
	subscriber, ok := s.handler.(SubscriptionHandler)
	if !ok {
		writeResponse(w, protocol.Response{OK: false, Error: "unknown request type: " + string(req.Type)})
		return
	}
	conn, ok := w.(streamConn)
	if !ok {
		writeResponse(w, protocol.Response{OK: false, Error: "subscribe requires a stream connection"})
		return
	}
	sink, refusal := s.subscribers.register()
	if sink == nil {
		writeResponse(w, protocol.Response{OK: false, Error: refusal})
		return
	}
	defer s.subscribers.unregister(sink)
	defer sink.end("")

	resp := subscriber.Subscribe(req, sink)
	if !resp.OK || resp.Subscription == nil {
		if resp.OK {
			resp = protocol.Response{OK: false, Error: "subscription not acknowledged"}
		}
		writeResponse(w, resp)
		return
	}
	if resp.Subscription.SubscriberID == "" {
		resp.Subscription.SubscriberID = sink.ID()
	}
	_ = conn.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
	writeResponse(w, resp)

	// From here the stream lives until it ends; only writes are bounded.
	_ = conn.SetReadDeadline(time.Time{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for scanner.Scan() {
			// Client lines are not requests on a stream; discard them.
		}
		sink.end("")
	}()

	s.streamFrames(sink, conn)
	// Closing the connection unblocks the reader if the client is still
	// connected; the reader must be gone before this goroutine leaves wg.
	_ = conn.Close()
	<-readerDone
}

// streamFrames is the writer loop: the snapshot slot first, then the queue,
// until the sink ends. A slow-consumer or shutdown end is announced with a
// best-effort bye.
func (s *Server) streamFrames(sink *StreamSink, conn streamConn) {
	var seq uint64
	write := func(typ protocol.StreamFrameType, data json.RawMessage) bool {
		line, err := json.Marshal(protocol.StreamFrame{V: protocol.CompanionProtocolVersion, Seq: seq + 1, Type: typ, Data: data})
		if err != nil {
			slog.Warn("daemon: stream frame marshal failed", "subscriber", sink.ID(), "type", typ, "err", err)
			return true
		}
		if len(line) > protocol.CompanionFrameMaxBytes {
			s.subscriptionMetrics.oversize.Add(1)
			slog.Warn("daemon: stream frame exceeds limit; dropped", "subscriber", sink.ID(), "type", typ, "bytes", len(line), "limit", protocol.CompanionFrameMaxBytes)
			return true
		}
		line = append(line, '\n')
		_ = conn.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
		if _, err := conn.Write(line); err != nil {
			if isTimeout(err) {
				sink.end(protocol.ByeSlowConsumer)
			} else {
				sink.end("")
			}
			slog.Debug("daemon: stream write failed", "subscriber", sink.ID(), "err", err)
			return false
		}
		seq++
		s.subscriptionMetrics.framesWritten.Add(1)
		return true
	}

	for {
		select {
		case <-sink.Done():
			s.writeBye(sink, conn, seq+1)
			return
		default:
		}
		if data, ok := sink.takeSnapshot(); ok {
			if !write(protocol.FrameSnapshot, data) {
				return
			}
			continue
		}
		select {
		case <-sink.Done():
			s.writeBye(sink, conn, seq+1)
			return
		case <-sink.wake:
		case f := <-sink.queue:
			if !write(f.typ, f.data) {
				return
			}
		}
	}
}

// writeBye sends the bye for a deliberate end. A write that failed or timed
// out already lost the connection, and a peer EOF has nobody to tell, so both
// close silently.
func (s *Server) writeBye(sink *StreamSink, conn streamConn, seq uint64) {
	reason := sink.endReason()
	if reason == "" {
		return
	}
	data, _ := json.Marshal(protocol.Bye{Reason: reason})
	line, err := json.Marshal(protocol.StreamFrame{V: protocol.CompanionProtocolVersion, Seq: seq, Type: protocol.FrameBye, Data: data})
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
	if _, err := conn.Write(append(line, '\n')); err == nil {
		s.subscriptionMetrics.framesWritten.Add(1)
	}
}

func isTimeout(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}
