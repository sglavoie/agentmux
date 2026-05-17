package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
)

// maxPayloadLogLen caps the number of payload bytes recorded in error logs to
// keep a single malformed request from spamming the log.
const maxPayloadLogLen = 256

// Server listens on a Unix domain socket and dispatches requests to a Handler.
// Each connection carries exactly one request/response pair.
type Server struct {
	handler *Handler
	ln      net.Listener
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
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()

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
		go s.handleConn(conn)
	}
}

// Close shuts down the listener.
func (s *Server) Close() { _ = s.ln.Close() }

func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
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
		slog.Debug("daemon: bad request JSON", "err", err, "payload", truncateForLog(payload, maxPayloadLogLen))
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
