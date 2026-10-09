package ipc

import (
	"sync/atomic"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestDedupeKeyScopesManagedRequestsToTheirRuntimeSession: one runtime
// session's retry replays its own response, while another session reusing the
// same request ID is applied rather than answered with the first session's
// result.
func TestDedupeKeyScopesManagedRequestsToTheirRuntimeSession(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)
	var runs atomic.Int64
	run := func() protocol.Response { runs.Add(1); return protocol.Response{OK: true} }

	a := protocol.Request{RequestID: "req-1", RuntimeSessionID: "rs-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := protocol.Request{RequestID: "req-1", RuntimeSessionID: "rs-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	c.do(dedupeKey(a), run)
	c.do(dedupeKey(a), run)
	c.do(dedupeKey(b), run)
	if got := runs.Load(); got != 2 {
		t.Fatalf("applied %d times; want once per runtime session", got)
	}
	if legacy := (protocol.Request{RequestID: "req-1"}); dedupeKey(legacy) != "req-1" {
		t.Fatalf("legacy dedupe key = %q; want the bare request ID", dedupeKey(legacy))
	}
	if dedupeKey(protocol.Request{RuntimeSessionID: a.RuntimeSessionID}) != "" {
		t.Fatal("a request without an ID must stay non-deduplicated")
	}
}

// TestDedupeCacheDoesNotReplayRetryableRefusals: a retryable refusal applied
// nothing, so the retry it invites is evaluated again instead of replayed.
func TestDedupeCacheDoesNotReplayRetryableRefusals(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)
	var runs atomic.Int64
	pending := true
	run := func() protocol.Response {
		runs.Add(1)
		if pending {
			return protocol.Response{OK: false, Retryable: true, Error: protocol.OriginReasonRuntimeSessionPending}
		}
		return protocol.Response{OK: true}
	}
	if resp := c.do("req-1", run); resp.OK {
		t.Fatalf("first response = %+v; want the retryable refusal", resp)
	}
	pending = false
	if resp := c.do("req-1", run); !resp.OK {
		t.Fatalf("retry = %+v; want a fresh evaluation", resp)
	}
	if resp := c.do("req-1", run); !resp.OK || runs.Load() != 2 {
		t.Fatalf("accepted retry was not cached: runs %d resp %+v", runs.Load(), resp)
	}
}
