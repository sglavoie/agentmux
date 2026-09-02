package asl

// The client half of an ASL exchange, minus the transport. Decoding a reply is
// an ASL fact — which refusals are typed, which results are mandatory — while
// sending the request and waiting on the socket belongs to the daemon's IPC
// client.

import (
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// ReadDeadline and ActionDeadline bound the client half of an ASL exchange.
// Each leaves room for the daemon's own bridge timeout plus IPC, so a client
// gives up only after the daemon has already given up — never before, which
// would turn a settled mutation into an unknown one.
var (
	ReadDeadline   = aslbridge.ReadTimeout + time.Second
	ActionDeadline = aslbridge.ActionTimeout + time.Second
)

// Result unwraps the ASL block of a reply, turning a refusal into an error that
// keeps the daemon's own typed refusal text.
func Result(resp protocol.Response, err error) (protocol.ASLResponse, error) {
	if err != nil {
		return protocol.ASLResponse{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			return protocol.ASLResponse{}, errors.New("daemon refused the ASL request")
		}
		return protocol.ASLResponse{}, errors.New(resp.Error)
	}
	if resp.ASL == nil {
		return protocol.ASLResponse{}, errors.New("daemon returned no ASL result")
	}
	return *resp.ASL, nil
}

// ResponseStatus recovers the capability block from a reply that may have
// refused, so a caller can report why ASL is unavailable rather than only that
// it is.
func ResponseStatus(resp protocol.Response) aslbridge.Status {
	if resp.ASL == nil {
		return aslbridge.Status{}
	}
	return resp.ASL.Status
}
