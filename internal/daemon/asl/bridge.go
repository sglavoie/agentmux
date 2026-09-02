// Package asl is what the daemon knows about an agent-session-loop run, and
// how it reads it.
//
// Two directions of evidence meet here. Outbound, the optional bridge
// subprocess answers snapshot/question/action/workbench requests, and this
// package owns the seam, its capability block, and the refusals that carry it.
// Inbound, ASL publishes durable per-run state that the daemon reads: the
// projection phase a discovered run is in, the supervisor mailbox records that
// say a human is being waited on, and the closure markers that say they are
// not. This package owns that reading — the parsing, the monotonicity rules,
// the per-run supervision bookkeeping, the lease-evidence decision, and the
// counters that make a silent pairing legible.
//
// It does not own what an ASL observation DOES to a window. Seeding a lease,
// retiring one, raising the question icon, and notifying about it are reducer
// mutations under the daemon's stateMu, and they stay in the daemon root with
// the reducer, the renderer, and the notifier. Like protocol, state, rendering,
// events, activity, probes, and process, this package must never import the
// daemon root, and it performs no Kitty, IPC, or journal I/O.
//
// Generic hook decoding stays in internal/daemon/events: an ASL lifecycle
// event is normalized there alongside every other hook, and transport dispatch
// stays with the daemon's IPC surface. This package supplies the ASL-specific
// vocabulary and decisions that both of those reach for.
package asl

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Bridge is the daemon's view of the optional agent-session-loop integration.
// It is an interface so the daemon can be tested without a bridge subprocess
// and so a daemon started without ASL simply holds nil.
type Bridge interface {
	Status() aslbridge.Status
	Snapshot() (aslbridge.Snapshot, aslbridge.SnapshotMeta, error)
	Receipts() []aslbridge.Operation
	QuestionBody(ctx context.Context, req aslbridge.QuestionRequest) (aslbridge.QuestionBody, error)
	Act(ctx context.Context, req aslbridge.ActionRequest) (aslbridge.Operation, error)
	Workbench(ctx context.Context, req aslbridge.WorkbenchRequest) (aslbridge.WorkbenchLink, error)
}

// BridgeHolder publishes the installed integration to concurrent request
// handlers. Its zero value is an uninstalled bridge, so a daemon running
// without ASL needs no construction step.
type BridgeHolder struct {
	bridge atomic.Pointer[Bridge]
}

// Install installs the integration. It is called once at startup, after the
// daemon socket is published, so a missing or slow ASL cannot delay the core
// daemon.
func (h *BridgeHolder) Install(b Bridge) {
	h.bridge.Store(&b)
}

// Current returns the installed integration, or nil when the daemon is running
// without one. Every request reads it once and works from that value, so a
// request cannot see the bridge appear halfway through its own reply.
func (h *BridgeHolder) Current() Bridge {
	if b := h.bridge.Load(); b != nil {
		return *b
	}
	return nil
}

// bridgeStatus returns the capability block for a reply, including the disabled
// block used when no bridge is installed at all.
func bridgeStatus(bridge Bridge) aslbridge.Status {
	if bridge == nil {
		return aslbridge.Status{Mode: string(aslbridge.ModeDisabled)}
	}
	return bridge.Status()
}

// failure builds a refusal that still carries the capability block, so a
// client can tell "ASL is off" from "ASL is momentarily unavailable" without a
// second request. Retryable is true only for transient unavailability.
func failure(bridge Bridge, err error) protocol.Response {
	return protocol.Response{
		OK:        false,
		Error:     err.Error(),
		Retryable: errors.Is(err, aslbridge.ErrUnavailable) || errors.Is(err, aslbridge.ErrMutationsDisabled),
		ASL:       &protocol.ASLResponse{Status: bridgeStatus(bridge)},
	}
}

// HandleSnapshot answers a snapshot request from the installed bridge.
func HandleSnapshot(bridge Bridge) protocol.Response {
	if bridge == nil {
		return failure(bridge, aslbridge.ErrDisabled)
	}
	snapshot, meta, err := bridge.Snapshot()
	if err != nil {
		return failure(bridge, err)
	}
	return protocol.Response{OK: true, ASL: &protocol.ASLResponse{
		Status:   bridge.Status(),
		Snapshot: &snapshot,
		Meta:     &meta,
		Receipts: bridge.Receipts(),
	}}
}

// HandleQuestion fetches one question body through the installed bridge.
func HandleQuestion(bridge Bridge, req protocol.Request) protocol.Response {
	if bridge == nil {
		return failure(bridge, aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return failure(bridge, aslbridge.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aslbridge.ReadTimeout)
	defer cancel()
	body, err := bridge.QuestionBody(ctx, aslbridge.QuestionRequest{
		PlanDir:    params.PlanDir,
		QuestionID: params.QuestionID,
	})
	if err != nil {
		return failure(bridge, err)
	}
	return protocol.Response{OK: true, ASL: &protocol.ASLResponse{Status: bridge.Status(), Question: &body}}
}

// HandleAction submits one explicit mutation through the installed bridge.
func HandleAction(bridge Bridge, req protocol.Request) protocol.Response {
	if bridge == nil {
		return failure(bridge, aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return failure(bridge, aslbridge.ErrInvalidRequest)
	}
	// The action budget is the bridge's own acceptance timeout. A mutation is
	// never retried here: an ambiguous outcome is reported to the caller, which
	// can re-read the snapshot rather than risk applying it twice.
	ctx, cancel := context.WithTimeout(context.Background(), aslbridge.ActionTimeout)
	defer cancel()
	op, err := bridge.Act(ctx, aslbridge.ActionRequest{
		Method:      params.Method,
		PlanDir:     params.PlanDir,
		RunID:       params.RunID,
		QuestionID:  params.QuestionID,
		OperationID: params.OperationID,
		ActionToken: params.ActionToken,
		Resolution:  params.Resolution,
		Note:        params.Note,
		Text:        params.Text,
	})
	if err != nil {
		return failure(bridge, err)
	}
	return protocol.Response{OK: true, ASL: &protocol.ASLResponse{Status: bridge.Status(), Operation: &op}}
}

// HandleWorkbench asks the installed bridge for a deep link into one plan's
// detail view.
func HandleWorkbench(bridge Bridge, req protocol.Request) protocol.Response {
	if bridge == nil {
		return failure(bridge, aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return failure(bridge, aslbridge.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aslbridge.ActionTimeout)
	defer cancel()
	link, err := bridge.Workbench(ctx, aslbridge.WorkbenchRequest{
		PlanDir: params.PlanDir,
		Focus:   params.Focus,
	})
	if err != nil {
		return failure(bridge, err)
	}
	return protocol.Response{OK: true, ASL: &protocol.ASLResponse{Status: bridge.Status(), Workbench: &link}}
}
