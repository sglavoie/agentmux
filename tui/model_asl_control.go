package tui

// This file owns the two run-level control commands (stop after current and
// abort) and the bounded receipt window every submission is tracked in. A
// receipt carries identity and state only, so a submission can be followed from
// accepted to consumed long after the form that made it has closed.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// Body-free operation states the UI reports. They are derived from the bridge
// receipt vocabulary rather than from anything the operator typed.
const (
	aslOpSubmitting = "submitting"
	aslOpAccepted   = "accepted"
	aslOpConsumed   = "consumed"
	aslOpFailed     = "failed"
	aslOpExpired    = "expired"
)

func aslOperationState(status string) string {
	switch status {
	case aslbridge.OperationAccepted:
		return aslOpAccepted
	case aslbridge.OperationConsumed:
		return aslOpConsumed
	case aslbridge.OperationExpired:
		return aslOpExpired
	case aslbridge.OperationRejected, aslbridge.OperationSuperseded:
		return aslOpFailed
	default:
		return aslOpSubmitting
	}
}

// aslOperationView is one tracked submission. It carries identity and state
// only, so the receipt history can outlive the form without holding an answer.
type aslOperationView struct {
	OperationID string
	Method      string
	RowID       string
	State       string
}

// aslOperationHistory bounds how many receipts the Runs view keeps. A receipt
// is a status line, not a journal, so the window only has to be long enough to
// watch a submission travel from accepted to consumed.
const aslOperationHistory = 5

// newASLOperationID mints the client operation ID for one submission. It is
// opaque and unique per submission; the bridge uses it both to deduplicate a
// replayed call and, for control commands, as the durable command ID.
func newASLOperationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("amx-%d", time.Now().UnixNano())
	}
	return "amx-" + hex.EncodeToString(buf[:])
}

// Confirmation action names for the shared y/n overlay.
const (
	aslConfirmStop  = "asl stop"
	aslConfirmAbort = "asl abort"
)

// confirmASLControl captures the selected run and raises the shared
// confirmation overlay. Nothing is queued until the operator answers y.
func (m Model) confirmASLControl(method string) (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	if allowed, reason := aslActionAllowed(m, row, method); !allowed {
		m.aslNotice = reason
		return m, nil
	}
	if m.aslControlInFlight {
		m.aslNotice = "a control action for this view is still in flight"
		return m, nil
	}
	m.aslNotice = ""
	m.aslActionTarget = &aslActionTarget{
		RowID:       row.RowID,
		PlanDir:     row.PlanDir,
		RunID:       row.RunID,
		ActionToken: row.ActionToken,
		Label:       aslRowLabel(row),
	}
	if method == aslbridge.MethodRunAbort {
		m.confirmAction = aslConfirmAbort
	} else {
		m.confirmAction = aslConfirmStop
	}
	m.confirmConsequence = aslControlConsequence(method)
	m.confirmReturnMode = ModeASLRuns
	m.mode = ModeConfirm
	return m, nil
}

func aslControlConsequence(method string) string {
	if method == aslbridge.MethodRunAbort {
		return fmt.Sprintf(
			"The in-flight turn is asked to stop within %d seconds. A turn that cannot be interrupted in time degrades to stopping after the current session.",
			aslbridge.AbortGraceSeconds)
	}
	return "The current session runs to completion and the run then stops. The in-flight turn is not interrupted."
}

// submitASLControl queues one control command against the captured target. The
// capture is consumed here, so a second confirmation cannot reuse it.
func (m Model) submitASLControl(method string) (tea.Model, tea.Cmd) {
	if m.aslActionTarget == nil {
		return m, nil
	}
	target := *m.aslActionTarget
	m.aslActionTarget = nil
	if m.aslControlInFlight {
		return m, nil
	}
	operationID := newASLOperationID()
	m.aslControlInFlight = true
	m.aslControlSeq++
	m.trackASLOperation(aslOperationView{
		OperationID: operationID,
		Method:      method,
		RowID:       target.RowID,
		State:       aslOpSubmitting,
	})
	return m, m.runASLActionCmd(m.aslControlSeq, true, target.RowID, aslbridge.ActionRequest{
		Method:      method,
		PlanDir:     target.PlanDir,
		RunID:       target.RunID,
		OperationID: operationID,
		ActionToken: target.ActionToken,
	})
}

func (m Model) handleASLControlDone(msg aslActionDoneMsg, state string) (tea.Model, tea.Cmd) {
	if msg.seq != m.aslControlSeq {
		return m, nil
	}
	m.aslControlInFlight = false
	if msg.err != nil {
		if aslStaleTarget(msg.err) {
			m.aslNotice = aslTargetChangedText
			return m, m.beginASLRefresh()
		}
		m.aslNotice = aslRefusalText(msg.err)
		return m, nil
	}
	m.aslNotice = ""
	toast := m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     fmt.Sprintf("%s: %s", sentenceCase(aslMethodLabel(msg.method)), state),
	})
	return m, tea.Batch(toast, m.beginASLRefresh())
}

func (m *Model) trackASLOperation(view aslOperationView) {
	for i := range m.aslOperations {
		if m.aslOperations[i].OperationID == view.OperationID {
			m.aslOperations[i] = view
			return
		}
	}
	m.aslOperations = append(m.aslOperations, view)
	if len(m.aslOperations) > aslOperationHistory {
		m.aslOperations = m.aslOperations[len(m.aslOperations)-aslOperationHistory:]
	}
}

func (m *Model) recordASLOperationState(operationID, state string) {
	if operationID == "" {
		return
	}
	for i := range m.aslOperations {
		if m.aslOperations[i].OperationID == operationID {
			m.aslOperations[i].State = state
			return
		}
	}
}

// applyASLReceipts folds the daemon's bounded receipt window into the tracked
// operations. This is how an accepted submission becomes consumed after its
// form has already closed: the status advances without the answer being held,
// replayed, or resubmitted.
func (m *Model) applyASLReceipts(receipts []aslbridge.Operation) {
	for _, receipt := range receipts {
		m.recordASLOperationState(receipt.OperationID, aslOperationState(receipt.Status))
	}
}
