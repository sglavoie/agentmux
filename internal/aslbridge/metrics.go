package aslbridge

import "sync"

// Metrics is the daemon-side accounting for one bridge manager. It deliberately
// holds byte counts, frame counts, and protocol method names only: no plan
// directory, no question identifier, and never any question or reply text.
type Metrics struct {
	FramesRead       uint64            `json:"frames_read"`
	FramesWritten    uint64            `json:"frames_written"`
	BytesRead        uint64            `json:"bytes_read"`
	BytesWritten     uint64            `json:"bytes_written"`
	Restarts         uint64            `json:"restarts"`
	Refusals         uint64            `json:"refusals"`
	RequestsByMethod map[string]uint64 `json:"requests_by_method,omitempty"`
	// OperationsByStatus counts observed mutation outcomes. It answers "are the
	// operator's actions landing" without naming a plan, a run, or a question.
	OperationsByStatus map[string]uint64 `json:"operations_by_status,omitempty"`
}

type metrics struct {
	mu sync.Mutex
	m  Metrics
}

// statusOther is the bucket every unrecognized operation status lands in. A
// status arrives from the bridge, so folding unknown values into one key keeps a
// newer ASL from turning this map into an unbounded, bridge-authored namespace.
const statusOther = "other"

func newMetrics() *metrics {
	return &metrics{m: Metrics{
		RequestsByMethod:   make(map[string]uint64),
		OperationsByStatus: make(map[string]uint64),
	}}
}

func (s *metrics) read(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.m.FramesRead++
	s.m.BytesRead += uint64(n)
	s.mu.Unlock()
}

func (s *metrics) wrote(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.m.FramesWritten++
	s.m.BytesWritten += uint64(n)
	s.mu.Unlock()
}

// request records one outbound call by protocol method name. Method names are
// a fixed vocabulary defined in this package, so the counter keys can never be
// attacker- or content-derived.
func (s *metrics) request(method string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.m.RequestsByMethod == nil {
		s.m.RequestsByMethod = make(map[string]uint64)
	}
	s.m.RequestsByMethod[method]++
	s.mu.Unlock()
}

func (s *metrics) operation(status string) {
	if s == nil {
		return
	}
	switch status {
	case OperationAccepted, OperationConsumed, OperationRejected, OperationExpired, OperationSuperseded:
	default:
		status = statusOther
	}
	s.mu.Lock()
	if s.m.OperationsByStatus == nil {
		s.m.OperationsByStatus = make(map[string]uint64)
	}
	s.m.OperationsByStatus[status]++
	s.mu.Unlock()
}

func (s *metrics) refusal() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.m.Refusals++
	s.mu.Unlock()
}

func (s *metrics) restart() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.m.Restarts++
	s.mu.Unlock()
}

func (s *metrics) snapshot() Metrics {
	if s == nil {
		return Metrics{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.m
	out.RequestsByMethod = make(map[string]uint64, len(s.m.RequestsByMethod))
	for k, v := range s.m.RequestsByMethod {
		out.RequestsByMethod[k] = v
	}
	out.OperationsByStatus = make(map[string]uint64, len(s.m.OperationsByStatus))
	for k, v := range s.m.OperationsByStatus {
		out.OperationsByStatus[k] = v
	}
	return out
}
