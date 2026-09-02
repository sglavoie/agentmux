package ipc

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

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
	good, _ := json.Marshal(protocol.Request{Type: protocol.TypeStateQuery})
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

// TestServer_AcceptsLargeRequest exercises the raised scanner buffer ceiling:
// payloads that would silently exceed bufio.Scanner's default 64 KiB limit
// must now be parsed successfully when within maxRequestPayloadBytes.
func TestServer_AcceptsLargeRequest(t *testing.T) {
	srv := newTestServer()

	// Build a request whose total wire length exceeds 64 KiB but stays well
	// under the new 1 MiB ceiling. SessionTitle is the largest free-form field
	// the daemon accepts, so it is a representative inflation vector.
	bigTitle := strings.Repeat("x", 80*1024)
	req := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", SessionTitle: bigTitle}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) <= 64*1024 {
		t.Fatalf("test payload (%d bytes) does not exercise the >64KiB path", len(payload))
	}
	payload = append(payload, '\n')

	var resp bytes.Buffer
	srv.serveConn(bytes.NewReader(payload), &resp)

	if !bytes.Contains(resp.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("large valid request rejected: %s", resp.String())
	}
}

// TestServer_RejectsOversizeRequest verifies that a payload larger than
// maxRequestPayloadBytes is rejected explicitly rather than silently
// truncated by bufio.Scanner. The rejection must surface as an OK=false
// response and bump the malformed-payload counter so operators can detect
// a misbehaving client.
func TestServer_RejectsOversizeRequest(t *testing.T) {
	srv := newTestServer()

	huge := strings.Repeat("A", maxRequestPayloadBytes+128) + "\n"

	var resp bytes.Buffer
	srv.serveConn(strings.NewReader(huge), &resp)

	if !bytes.Contains(resp.Bytes(), []byte(`"ok":false`)) {
		t.Fatalf("expected ok=false response for oversize payload, got: %s", resp.String())
	}
	if !bytes.Contains(resp.Bytes(), []byte(`request payload exceeds limit`)) {
		t.Fatalf("expected explicit oversize error, got: %s", resp.String())
	}
	if got := srv.badPayloads.Load(); got != 1 {
		t.Fatalf("badPayloads counter = %d, want 1 after oversize request", got)
	}
}
