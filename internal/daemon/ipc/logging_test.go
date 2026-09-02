package ipc

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
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
