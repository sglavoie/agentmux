package asl

// This file owns the bounded receipt window every submission is tracked in, and
// the run-level control confirmations that precede a stop or an abort. A
// receipt carries identity and state only, so a submission can be followed from
// accepted to consumed long after the form that made it has closed.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// Body-free operation states the UI reports. They are derived from the bridge
// receipt vocabulary rather than from anything the operator typed.
const (
	OpSubmitting = "submitting"
	OpAccepted   = "accepted"
	OpConsumed   = "consumed"
	OpFailed     = "failed"
	OpExpired    = "expired"
)

// OperationState maps a bridge receipt status onto the body-free vocabulary the
// UI reports.
func OperationState(status string) string {
	switch status {
	case aslbridge.OperationAccepted:
		return OpAccepted
	case aslbridge.OperationConsumed:
		return OpConsumed
	case aslbridge.OperationExpired:
		return OpExpired
	case aslbridge.OperationRejected, aslbridge.OperationSuperseded:
		return OpFailed
	default:
		return OpSubmitting
	}
}

// OperationView is one tracked submission. It carries identity and state only,
// so the receipt history can outlive the form without holding an answer.
type OperationView struct {
	OperationID string
	Method      string
	RowID       string
	State       string
}

// OperationHistory bounds how many receipts the Runs view keeps. A receipt is a
// status line, not a journal, so the window only has to be long enough to watch
// a submission travel from accepted to consumed.
const OperationHistory = 5

// NewOperationID mints the client operation ID for one submission. It is opaque
// and unique per submission; the bridge uses it both to deduplicate a replayed
// call and, for control commands, as the durable command ID.
func NewOperationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("amx-%d", time.Now().UnixNano())
	}
	return "amx-" + hex.EncodeToString(buf[:])
}

// TrackOperation records one submission, replacing an existing entry with the
// same operation ID and evicting the oldest once the window is full.
func TrackOperation(history []OperationView, view OperationView) []OperationView {
	for i := range history {
		if history[i].OperationID == view.OperationID {
			history[i] = view
			return history
		}
	}
	history = append(history, view)
	if len(history) > OperationHistory {
		history = history[len(history)-OperationHistory:]
	}
	return history
}

// RecordOperationState advances a tracked submission's state in place. An
// operation ID the window no longer holds is ignored rather than re-added,
// because a receipt that outlived its own row is not a status line any more.
func RecordOperationState(history []OperationView, operationID, state string) {
	if operationID == "" {
		return
	}
	for i := range history {
		if history[i].OperationID == operationID {
			history[i].State = state
			return
		}
	}
}

// ApplyReceipts folds the daemon's bounded receipt window into the tracked
// operations. This is how an accepted submission becomes consumed after its
// form has already closed: the status advances without the answer being held,
// replayed, or resubmitted.
func ApplyReceipts(history []OperationView, receipts []aslbridge.Operation) {
	for _, receipt := range receipts {
		RecordOperationState(history, receipt.OperationID, OperationState(receipt.Status))
	}
}

// Confirmation action names for the shared y/n overlay.
const (
	ConfirmStop  = "asl stop"
	ConfirmAbort = "asl abort"
)

// ControlConsequence spells out what a control command actually does to an
// in-flight turn, which is the whole point of confirming it first.
func ControlConsequence(method string) string {
	if method == aslbridge.MethodRunAbort {
		return fmt.Sprintf(
			"The in-flight turn is asked to stop within %d seconds. A turn that cannot be interrupted in time degrades to stopping after the current session.",
			aslbridge.AbortGraceSeconds)
	}
	return "The current session runs to completion and the run then stops. The in-flight turn is not interrupted."
}
