package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// maxPayloadLogLen caps the number of payload bytes recorded in error logs to
// keep a single malformed request from spamming the log.
const maxPayloadLogLen = 256

// maxRequestPayloadBytes bounds the size of a single newline-delimited JSON
// request the daemon will accept. Requests larger than this are rejected with a
// "request too large" response rather than triggering bufio.Scanner's silent
// truncation (its default 64KB ceiling). 1 MiB is well above any realistic hook
// payload while still preventing a buggy or malicious client from forcing the
// daemon to buffer unbounded memory.
const maxRequestPayloadBytes = 1 << 20

// badPayloadLogInterval controls how often a cumulative malformed-payload
// milestone is logged at info level. Per-payload diagnostics already log at
// debug; the milestone surfaces sustained noise from a misbehaving client
// (e.g. a hook script emitting garbage) without spamming the log.
const badPayloadLogInterval = 100

// connDeadline bounds a single request/response handshake on the server side.
// Without it, a client that opens a connection but never sends data (slow
// loris, crashed hook script holding the fd open via inheritance, etc.) ties
// up a handler goroutine indefinitely, since Unix sockets do not time out
// quiescent peers. The window covers the client's full sendDeadline plus a
// margin for IPC scheduling. Exposed as a var so tests can shrink it without
// sleeping for production duration.
var connDeadline = 10 * time.Second

// Server listens on a Unix domain socket and dispatches requests to a Handler.
// Each connection carries exactly one request/response pair.
type Server struct {
	handler     *Handler
	ln          net.Listener
	badPayloads atomic.Uint64
	// wg tracks in-flight handleConn goroutines so shutdown can wait for them
	// to finish draining instead of returning while a request is still being
	// processed.
	wg sync.WaitGroup
	// onConnStart, when non-nil, is invoked synchronously inside handleConn
	// before any I/O occurs. Test-only hook used to deterministically observe
	// that a handler goroutine has registered with wg without polling.
	onConnStart func()
}

// NewServer creates a Server bound to socketPath.
// The socket file is removed if it already exists (stale socket from a prior crash).
func NewServer(socketPath string, handler *Handler) (*Server, error) {
	// Remove stale socket.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	return &Server{handler: handler, ln: ln}, nil
}

// Serve accepts connections until ctx is cancelled or the listener is closed.
// On return, Serve blocks until every in-flight handleConn goroutine has
// finished so callers can rely on the daemon being fully drained before the
// socket file is removed.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()

	defer s.wg.Wait()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// Close shuts down the listener and waits for any in-flight handlers to
// finish. It is safe to call Close after Serve has already returned.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()
	if s.onConnStart != nil {
		s.onConnStart()
	}
	// Bound the entire request/response handshake so a client that connects
	// without sending data (or stops reading mid-response) cannot pin this
	// goroutine for the daemon's lifetime.
	_ = conn.SetDeadline(time.Now().Add(connDeadline))
	s.serveConn(conn, conn)
}

// serveConn processes a single request/response pair on rw. It is split out
// from handleConn so tests can drive it with fake readers and writers.
func (s *Server) serveConn(r io.Reader, w io.Writer) {
	var req Request
	scanner := bufio.NewScanner(r)
	// Raise the scan buffer ceiling above bufio's 64 KiB default so larger
	// hook payloads (e.g. long notification messages) do not silently fail
	// with bufio.ErrTooLong. Oversize requests surface explicitly below.
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestPayloadBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				count := s.badPayloads.Add(1)
				slog.Warn("daemon: request payload exceeds limit", "limit", maxRequestPayloadBytes, "bad_payload_count", count)
				writeResponse(w, Response{OK: false, Error: "request payload exceeds limit"})
				return
			}
			slog.Warn("daemon: read failed", "err", err)
		}
		return
	}
	payload := scanner.Bytes()
	if err := json.Unmarshal(payload, &req); err != nil {
		count := s.badPayloads.Add(1)
		slog.Debug("daemon: bad request JSON", "err", err, "payload", truncateForLog(payload, maxPayloadLogLen), "bad_payload_count", count)
		if count%badPayloadLogInterval == 0 {
			slog.Info("daemon: cumulative malformed payloads received", "count", count)
		}
		writeResponse(w, Response{OK: false, Error: "invalid JSON"})
		return
	}

	resp := s.handler.Handle(req)
	writeResponse(w, resp)
}

func writeResponse(w io.Writer, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("daemon: response marshal failed", "err", err)
		return
	}
	data = append(data, '\n')
	for written := 0; written < len(data); {
		n, err := w.Write(data[written:])
		if err != nil {
			slog.Warn("daemon: response write failed", "err", err, "written", written, "total", len(data))
			return
		}
		if n <= 0 {
			slog.Warn("daemon: response write made no progress", "written", written, "total", len(data))
			return
		}
		written += n
	}
}

// truncateForLog returns a quoted, length-bounded form of b suitable for log
// fields. Bytes beyond max are dropped and a "...(truncated)" marker appended.
func truncateForLog(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}
