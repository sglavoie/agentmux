package daemon

// Transport-level fault injection: the two ways a request can go wrong between
// the client and the handler. A lost response is an ambiguous timeout — the
// daemon applied the request but the client never learned it did — so the
// retry must replay the recorded response rather than apply the event a second
// time. Queue pressure is the opposite failure: the ingress queue is full, so
// submits degrade to inline processing and bump queue_full, but degradation
// must never turn into a dropped event.

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestFaultResponseLossRetryAppliesOnce reproduces the ambiguous-timeout
// fault: the daemon applies a request but the client never sees the response
// (connection closed early). The client's retry with the same request ID must
// not re-apply the event.
func TestFaultResponseLossRetryAppliesOnce(t *testing.T) {
	t.Parallel()

	sock, server, handler := newTestServerWithRef(t)

	req := protocol.Request{
		Type:      protocol.TypeStateUpdate,
		RequestID: "lost-response-1",
		WindowID:  7,
		Action:    "subagent",
		Event:     "SubagentStart",
	}

	// First delivery: write the request, then close without reading the
	// response — the daemon processes it but the response is lost.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	data := fmt.Sprintf(`{"type":%q,"request_id":%q,"window_id":7,"action":"subagent","event":"SubagentStart"}`+"\n",
		protocol.TypeStateUpdate, req.RequestID)
	if _, err := conn.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.Close()

	// Wait until the daemon has actually applied the first delivery.
	c := ipc.NewClientWithSocket(sock)
	deadline := time.Now().Add(3 * time.Second)
	for server.Metrics().Accepted < 1 {
		if time.Now().After(deadline) {
			t.Fatal("daemon never processed the response-lost request")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The retry (same request ID) must replay, not re-apply.
	retry, err := c.Send(req)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}
	if !retry.OK {
		t.Fatalf("retry response = %+v; want replayed OK", retry)
	}

	snap := server.Metrics()
	if snap.Accepted != 1 || snap.Duplicate < 1 {
		t.Fatalf("metrics = %+v; want exactly one accepted apply and the retry deduplicated", snap)
	}
	handler.stateMu.Lock()
	count := handler.store[7].SubagentCount
	handler.stateMu.Unlock()
	if count != 1 {
		t.Fatalf("SubagentCount = %d after response-loss retry; want 1 (applied once)", count)
	}
}
