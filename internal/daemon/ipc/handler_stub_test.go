package ipc

import (
	"sync"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// testHandler is the transport tests' stand-in for the daemon's real request
// application. The transport is only allowed to see the three RequestHandler
// methods, so a stub that accepts every well-formed request is a complete
// substitute here: what a request MEANS is the daemon root's concern and is
// covered by that package's own tests.
type testHandler struct {
	mu         sync.Mutex
	requests   []protocol.Request
	duplicates []protocol.Request
	metricsFn  func() protocol.MetricsSnapshot
}

func newTestHandler() *testHandler { return &testHandler{} }

// Handle accepts anything that survived framing and decoding. Tests that care
// about a rejection drive it from the transport side (malformed JSON, oversize
// payload), never from the application's verdict.
func (h *testHandler) Handle(req protocol.Request) protocol.Response {
	h.mu.Lock()
	h.requests = append(h.requests, req)
	h.mu.Unlock()
	return protocol.Response{OK: true}
}

func (h *testHandler) SetMetricsSource(fn func() protocol.MetricsSnapshot) {
	h.mu.Lock()
	h.metricsFn = fn
	h.mu.Unlock()
}

func (h *testHandler) RecordDuplicateDelivery(req protocol.Request, _ *protocol.DeliveryAcknowledgement) {
	h.mu.Lock()
	h.duplicates = append(h.duplicates, req)
	h.mu.Unlock()
}
