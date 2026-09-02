package ipc

import "github.com/sglavoie/agentmux/internal/daemon/protocol"

// cloneDeliveryAcknowledgement copies an acknowledgement so a replay can be
// stamped with the current wire attempt without mutating the one the dedupe
// cache retained for later duplicates. Both the cache (when it answers a
// duplicate) and serveConn (when it stamps trace/request/attempt identity onto
// the outgoing response) need it, which is why it lives with the transport
// rather than with the handler that builds the original acknowledgement.
func cloneDeliveryAcknowledgement(ack *protocol.DeliveryAcknowledgement) *protocol.DeliveryAcknowledgement {
	if ack == nil {
		return nil
	}
	clone := *ack
	return &clone
}
