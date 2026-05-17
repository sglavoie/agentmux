package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// captureSlog redirects the package-level slog default to a buffer for the
// duration of the test and restores the prior default on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func newTestServer() *Server {
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_, _ string) error { return nil }
	handler := NewHandler(guard, notifier)
	return &Server{handler: handler}
}

// errReader returns the same error from every Read call. Used to simulate a
// non-EOF I/O failure that bufio.Scanner should surface via Err().
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// slowWriter writes at most chunk bytes per Write call, draining into buf.
// It models a partial-write socket where Write returns short counts.
type slowWriter struct {
	buf   bytes.Buffer
	chunk int
}

func (s *slowWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := s.chunk
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	return s.buf.Write(p[:n])
}

func TestServer_LogsScannerError(t *testing.T) {
	logs := captureSlog(t)
	srv := newTestServer()

	sentinel := errors.New("simulated read failure")
	srv.serveConn(&errReader{err: sentinel}, io.Discard)

	got := logs.String()
	if !strings.Contains(got, "daemon: read failed") {
		t.Fatalf("expected scanner-error log entry, got: %s", got)
	}
	if !strings.Contains(got, sentinel.Error()) {
		t.Fatalf("expected log to mention the underlying error %q, got: %s", sentinel.Error(), got)
	}
}

func TestServer_LogsBadJSONPayload(t *testing.T) {
	logs := captureSlog(t)
	srv := newTestServer()

	bad := "{this is not json}\n"
	var resp bytes.Buffer
	srv.serveConn(strings.NewReader(bad), &resp)

	got := logs.String()
	if !strings.Contains(got, "daemon: bad request JSON") {
		t.Fatalf("expected bad-JSON log entry, got: %s", got)
	}
	if !strings.Contains(got, "this is not json") {
		t.Fatalf("expected log to include the offending payload, got: %s", got)
	}

	// The client must still receive an OK=false JSON response.
	if !bytes.Contains(resp.Bytes(), []byte(`"ok":false`)) {
		t.Fatalf("expected error response to client, got: %s", resp.String())
	}
}

func TestServer_LogsBadJSONPayloadTruncated(t *testing.T) {
	logs := captureSlog(t)
	srv := newTestServer()

	// Build a payload larger than maxPayloadLogLen of identifiable bytes.
	huge := strings.Repeat("A", maxPayloadLogLen+200) + "\n"
	srv.serveConn(strings.NewReader(huge), io.Discard)

	got := logs.String()
	if !strings.Contains(got, "...(truncated)") {
		t.Fatalf("expected truncation marker in log, got: %s", got)
	}
	// The full payload must not appear in the log.
	if strings.Contains(got, strings.Repeat("A", maxPayloadLogLen+100)) {
		t.Fatalf("payload should have been truncated before logging; got: %s", got)
	}
}

func TestServer_HandlesPartialWrites(t *testing.T) {
	w := &slowWriter{chunk: 1}
	resp := Response{OK: true, IconState: "bolt", Allowed: true}

	writeResponse(w, resp)

	out := w.buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got: %q", out)
	}
	trimmed := strings.TrimRight(out, "\n")
	var decoded Response
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		t.Fatalf("response not valid JSON after partial writes: %v (raw: %q)", err, out)
	}
	if decoded.OK != resp.OK || decoded.Allowed != resp.Allowed || decoded.IconState != resp.IconState {
		t.Fatalf("response round-trip mismatch: got %+v want %+v", decoded, resp)
	}
}
