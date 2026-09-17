// This file is the operation-result contract: what became of one dispatched
// action, reported back to whichever surface dispatched it.

package sessionactions

import "github.com/sglavoie/agentmux/tui/commands"

// RequestID distinguishes one dispatch of an action from another dispatch of
// the same action against the same row. A surface mints it when it dispatches
// and compares it when a result arrives, which is what lets a reply that has
// been overtaken be dropped instead of reported over a newer one.
//
// The zero value is "no request", so a result that was never dispatched cannot
// be mistaken for the answer to one that was.
type RequestID uint64

// Valid reports whether the ID names a dispatch.
func (r RequestID) Valid() bool { return r != 0 }

// Outcome is what became of a dispatched action. There are four, and the
// distinction that matters most is between Refused and Failed: a refusal is the
// dashboard declining to try, which is a sentence an operator can act on, while
// a failure is an attempt that reached Kitty or the harness and did not work.
// Collapsing them would leave "nothing happened" as the only report for both.
type Outcome string

const (
	// OutcomeSucceeded means the action ran and did what it said.
	OutcomeSucceeded Outcome = "succeeded"
	// OutcomeRefused means the action was not attempted: unavailable for the
	// captured row, or declined at a confirmation.
	OutcomeRefused Outcome = "refused"
	// OutcomeFailed means the action was attempted and did not complete.
	OutcomeFailed Outcome = "failed"
	// OutcomeCancelled means the operator backed out of a nested interaction
	// the action had opened — a profile picker, a confirmation — rather than
	// answering it.
	OutcomeCancelled Outcome = "cancelled"
)

// Result reports one dispatched action's fate. It carries the identity of the
// action, the dispatch, and the row, so a surface can tell which of several
// in-flight operations a late reply belongs to without holding that
// correspondence in its own state.
type Result struct {
	// Action is the identity declared in Definitions.
	Action commands.ID
	// Request is the dispatch this result answers.
	Request RequestID
	// Target is the row the action was aimed at, as captured at dispatch — not
	// re-read at reply time, when the row may be gone.
	Target Target
	// Outcome is what became of the action.
	Outcome Outcome
	// Detail is the operator-facing sentence: the refusal reason, the failure
	// message, or the confirmation of what was done. It is empty when the
	// outcome says everything there is to say.
	Detail string
	// Err is the underlying failure, present only with OutcomeFailed. Detail is
	// what a surface renders; Err is what a diagnostic reads.
	Err error
}

// Succeeded reports whether the action did what it said.
func (r Result) Succeeded() bool { return r.Outcome == OutcomeSucceeded }
