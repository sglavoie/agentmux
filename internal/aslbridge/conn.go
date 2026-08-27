package aslbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// errConnClosed is returned to every caller still waiting when a session ends.
// It carries no plan content: the cause is recorded once, as a sanitized
// reason, on the manager.
var errConnClosed = errors.New("asl bridge: connection closed")

// event is one unsolicited frame handed to the manager. The reader goroutine
// must never block on delivery, so the manager's handler is expected to do
// nothing but record and signal.
type event struct {
	Seq  uint64
	Name string
	Data json.RawMessage
}

type result struct {
	payload json.RawMessage
	err     error
}

// conn is one live protocol session over a child's stdio.
//
// A single reader goroutine owns stdout: it validates the server-first hello,
// correlates responses, and forwards events. Writes are serialized by a mutex
// so two concurrent callers cannot interleave halves of a frame. Anything on
// stdout that is not a well-formed protocol object closes the session, because
// stdout is protocol-only and a contaminated stream cannot be resynchronized.
type conn struct {
	stdin io.WriteCloser

	writeMu sync.Mutex

	mu       sync.Mutex
	pending  map[string]chan result
	closed   bool
	closeErr error

	seq uint64

	helloOnce sync.Once
	helloCh   chan Hello

	onEvent func(event)
	metrics *metrics

	readerDone chan struct{}
}

func newConn(stdin io.WriteCloser, stdout io.Reader, onEvent func(event), m *metrics) *conn {
	c := &conn{
		stdin:      stdin,
		pending:    make(map[string]chan result),
		helloCh:    make(chan Hello, 1),
		onEvent:    onEvent,
		metrics:    m,
		readerDone: make(chan struct{}),
	}
	go c.readLoop(stdout)
	return c
}

// waitHello blocks until the server-first hello arrives, the session fails, or
// ctx expires. Availability is published only after this returns cleanly.
func (c *conn) waitHello(ctx context.Context) (Hello, error) {
	select {
	case hello := <-c.helloCh:
		return hello, nil
	case <-c.readerDone:
		return Hello{}, c.err()
	case <-ctx.Done():
		return Hello{}, ctx.Err()
	}
}

// call sends one request and waits for its correlated response. It never
// retries: a caller that wants another attempt must decide that itself, which
// is what keeps an ambiguous mutation from being applied twice.
func (c *conn) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = errConnClosed
		}
		return nil, err
	}
	c.seq++
	id := strconv.FormatUint(c.seq, 10)
	ch := make(chan result, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(newOutbound(id, method, params)); err != nil {
		return nil, err
	}
	c.metrics.request(method)

	select {
	case res := <-ch:
		return res.payload, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readerDone:
		return nil, c.err()
	}
}

func (c *conn) write(req outboundRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("asl bridge: encoding %s request: %w", req.Method, err)
	}
	data = append(data, '\n')
	// The ceiling is enforced on write too: an oversize request would be
	// answered with frame-too-large after the child had already read part of a
	// line it cannot use.
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("asl bridge: %s request exceeds the %d byte frame ceiling", req.Method, MaxFrameBytes)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("asl bridge: writing %s request: %w", req.Method, err)
	}
	c.metrics.wrote(len(data))
	return nil
}

func (c *conn) readLoop(stdout io.Reader) {
	defer close(c.readerDone)
	reader := bufio.NewReaderSize(stdout, 64<<10)
	first := true
	for {
		line, err := readFrame(reader)
		if err != nil {
			c.closeWith(err)
			return
		}
		if line == nil {
			c.closeWith(io.EOF)
			return
		}
		if len(line) == 0 {
			continue
		}
		c.metrics.read(len(line))

		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			c.closeWith(contaminationError("non-JSON"))
			return
		}
		if f.Protocol != ProtocolVersion {
			c.closeWith(fmt.Errorf("asl bridge: child announced protocol %d, want %d", f.Protocol, ProtocolVersion))
			return
		}

		switch f.Type {
		case "hello":
			if !first {
				c.closeWith(contaminationError("duplicate hello"))
				return
			}
			c.helloOnce.Do(func() {
				c.helloCh <- Hello{
					Protocol:            f.Protocol,
					ASLVersion:          f.ASLVersion,
					ContractSetID:       f.ContractSetID,
					Capabilities:        f.Capabilities,
					Events:              f.Events,
					MutationMethods:     f.MutationMethods,
					HandshakeRequired:   f.HandshakeRequired,
					MaxFrameBytes:       f.MaxFrameBytes,
					PollIntervalSeconds: f.PollIntervalSeconds,
				}
			})
		case "response":
			if first {
				c.closeWith(fmt.Errorf("asl bridge: child sent a response before its hello"))
				return
			}
			c.deliver(f)
		case "event":
			if first {
				c.closeWith(fmt.Errorf("asl bridge: child sent an event before its hello"))
				return
			}
			if c.onEvent != nil {
				c.onEvent(event{Seq: f.Seq, Name: f.Event, Data: f.Data})
			}
		default:
			c.closeWith(contaminationError("unknown type"))
			return
		}
		first = false
	}
}

func (c *conn) deliver(f frame) {
	res := result{payload: f.Result}
	if !f.OK {
		if f.Error != nil {
			res.err = f.Error
		} else {
			res.err = &Error{Code: "internal-error", Message: "child refused the request without a reason"}
		}
		c.metrics.refusal()
	}
	c.mu.Lock()
	ch, ok := c.pending[f.ID]
	if ok {
		delete(c.pending, f.ID)
	}
	c.mu.Unlock()
	if !ok {
		// A response to a request whose caller already gave up. Dropping it is
		// correct; the id space is per-session so it cannot collide later.
		return
	}
	ch <- res
}

// closeWith ends the session and fails everything still waiting. The first
// cause wins so a teardown triggered by shutdown is not relabelled by the EOF
// it produces.
func (c *conn) closeWith(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if err == nil {
		err = errConnClosed
	}
	c.closeErr = err
	pending := c.pending
	c.pending = make(map[string]chan result)
	c.mu.Unlock()

	for _, ch := range pending {
		ch <- result{err: err}
	}
}

func (c *conn) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return c.closeErr
	}
	return errConnClosed
}

// fail ends the session with a cause the manager decided rather than the reader
// did. Closing stdin is the load-bearing half: closeWith alone only poisons this
// side, and the faults the manager detects — a foreign contract set, a result it
// cannot decode — are all things a child considers itself healthy after. Such a
// child writes nothing further and never closes stdout, so without the
// end-of-stream signal the reader stays parked and supervision waits forever for
// an exit that is not coming.
func (c *conn) fail(err error) {
	c.closeWith(err)
	_ = c.stdin.Close()
}

// shutdown closes stdin, which is the bridge's own end-of-stream signal. It
// deliberately does not wait for the reader: the reader is parked in a blocking
// read on the child's stdout, and a child that is mid-projection writes nothing
// and reads nothing for as long as that projection takes. Joining here would
// make every teardown cost a whole projection and defeat the grace period
// entirely. Callers terminate the child first, then awaitReader.
//
// It routes through fail so an earlier cause is preserved: closeWith keeps the
// first error, which is what stops a teardown from relabelling the mismatch that
// triggered it as a plain exit.
func (c *conn) shutdown() {
	c.fail(errConnClosed)
}

// awaitReader joins the reader goroutine, which returns once the child's stdout
// is at end of stream. It is only safe to call once the child is known to have
// exited.
func (c *conn) awaitReader() {
	<-c.readerDone
}

// readFrame reads one bounded NDJSON line, or nil at end of stream. An
// oversize line is never buffered past the ceiling: the bridge bounds its own
// frames to MaxFrameBytes, so anything longer means the stream is not what it
// claims to be and the session ends rather than resynchronizing.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxFrameBytes {
			return nil, fmt.Errorf("asl bridge: inbound frame exceeded the %d byte ceiling", MaxFrameBytes)
		}
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return trimEOL(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(buf) == 0 {
				return nil, nil
			}
			return trimEOL(buf), nil
		default:
			return nil, err
		}
	}
}

func trimEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
