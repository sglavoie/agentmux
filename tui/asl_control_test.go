package tui

// Run-level control commands (stop after current, abort) and the bounded
// receipt window every submission is tracked in. The code under test lives in
// model_asl_control.go.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

func TestASLActionsFollowProjectedCapabilities(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	m := d.Model()
	row, _ := m.selectedASLRow()

	if allowed, _ := aslActionAllowed(m, row, aslbridge.MethodRunStopAfterCurrent); !allowed {
		t.Fatal("a permitted stop was refused")
	}
	allowed, reason := aslActionAllowed(m, row, aslbridge.MethodRunAbort)
	if allowed {
		t.Fatal("abort was allowed on a run that does not permit it")
	}
	if !strings.Contains(reason, "no in-flight turn") {
		t.Fatalf("reason = %q, want the row's own disabled reason", reason)
	}

	// The footer must offer exactly what the handlers allow.
	if aslAbortAvailable(m) {
		t.Fatal("the footer offered abort on a run that refuses it")
	}
	if !aslStopAvailable(m) {
		t.Fatal("the footer hid a permitted stop")
	}
}

func TestASLStopAndAbortConfirmBeforeSubmitting(t *testing.T) {
	for _, tc := range []struct {
		key         string
		action      string
		method      string
		consequence string
	}{
		{keyASLStop, aslConfirmStop, aslbridge.MethodRunStopAfterCurrent, "runs to completion"},
		{keyASLAbort, aslConfirmAbort, aslbridge.MethodRunAbort, "120 seconds"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d, _ := enterASLRunsDriver(t)
			selectASLRun(t, d, "row-reply")
			if err := d.Key(tc.key); err != nil {
				t.Fatalf("pressing %q: %v", tc.key, err)
			}
			m := d.Model()
			if m.mode != ModeConfirm || m.confirmAction != tc.action {
				t.Fatalf("mode=%v action=%q, want ModeConfirm/%s", m.mode, m.confirmAction, tc.action)
			}
			if m.aslActionTarget == nil || m.aslActionTarget.RowID != "row-reply" {
				t.Fatalf("capture = %+v, want row-reply", m.aslActionTarget)
			}
			if !strings.Contains(m.confirmConsequence, tc.consequence) {
				t.Fatalf("consequence = %q, want it to mention %q", m.confirmConsequence, tc.consequence)
			}
			if actions := d.ASLActions(); len(actions) != 0 {
				t.Fatalf("the confirmation already submitted %+v", actions)
			}

			if err := d.Key(keyY); err != nil {
				t.Fatalf("confirming: %v", err)
			}
			actions := d.ASLActions()
			if len(actions) != 1 || actions[0].Method != tc.method {
				t.Fatalf("actions = %+v, want one %s", actions, tc.method)
			}
			if d.Model().aslActionTarget != nil {
				t.Fatal("the capture survived its submission")
			}
			if d.Model().mode != ModeASLRuns {
				t.Fatalf("mode after confirming = %v, want ModeASLRuns", d.Model().mode)
			}
		})
	}
}

func TestASLControlCancellationSubmitsNothingAndDropsTheCapture(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLStop); err != nil {
		t.Fatalf("pressing %q: %v", keyASLStop, err)
	}
	if err := d.Key(keyN); err != nil {
		t.Fatalf("declining: %v", err)
	}
	m := d.Model()
	if m.aslActionTarget != nil {
		t.Fatal("declining kept the capture")
	}
	if m.mode != ModeASLRuns {
		t.Fatalf("mode after declining = %v, want ModeASLRuns", m.mode)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("declining submitted %+v", actions)
	}
}

func TestASLConfirmationKeepsItsCaptureWhenTheListRefreshes(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLStop); err != nil {
		t.Fatalf("pressing %q: %v", keyASLStop, err)
	}

	// A snapshot landing under the overlay drops the confirmed run entirely.
	m := d.Model()
	remaining := make([]aslbridge.Row, 0, len(m.aslRows))
	for _, row := range m.aslRows {
		if row.RowID != "row-reply" {
			remaining = append(remaining, row)
		}
	}
	d.Inject(aslSnapshotLoadedMsg{
		seq:      d.Model().aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: remaining},
		status:   aslRunsFixture().ASLStatus,
	})
	if target := d.Model().aslActionTarget; target == nil || target.RowID != "row-reply" {
		t.Fatalf("capture after a refresh = %+v, want row-reply", target)
	}
	if got := aslConfirmationTitle(d.Model().aslActionTarget); got != "api-refactor" {
		t.Fatalf("confirmation title = %q, want the captured label", got)
	}
}

func TestASLOperationReceiptsAdvanceAfterTheFormCloses(t *testing.T) {
	d := openASLResponseDriver(t, "row-paused")
	typeASLAnswer(t, d, "resume please")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	m := d.Model()
	if m.aslResponse.active {
		t.Fatal("the paused form stayed open after a resume")
	}
	if len(m.aslOperations) != 1 || m.aslOperations[0].State != aslOpAccepted {
		t.Fatalf("operations = %+v, want one accepted receipt", m.aslOperations)
	}
	operationID := m.aslOperations[0].OperationID

	d.Inject(aslSnapshotLoadedMsg{
		seq:      d.Model().aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: d.Model().aslRows},
		status:   aslRunsFixture().ASLStatus,
		receipts: []aslbridge.Operation{{OperationID: operationID, Status: aslbridge.OperationConsumed}},
	})
	ops := d.Model().aslOperations
	if len(ops) != 1 || ops[0].State != aslOpConsumed {
		t.Fatalf("operations after the receipt = %+v, want consumed", ops)
	}
	assertNoASLTextLeak(t, d, "resume please")
}

func TestASLOperationHistoryStaysBounded(t *testing.T) {
	var m Model
	for i := 0; i < aslOperationHistory+3; i++ {
		m.trackASLOperation(aslOperationView{
			OperationID: fmt.Sprintf("op-%d", i),
			Method:      aslbridge.MethodQuestionReply,
			State:       aslOpSubmitting,
		})
	}
	if len(m.aslOperations) != aslOperationHistory {
		t.Fatalf("tracked %d operations, want %d", len(m.aslOperations), aslOperationHistory)
	}
	if m.aslOperations[0].OperationID != "op-3" {
		t.Fatalf("oldest retained operation = %q, want op-3", m.aslOperations[0].OperationID)
	}
}
