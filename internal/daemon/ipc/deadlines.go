package ipc

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// deadlineFor bounds the rest of a request/response handshake once the request
// line has been decoded and its type is known. Reading that first line stays
// under connDeadline, so an idle or stalled peer is still released quickly;
// only a request the daemon is known to answer slowly earns a longer window.
//
// The ASL entries mirror the deadlines the client half waits with in asl.go.
// They are read from internal/daemon/asl rather than copied, so the server can
// never cut a connection the client is still prepared to wait on — which for
// asl_action would turn a settled mutation into an unknown one.
func deadlineFor(t protocol.MessageType) time.Duration {
	switch t {
	case protocol.TypeASLAction, protocol.TypeASLWorkbench:
		return asl.ActionDeadline
	case protocol.TypeASLQuestion, protocol.TypeASLSnapshot:
		return asl.ReadDeadline
	default:
		return connDeadline
	}
}

// deadlineSetter is the part of net.Conn serveConn needs to extend the
// handshake after decoding. Test writers that do not implement it keep the
// deadline they were given, which is none.
type deadlineSetter interface {
	SetDeadline(time.Time) error
}

// extendDeadline re-arms w's deadline for the decoded request type, measured
// from now so the time spent reading the request line is not charged twice.
func extendDeadline(w any, t protocol.MessageType) {
	if conn, ok := w.(deadlineSetter); ok {
		_ = conn.SetDeadline(time.Now().Add(deadlineFor(t)))
	}
}
