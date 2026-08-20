package daemon

import (
	"context"
	"errors"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// ASLBridge is the daemon's view of the optional agent-session-loop
// integration. It is an interface so the daemon can be tested without a bridge
// subprocess and so a daemon started without ASL simply holds nil.
type ASLBridge interface {
	Status() aslbridge.Status
	Snapshot() (aslbridge.Snapshot, aslbridge.SnapshotMeta, error)
	Receipts() []aslbridge.Operation
	QuestionBody(ctx context.Context, req aslbridge.QuestionRequest) (aslbridge.QuestionBody, error)
	Act(ctx context.Context, req aslbridge.ActionRequest) (aslbridge.Operation, error)
	Workbench(ctx context.Context, req aslbridge.WorkbenchRequest) (aslbridge.WorkbenchLink, error)
}

// SetASLBridge installs the integration. It is called once at startup, after
// the daemon socket is published, so a missing or slow ASL cannot delay the
// core daemon.
func (h *Handler) SetASLBridge(b ASLBridge) {
	h.aslBridge.Store(&b)
}

// asl returns the installed integration, or nil when the daemon is running
// without one. Every request reads it once and works from that value, so a
// request cannot see the bridge appear halfway through its own reply.
func (h *Handler) asl() ASLBridge {
	if b := h.aslBridge.Load(); b != nil {
		return *b
	}
	return nil
}

// aslStatus returns the capability block for a reply, including the disabled
// block used when no bridge is installed at all.
func (h *Handler) aslStatus() aslbridge.Status {
	bridge := h.asl()
	if bridge == nil {
		return aslbridge.Status{Mode: string(aslbridge.ModeDisabled)}
	}
	return bridge.Status()
}

// aslFailure builds a refusal that still carries the capability block, so a
// client can tell "ASL is off" from "ASL is momentarily unavailable" without a
// second request. Retryable is true only for transient unavailability.
func (h *Handler) aslFailure(err error) Response {
	status := h.aslStatus()
	return Response{
		OK:        false,
		Error:     err.Error(),
		Retryable: errors.Is(err, aslbridge.ErrUnavailable) || errors.Is(err, aslbridge.ErrMutationsDisabled),
		ASL:       &ASLResponse{Status: status},
	}
}

func (h *Handler) handleASLSnapshot() Response {
	bridge := h.asl()
	if bridge == nil {
		return h.aslFailure(aslbridge.ErrDisabled)
	}
	snapshot, meta, err := bridge.Snapshot()
	if err != nil {
		return h.aslFailure(err)
	}
	return Response{OK: true, ASL: &ASLResponse{
		Status:   bridge.Status(),
		Snapshot: &snapshot,
		Meta:     &meta,
		Receipts: bridge.Receipts(),
	}}
}

func (h *Handler) handleASLQuestion(req Request) Response {
	bridge := h.asl()
	if bridge == nil {
		return h.aslFailure(aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return h.aslFailure(aslbridge.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aslbridge.ReadTimeout)
	defer cancel()
	body, err := bridge.QuestionBody(ctx, aslbridge.QuestionRequest{
		PlanDir:    params.PlanDir,
		QuestionID: params.QuestionID,
	})
	if err != nil {
		return h.aslFailure(err)
	}
	return Response{OK: true, ASL: &ASLResponse{Status: bridge.Status(), Question: &body}}
}

func (h *Handler) handleASLAction(req Request) Response {
	bridge := h.asl()
	if bridge == nil {
		return h.aslFailure(aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return h.aslFailure(aslbridge.ErrInvalidRequest)
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
		return h.aslFailure(err)
	}
	return Response{OK: true, ASL: &ASLResponse{Status: bridge.Status(), Operation: &op}}
}

func (h *Handler) handleASLWorkbench(req Request) Response {
	bridge := h.asl()
	if bridge == nil {
		return h.aslFailure(aslbridge.ErrDisabled)
	}
	params := req.ASL
	if params == nil {
		return h.aslFailure(aslbridge.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aslbridge.ActionTimeout)
	defer cancel()
	link, err := bridge.Workbench(ctx, aslbridge.WorkbenchRequest{
		PlanDir: params.PlanDir,
		Focus:   params.Focus,
	})
	if err != nil {
		return h.aslFailure(err)
	}
	return Response{OK: true, ASL: &ASLResponse{Status: bridge.Status(), Workbench: &link}}
}
