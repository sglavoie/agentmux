package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
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
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
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

// TestServer_BadPayloadCounterAndMilestone verifies that malformed payloads
// increment the daemon-wide counter and that every badPayloadLogInterval-th
// malformed request emits an info-level milestone log. Per-payload debug
// logging is already covered by TestServer_LogsBadJSONPayload; this test pins
// the sustained-noise milestone behavior that lets operators notice a hook
// script gone wild without enabling debug logging.
func TestServer_BadPayloadCounterAndMilestone(t *testing.T) {
	logs := captureSlog(t)
	srv := newTestServer()

	bad := "{this is not json}\n"
	for i := 0; i < badPayloadLogInterval; i++ {
		srv.serveConn(strings.NewReader(bad), io.Discard)
	}

	if got := srv.badPayloads.Load(); got != uint64(badPayloadLogInterval) {
		t.Fatalf("badPayloads counter = %d, want %d", got, badPayloadLogInterval)
	}

	got := logs.String()
	if !strings.Contains(got, "cumulative malformed payloads received") {
		t.Fatalf("expected milestone log entry after %d bad payloads, got: %s", badPayloadLogInterval, got)
	}

	// Below the next milestone, no additional milestone log should fire.
	preMilestoneSnapshot := strings.Count(logs.String(), "cumulative malformed payloads received")
	for i := 0; i < badPayloadLogInterval/2; i++ {
		srv.serveConn(strings.NewReader(bad), io.Discard)
	}
	postCount := strings.Count(logs.String(), "cumulative malformed payloads received")
	if postCount != preMilestoneSnapshot {
		t.Fatalf("milestone log fired between intervals: prev=%d, got=%d", preMilestoneSnapshot, postCount)
	}

	// One valid request must not bump the counter.
	good, _ := json.Marshal(Request{Type: TypeStateQuery})
	srv.serveConn(strings.NewReader(string(good)+"\n"), io.Discard)
	if got := srv.badPayloads.Load(); got != uint64(badPayloadLogInterval+badPayloadLogInterval/2) {
		t.Fatalf("valid request unexpectedly bumped counter to %d", got)
	}
}

// TestServer_BadPayloadCounterRaceFree exercises the atomic counter under
// concurrent serveConn invocations to confirm there is no torn write or
// missed increment under -race.
func TestServer_BadPayloadCounterRaceFree(t *testing.T) {
	srv := newTestServer()
	const goroutines = 32
	const perGoroutine = 5
	bad := "{this is not json}\n"

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				srv.serveConn(strings.NewReader(bad), io.Discard)
			}
		}()
	}
	wg.Wait()

	want := uint64(goroutines * perGoroutine)
	if got := srv.badPayloads.Load(); got != want {
		t.Fatalf("concurrent bad payloads: counter = %d, want %d", got, want)
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
