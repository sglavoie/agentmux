package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
)

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

	var req Request
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		slog.Debug("daemon: bad request JSON", "err", err)
		writeResponse(conn, Response{OK: false, Error: "invalid JSON"})
		return
	}

	resp := s.handler.Handle(req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
