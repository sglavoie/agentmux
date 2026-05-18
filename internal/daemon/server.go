package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

// maxPayloadLogLen caps the number of payload bytes recorded in error logs to
// keep a single malformed request from spamming the log.
const maxPayloadLogLen = 256

// badPayloadLogInterval controls how often a cumulative malformed-payload
// milestone is logged at info level. Per-payload diagnostics already log at
// debug; the milestone surfaces sustained noise from a misbehaving client
// (e.g. a hook script emitting garbage) without spamming the log.
const badPayloadLogInterval = 100

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
	s.serveConn(conn, conn)
}

// serveConn processes a single request/response pair on rw. It is split out
// from handleConn so tests can drive it with fake readers and writers.
func (s *Server) serveConn(r io.Reader, w io.Writer) {
	var req Request
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
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
