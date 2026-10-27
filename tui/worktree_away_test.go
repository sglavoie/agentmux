//go:build tuitest

package tui

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/sendform"
)

// A worktree action keeps running after the operator leaves the Worktrees
// view, and what it came to is reported where they went: it never takes them
// back to the view, nor raises a picker over whatever they are doing.

// worktreeAwayModel is a model the operator took from Worktrees to the send
// form while a worktree action was out.
func worktreeAwayModel() Model {
	m := testModel()
	m.worktreeActionInFlight = true
	m.worktreeActionTarget = &actions.WorktreeView{}
	m.mode = ModeSend
	m.send = sendform.State{Active: true, Seq: 1}
	return m
}

func lastToast(t *testing.T, m Model) Toast {
	t.Helper()
	if len(m.toasts) == 0 {
		t.Fatal("no toast was raised")
	}
	return m.toasts[len(m.toasts)-1]
}

func TestWorktreeMutationDoneAwayFromTheViewKeepsTheOperatorWhereTheyAre(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		msg      worktreeActionDoneMsg
		severity toastSeverity
	}{
		{"removed", worktreeActionDoneMsg{action: actions.WorktreeRemove, result: actions.Result{Message: "removed api-fix"}}, toastSuccess},
		{"partial", worktreeActionDoneMsg{action: actions.WorktreeRemove, err: &actions.PartialResultError{ActionError: &actions.Error{Code: "partial", Message: "branch kept"}}}, toastWarning},
		{"failed", worktreeActionDoneMsg{action: actions.WorktreeForget, err: errors.New("metadata is locked")}, toastError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := worktreeAwayModel()
			updated, _ := m.Update(tc.msg)
			got := updated.(Model)
			if got.mode != ModeSend || !got.send.Active {
				t.Fatalf("a worktree result moved the operator to %v", got.mode)
			}
			if got.worktreeActionInFlight || got.worktreeActionTarget != nil {
				t.Fatal("the finished action was left in flight")
			}
			if toast := lastToast(t, got); toast.Severity != tc.severity {
				t.Fatalf("toast = %+v, want severity %v", toast, tc.severity)
			}
		})
	}
}

func TestWorktreeReopenNeedingAChoiceAwayFromTheViewRaisesNoPicker(t *testing.T) {
	t.Parallel()

	m := worktreeAwayModel()
	updated, _ := m.Update(worktreeActionDoneMsg{action: actions.WorktreeReopen, err: &actions.Error{
		Code: "profile_required", Action: actions.WorktreeReopen, Message: "choose a profile",
	}})
	got := updated.(Model)
	if got.mode != ModeSend {
		t.Fatalf("a reopen that needs a profile moved the operator to %v", got.mode)
	}
	if toast := lastToast(t, got); toast.Severity != toastError {
		t.Fatalf("toast = %+v, want an error", toast)
	}
}

func TestWorktreeMutationDoneInTheViewStillRefreshesIt(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.mode = ModeWorktrees
	m.worktreeActionInFlight = true
	seq := m.worktreeRefreshSeq
	updated, _ := m.Update(worktreeActionDoneMsg{action: actions.WorktreeRemove, result: actions.Result{Message: "removed"}})
	got := updated.(Model)
	if got.mode != ModeWorktrees || got.worktreeRefreshSeq == seq {
		t.Fatalf("a result in the view did not refresh it (mode %v)", got.mode)
	}
}
