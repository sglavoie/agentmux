package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// subscribeAckDeadline bounds dialing and reading the ack. The stream itself
// has no read deadline: frames arrive at the daemon's pace, and a heartbeat is
// what tells a reader the daemon is still there.
var subscribeAckDeadline = 2 * time.Second

// SubscribeRefusedError is a subscribe the daemon answered and declined. Code
// is the daemon's Response.Error verbatim (subscriber_limit, shutting_down,
// or an unknown-request text from a daemon that offers no stream).
type SubscribeRefusedError struct {
	Code string
}

func (e *SubscribeRefusedError) Error() string {
	return "daemon refused subscription: " + e.Code
}

// Subscription is the client side of one companion stream.
type Subscription struct {
	Ack protocol.SubscriptionAck

	conn       net.Conn
	scanner    *bufio.Scanner
	stopCancel func() bool
}

// Subscribe opens a companion stream. The connection is closed when ctx is
// cancelled, which is how a caller abandons a Next that is waiting on frames.
func (c *Client) Subscribe(ctx context.Context, sub protocol.SubscribeRequest) (*Subscription, error) {
	if ctx == nil {
		return nil, errors.New("daemon request context is nil")
	}
	dialContext := c.dialContext
	if dialContext == nil {
		dialContext = defaultDialContext
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, subscribeAckDeadline)
	defer cancelDial()
	conn, err := dialContext(dialCtx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(err error) (*Subscription, error) {
		stopCancel()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	_ = conn.SetDeadline(time.Now().Add(subscribeAckDeadline))
	data, err := json.Marshal(protocol.Request{Type: protocol.TypeSubscribe, Subscribe: &sub})
	if err != nil {
		return fail(fmt.Errorf("encoding request: %w", err))
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fail(fmt.Errorf("sending request: %w", err))
	}
	scanner := bufio.NewScanner(conn)
	// A frame is at most CompanionFrameMaxBytes plus its newline.
	scanner.Buffer(make([]byte, 0, 64*1024), protocol.CompanionFrameMaxBytes+1)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fail(fmt.Errorf("reading subscription ack: %w", err))
		}
		return fail(errors.New("daemon closed connection without responding"))
	}
	var resp protocol.Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fail(fmt.Errorf("decoding subscription ack: %w", err))
	}
	if !resp.OK || resp.Subscription == nil {
		code := resp.Error
		if code == "" {
			code = "subscription not acknowledged"
		}
		return fail(&SubscribeRefusedError{Code: code})
	}
	_ = conn.SetDeadline(time.Time{})
	return &Subscription{Ack: *resp.Subscription, conn: conn, scanner: scanner, stopCancel: stopCancel}, nil
}

// Next blocks for the next frame. It returns io.EOF when the daemon closed the
// stream, after any bye frame has been returned.
func (s *Subscription) Next() (protocol.StreamFrame, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return protocol.StreamFrame{}, err
		}
		return protocol.StreamFrame{}, io.EOF
	}
	var frame protocol.StreamFrame
	if err := json.Unmarshal(s.scanner.Bytes(), &frame); err != nil {
		return protocol.StreamFrame{}, fmt.Errorf("decoding stream frame: %w", err)
	}
	return frame, nil
}

// Close ends the subscription from the client side.
func (s *Subscription) Close() error {
	s.stopCancel()
	return s.conn.Close()
}
