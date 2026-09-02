package tui

// This file owns the two run-level control commands (stop after current and
// abort) and the model's half of the receipt window. The window's own shape and
// bound live in tui/asl; what a confirmation DOES to the model stays here.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
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
	m.aslActionTarget = &asl.ActionTarget{
		RowID:       row.RowID,
		PlanDir:     row.PlanDir,
		RunID:       row.RunID,
		ActionToken: row.ActionToken,
		Label:       asl.RowLabel(row),
	}
	if method == aslbridge.MethodRunAbort {
		m.confirmAction = asl.ConfirmAbort
	} else {
		m.confirmAction = asl.ConfirmStop
	}
	m.confirmConsequence = asl.ControlConsequence(method)
	m.confirmReturnMode = ModeASLRuns
	m.mode = ModeConfirm
	return m, nil
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
	operationID := asl.NewOperationID()
	m.aslControlInFlight = true
	m.aslControlSeq++
	m.trackASLOperation(asl.OperationView{
		OperationID: operationID,
		Method:      method,
		RowID:       target.RowID,
		State:       asl.OpSubmitting,
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
		if asl.StaleTarget(msg.err) {
			m.aslNotice = asl.TargetChangedText
			return m, m.beginASLRefresh()
		}
		m.aslNotice = asl.RefusalText(msg.err)
		return m, nil
	}
	m.aslNotice = ""
	toast := m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     fmt.Sprintf("%s: %s", sentenceCase(asl.MethodLabel(msg.method)), state),
	})
	return m, tea.Batch(toast, m.beginASLRefresh())
}

func (m *Model) trackASLOperation(view asl.OperationView) {
	m.aslOperations = asl.TrackOperation(m.aslOperations, view)
}

func (m *Model) recordASLOperationState(operationID, state string) {
	asl.RecordOperationState(m.aslOperations, operationID, state)
}

// applyASLReceipts folds the daemon's bounded receipt window into the tracked
// operations. This is how an accepted submission becomes consumed after its
// form has already closed: the status advances without the answer being held,
// replayed, or resubmitted.
func (m *Model) applyASLReceipts(receipts []aslbridge.Operation) {
	asl.ApplyReceipts(m.aslOperations, receipts)
}
